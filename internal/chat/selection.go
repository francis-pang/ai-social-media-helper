package chat

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/fpang/gemini-media-cli/internal/assets"
	"github.com/fpang/gemini-media-cli/internal/filehandler"
	"github.com/google/generative-ai-go/genai"
	"github.com/rs/zerolog/log"
)

// DefaultMaxPhotos is the default maximum number of photos to select.
const DefaultMaxPhotos = 20

// SelectionSystemInstruction provides context for quality-agnostic photo selection tasks.
// Loaded from embedded prompt file. See DDR-019: Externalized Prompt Templates.
// See DDR-016: Quality-Agnostic Metadata-Driven Photo Selection.
// See DDR-017: Francis Reference Photo for Person Identification.
var SelectionSystemInstruction = assets.SelectionSystemPrompt

// BuildPhotoSelectionPrompt creates a prompt asking Gemini to rank and select photos
// using quality-agnostic, metadata-driven criteria.
// It includes metadata context and user trip description for informed selection.
func BuildPhotoSelectionPrompt(files []*filehandler.MediaFile, maxPhotos int, tripContext string) string {
	var sb strings.Builder

	sb.WriteString("## Photo Selection Task\n\n")
	sb.WriteString(fmt.Sprintf("You are reviewing %d photos from a directory for an Instagram carousel (max %d).\n\n", len(files), maxPhotos))

	// User context section
	sb.WriteString("### Trip/Event Context\n\n")
	if tripContext != "" {
		sb.WriteString(tripContext)
		sb.WriteString("\n\n")
	} else {
		sb.WriteString("No context provided. Infer the event type from photos and metadata.\n\n")
	}

	sb.WriteString("### Selection Criteria\n\n")
	sb.WriteString("Remember: Quality is NOT a criterion unless the photo is completely unusable.\n\n")
	sb.WriteString("Focus on:\n")
	sb.WriteString("- **Subject Diversity** (Highest): food, architecture, landscape, people, activities\n")
	sb.WriteString("- **Scene Representation**: ensure each sub-event/location is represented\n")
	sb.WriteString("- **Enhancement Potential**: when choosing between duplicates, pick easiest to enhance\n")
	sb.WriteString("- **People Variety** (Lower): different groups/individuals\n")
	sb.WriteString("- **Time of Day** (Tiebreaker): only use to break ties\n\n")

	sb.WriteString("### Scene Detection\n\n")
	sb.WriteString("Detect scenes using combined signals:\n")
	sb.WriteString("- **Visual similarity**: similar backgrounds, subjects, composition\n")
	sb.WriteString("- **Time gaps**: 2+ hour gaps suggest a new scene/sub-event\n")
	sb.WriteString("- **Location gaps**: 1km+ GPS distance suggests a new venue\n\n")

	sb.WriteString("### Photo Metadata\n\n")
	sb.WriteString("Below is the metadata for each photo. Thumbnails are provided in the same order.\n\n")

	for i, file := range files {
		sb.WriteString(fmt.Sprintf("**Photo %d: %s**\n", i+1, filepath.Base(file.Path)))

		if file.Metadata != nil {
			if file.Metadata.HasGPSData() {
				lat, lon := file.Metadata.GetGPS()
				sb.WriteString(fmt.Sprintf("- GPS: %.6f, %.6f\n", lat, lon))
			}
			if file.Metadata.HasDateData() {
				date := file.Metadata.GetDate()
				sb.WriteString(fmt.Sprintf("- Date: %s\n", date.Format("Monday, January 2, 2006 at 3:04 PM")))
			}
			// Add camera info for images
			if imgMeta, ok := file.Metadata.(*filehandler.ImageMetadata); ok {
				if imgMeta.CameraMake != "" || imgMeta.CameraModel != "" {
					sb.WriteString(fmt.Sprintf("- Camera: %s %s\n", imgMeta.CameraMake, imgMeta.CameraModel))
				}
			}
		} else {
			sb.WriteString("- No metadata available\n")
		}
		sb.WriteString("\n")
	}

	sb.WriteString("### Required Output Format\n\n")
	sb.WriteString("You MUST provide all three sections:\n\n")

	sb.WriteString("#### 1. Ranked List\n")
	sb.WriteString("```\n")
	sb.WriteString("RANK | PHOTO | SCENE | JUSTIFICATION\n")
	sb.WriteString("-----|-------|-------|---------------\n")
	sb.WriteString("1    | Photo X: filename.jpg | Scene Name | Why this photo was selected\n")
	sb.WriteString("2    | Photo Y: filename.heic | Scene Name | Why this photo was selected\n")
	sb.WriteString("... (continue for all selected photos, up to 20)\n")
	sb.WriteString("```\n\n")

	sb.WriteString("#### 2. Scene Grouping\n")
	sb.WriteString("```\n")
	sb.WriteString("## Detected Scenes\n\n")
	sb.WriteString("### Scene 1: [Name] (GPS: [Venue/Coordinates], [Time Range])\n")
	sb.WriteString("Total photos in scene: X\n")
	sb.WriteString("Selected: Y photos\n")
	sb.WriteString("- filename.jpg: Brief description (SELECTED)\n")
	sb.WriteString("- filename2.jpg: Brief description (SELECTED)\n")
	sb.WriteString("...\n")
	sb.WriteString("```\n\n")

	sb.WriteString("#### 3. Exclusion Report (MANDATORY - DO NOT SKIP)\n")
	sb.WriteString("You MUST list EVERY photo that was not selected with a specific reason.\n\n")
	sb.WriteString("```\n")
	sb.WriteString("## Excluded Photos\n\n")
	sb.WriteString("### Near-Duplicates (X photos)\n")
	sb.WriteString("| Photo | Duplicate Of | Reason Not Chosen |\n")
	sb.WriteString("|-------|--------------|-------------------|\n")
	sb.WriteString("| Photo 3: filename.jpg | Photo 2: IMG_001.jpg | Same scene, worse expressions |\n")
	sb.WriteString("...\n\n")
	sb.WriteString("### Quality Issues (X photos)\n")
	sb.WriteString("| Photo | Issue | Enhancement Feasible? |\n")
	sb.WriteString("|-------|-------|----------------------|\n")
	sb.WriteString("| Photo 7: filename.jpg | Extremely blurry | No - subject unrecognizable |\n")
	sb.WriteString("...\n\n")
	sb.WriteString("### Content Mismatch (X photos)\n")
	sb.WriteString("| Photo | Issue |\n")
	sb.WriteString("|-------|-------|\n")
	sb.WriteString("| Photo 5: filename.jpg | Content doesn't fit event theme |\n")
	sb.WriteString("...\n\n")
	sb.WriteString("### Redundant Scenes (X photos)\n")
	sb.WriteString("| Photo | Scene | Reason |\n")
	sb.WriteString("|-------|-------|--------|\n")
	sb.WriteString("| Photo 12: filename.jpg | Restaurant | Scene already well-represented |\n")
	sb.WriteString("...\n")
	sb.WriteString("```\n")

	return sb.String()
}

// AskPhotoSelection sends thumbnails with metadata to Gemini and asks for photo selection
// using quality-agnostic, metadata-driven criteria.
// tripContext provides optional user description of the trip/event.
// Returns the structured selection with ranked list, scene grouping, and exclusion report.
func AskPhotoSelection(ctx context.Context, client *genai.Client, files []*filehandler.MediaFile, maxPhotos int, tripContext string) (string, error) {
	log.Info().
		Int("total_photos", len(files)).
		Int("max_select", maxPhotos).
		Bool("has_context", tripContext != "").
		Msg("Starting quality-agnostic photo selection with Gemini")

	// Build the prompt with metadata and context
	prompt := BuildPhotoSelectionPrompt(files, maxPhotos, tripContext)

	// Configure model with system instruction
	model := client.GenerativeModel(GetModelName())
	model.SystemInstruction = &genai.Content{
		Parts: []genai.Part{
			genai.Text(SelectionSystemInstruction),
		},
	}

	// Build parts: reference photo first, then thumbnails, then prompt
	var parts []genai.Part

	// Add Francis reference photo as the first image (DDR-017)
	log.Debug().
		Int("reference_bytes", len(assets.FrancisReferencePhoto)).
		Msg("Including Francis reference photo for identification")
	parts = append(parts, genai.Blob{
		MIMEType: assets.FrancisReferenceMIMEType,
		Data:     assets.FrancisReferencePhoto,
	})

	// Generate and add thumbnails
	log.Info().Msg("Generating thumbnails for all photos...")

	for i, file := range files {
		thumbData, mimeType, err := filehandler.GenerateThumbnail(file, filehandler.DefaultThumbnailMaxDimension)
		if err != nil {
			log.Warn().Err(err).Str("file", file.Path).Msg("Failed to generate thumbnail, skipping")
			continue
		}

		log.Debug().
			Int("index", i+1).
			Str("file", filepath.Base(file.Path)).
			Int("thumb_bytes", len(thumbData)).
			Str("mime", mimeType).
			Msg("Thumbnail ready")

		parts = append(parts, genai.Blob{
			MIMEType: mimeType,
			Data:     thumbData,
		})
	}

	// Add the text prompt at the end
	parts = append(parts, genai.Text(prompt))

	log.Info().
		Int("num_thumbnails", len(parts)-2). // -2 for reference photo and prompt
		Msg("Sending thumbnails to Gemini for quality-agnostic selection...")

	// Generate content
	resp, err := model.GenerateContent(ctx, parts...)
	if err != nil {
		log.Error().Err(err).Msg("Failed to generate selection from Gemini")
		return "", fmt.Errorf("failed to generate content: %w", err)
	}

	if resp == nil || len(resp.Candidates) == 0 {
		log.Warn().Msg("Received empty response from Gemini")
		return "", fmt.Errorf("received empty response from Gemini API")
	}

	// Extract text from response
	var result strings.Builder
	for _, candidate := range resp.Candidates {
		if candidate.Content != nil {
			for _, part := range candidate.Content.Parts {
				if text, ok := part.(genai.Text); ok {
					result.WriteString(string(text))
				}
			}
		}
	}

	response := result.String()
	log.Info().
		Int("response_length", len(response)).
		Msg("Photo selection complete")

	return response, nil
}
