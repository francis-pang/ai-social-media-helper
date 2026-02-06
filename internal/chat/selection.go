package chat

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpang/gemini-media-cli/internal/assets"
	"github.com/fpang/gemini-media-cli/internal/filehandler"
	"github.com/google/generative-ai-go/genai"
	"github.com/rs/zerolog/log"
)

// DefaultMaxPhotos is the default maximum number of photos to select.
const DefaultMaxPhotos = 20

// DefaultMaxMedia is the default maximum number of media items to select.
const DefaultMaxMedia = 20

// SelectionSystemInstruction provides context for quality-agnostic photo selection tasks.
// Loaded from embedded prompt file. See DDR-019: Externalized Prompt Templates.
// See DDR-016: Quality-Agnostic Metadata-Driven Photo Selection.
// See DDR-017: Francis Reference Photo for Person Identification.
var SelectionSystemInstruction = assets.SelectionSystemPrompt

// MediaSelectionSystemInstruction provides context for mixed media (photos + videos) selection tasks.
// See DDR-016: Quality-Agnostic Metadata-Driven Photo Selection.
// See DDR-017: Francis Reference Photo for Person Identification.
// See DDR-020: Mixed Media Selection Strategy.
const MediaSelectionSystemInstruction = `You are selecting media items (photos AND videos) for an Instagram carousel (up to 20 items). The user has access to Google's comprehensive photo enhancement suite including Magic Editor, Reimagine, Help Me Edit, Unblur, Magic Eraser, Portrait Light, Best Take, Face Retouch, Auto-Enhance, Portrait Blur, and Sky & Color Pop.

REFERENCE PHOTO: The first image provided is a reference photo of Francis, the owner of this media. Use this to identify Francis in the candidate media. The candidate media starts from the second file.

VIDEO PREVIEWS: Videos have been compressed for efficient analysis. Judge content, not compression artifacts. The original videos are high quality.

CRITICAL: Media quality is NOT a selection criterion. Only exclude items that are completely unusable (extremely blurry, corrupt, accidental shots that cannot be enhanced to Instagram quality even with these tools).

EQUAL WEIGHTING: Photos and videos compete equally. Select the best media regardless of type. A compelling 15-second video may be better than multiple similar photos.

AUDIO ANALYSIS: For videos, consider audio content (music, speech, ambient sounds) in your selection. Videos with engaging audio may enhance the carousel's storytelling.

SELECTION PRIORITIES (in order of weight):

1. SUBJECT DIVERSITY (Highest Priority)
   - Select media covering different subjects: food, architecture, landscape, people, activities, objects
   - Each item should add a distinct type of content
   - Prioritize DEPTH over coverage: allocate more items to visually interesting scenes, fewer to less interesting ones

2. SCENE REPRESENTATION
   - Detect scenes using: visual similarity + time gaps (2+ hours) + location gaps (1km+)
   - Use GPS coordinates to identify different venues/locations
   - Ensure each major sub-event/location is represented

3. MEDIA TYPE SYNERGY
   - Consider whether a scene is better captured as photo or video
   - Action/motion scenes may benefit from video
   - Static/compositional scenes may work better as photos

4. ENHANCEMENT POTENTIAL (For Duplicates Only)
   - When choosing between similar items, pick the one requiring least enhancement effort
   - Consider: exposure, blur, composition, expressions

5. PEOPLE VARIETY (Lower Priority)
   - Include different groups or individuals if relevant to the event
   - Secondary to subject and scene diversity

6. TIME OF DAY (Tiebreaker Only)
   - Only use to break ties between otherwise equal items
   - Prefer variety across morning/afternoon/evening if choosing between equals

DEDUPLICATION: Strictly one item per scene/moment. Recommend best candidate based on content and enhancement potential.

OUTPUT FORMAT: You MUST provide ALL THREE sections in your response. Incomplete responses are not acceptable.

1. RANKED LIST - A table with columns: RANK | ITEM | TYPE | SCENE | JUSTIFICATION
   - Order by recommendation priority for the Instagram carousel
   - Include up to 20 items maximum
   - TYPE should be "Photo" or "Video"
   - For videos, note any audio highlights in justification

2. SCENE GROUPING - Group media by detected scenes
   - Include scene name, GPS location/venue if known, time range
   - List selected items for each scene with brief description
   - Indicate media type for each item

3. EXCLUSION REPORT (MANDATORY) - You MUST explain why EVERY non-selected item was excluded
   - This section is REQUIRED - do not skip it
   - List EACH excluded item by name with a specific reason
   - Group by exclusion reason: Near-Duplicates, Quality Issues, Redundant Scenes, Content Mismatch
   - Be specific: "duplicate of IMG_001" not just "duplicate"
   - Example: "Media 5: 2024-10-13 10.27.34.jpg - Excluded: Content doesn't fit event theme"
   - If an item is excluded for sensitive/inappropriate content, still list it with reason "Content not suitable for post"`

// BuildMediaSelectionPrompt creates a prompt asking Gemini to rank and select media items
// (photos AND videos) using quality-agnostic, metadata-driven criteria.
// It includes metadata context and user trip description for informed selection.
// See DDR-020: Mixed Media Selection Strategy.
func BuildMediaSelectionPrompt(files []*filehandler.MediaFile, maxItems int, tripContext string) string {
	var sb strings.Builder

	// Count media types
	var imageCount, videoCount int
	for _, file := range files {
		ext := strings.ToLower(filepath.Ext(file.Path))
		if filehandler.IsImage(ext) {
			imageCount++
		} else if filehandler.IsVideo(ext) {
			videoCount++
		}
	}

	sb.WriteString("## Media Selection Task\n\n")
	sb.WriteString(fmt.Sprintf("You are reviewing %d media items (%d photos, %d videos) for an Instagram carousel (max %d).\n\n",
		len(files), imageCount, videoCount, maxItems))

	// User context section
	sb.WriteString("### Trip/Event Context\n\n")
	if tripContext != "" {
		sb.WriteString(tripContext)
		sb.WriteString("\n\n")
	} else {
		sb.WriteString("No context provided. Infer the event type from media and metadata.\n\n")
	}

	sb.WriteString("### Selection Criteria\n\n")
	sb.WriteString("Remember: Quality is NOT a criterion unless the item is completely unusable.\n")
	sb.WriteString("Photos and videos compete EQUALLY - select the best media regardless of type.\n\n")
	sb.WriteString("Focus on:\n")
	sb.WriteString("- **Subject Diversity** (Highest): food, architecture, landscape, people, activities\n")
	sb.WriteString("- **Scene Representation**: ensure each sub-event/location is represented\n")
	sb.WriteString("- **Media Type Synergy**: consider which type best captures each moment\n")
	sb.WriteString("- **Audio Content** (Videos): consider music, speech, ambient sounds\n")
	sb.WriteString("- **Enhancement Potential**: when choosing between duplicates, pick easiest to enhance\n")
	sb.WriteString("- **People Variety** (Lower): different groups/individuals\n")
	sb.WriteString("- **Time of Day** (Tiebreaker): only use to break ties\n\n")

	sb.WriteString("### Scene Detection\n\n")
	sb.WriteString("Detect scenes using combined signals:\n")
	sb.WriteString("- **Visual similarity**: similar backgrounds, subjects, composition\n")
	sb.WriteString("- **Time gaps**: 2+ hour gaps suggest a new scene/sub-event\n")
	sb.WriteString("- **Location gaps**: 1km+ GPS distance suggests a new venue\n\n")

	sb.WriteString("### Media Metadata\n\n")
	sb.WriteString("Below is the metadata for each media item. Media files are provided in the same order.\n\n")

	for i, file := range files {
		ext := strings.ToLower(filepath.Ext(file.Path))
		mediaType := "Photo"
		if filehandler.IsVideo(ext) {
			mediaType = "Video"
		}

		sb.WriteString(fmt.Sprintf("**Media %d: %s** [%s]\n", i+1, filepath.Base(file.Path), mediaType))

		if file.Metadata != nil {
			if file.Metadata.HasGPSData() {
				lat, lon := file.Metadata.GetGPS()
				sb.WriteString(fmt.Sprintf("- GPS: %.6f, %.6f\n", lat, lon))
			}
			if file.Metadata.HasDateData() {
				date := file.Metadata.GetDate()
				sb.WriteString(fmt.Sprintf("- Date: %s\n", date.Format("Monday, January 2, 2006 at 3:04 PM")))
			}

			// Add type-specific metadata
			switch m := file.Metadata.(type) {
			case *filehandler.ImageMetadata:
				if m.CameraMake != "" || m.CameraModel != "" {
					sb.WriteString(fmt.Sprintf("- Camera: %s %s\n", m.CameraMake, m.CameraModel))
				}
			case *filehandler.VideoMetadata:
				if m.Duration > 0 {
					sb.WriteString(fmt.Sprintf("- Duration: %s\n", formatVideoDuration(m.Duration)))
				}
				if m.Width > 0 && m.Height > 0 {
					sb.WriteString(fmt.Sprintf("- Resolution: %dx%d\n", m.Width, m.Height))
				}
				hasAudio := m.AudioCodec != ""
				sb.WriteString(fmt.Sprintf("- Has Audio: %v\n", hasAudio))
				if hasAudio {
					sb.WriteString("- Audio Note: Analyze audio for music, speech, ambient sounds\n")
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
	sb.WriteString("RANK | ITEM | TYPE | SCENE | JUSTIFICATION\n")
	sb.WriteString("-----|------|------|-------|---------------\n")
	sb.WriteString("1    | Media X: filename.jpg | Photo | Scene Name | Why this item was selected\n")
	sb.WriteString("2    | Media Y: filename.mp4 | Video | Scene Name | Why selected (audio: music playing)\n")
	sb.WriteString("... (continue for all selected items, up to 20)\n")
	sb.WriteString("```\n\n")

	sb.WriteString("#### 2. Scene Grouping\n")
	sb.WriteString("```\n")
	sb.WriteString("## Detected Scenes\n\n")
	sb.WriteString("### Scene 1: [Name] (GPS: [Venue/Coordinates], [Time Range])\n")
	sb.WriteString("Total items in scene: X\n")
	sb.WriteString("Selected: Y items\n")
	sb.WriteString("- filename.jpg [Photo]: Brief description (SELECTED)\n")
	sb.WriteString("- filename.mp4 [Video]: Brief description (SELECTED)\n")
	sb.WriteString("...\n")
	sb.WriteString("```\n\n")

	sb.WriteString("#### 3. Exclusion Report (MANDATORY - DO NOT SKIP)\n")
	sb.WriteString("You MUST list EVERY item that was not selected with a specific reason.\n\n")
	sb.WriteString("```\n")
	sb.WriteString("## Excluded Media\n\n")
	sb.WriteString("### Near-Duplicates (X items)\n")
	sb.WriteString("| Item | Duplicate Of | Reason Not Chosen |\n")
	sb.WriteString("|------|--------------|-------------------|\n")
	sb.WriteString("| Media 3: filename.jpg | Media 2: IMG_001.jpg | Same scene, worse expressions |\n")
	sb.WriteString("...\n\n")
	sb.WriteString("### Quality Issues (X items)\n")
	sb.WriteString("| Item | Issue | Enhancement Feasible? |\n")
	sb.WriteString("|------|-------|----------------------|\n")
	sb.WriteString("| Media 7: filename.jpg | Extremely blurry | No - subject unrecognizable |\n")
	sb.WriteString("...\n\n")
	sb.WriteString("### Content Mismatch (X items)\n")
	sb.WriteString("| Item | Issue |\n")
	sb.WriteString("|------|-------|\n")
	sb.WriteString("| Media 5: filename.mp4 | Content doesn't fit event theme |\n")
	sb.WriteString("...\n\n")
	sb.WriteString("### Redundant Scenes (X items)\n")
	sb.WriteString("| Item | Scene | Reason |\n")
	sb.WriteString("|------|-------|--------|\n")
	sb.WriteString("| Media 12: filename.jpg | Restaurant | Scene already well-represented |\n")
	sb.WriteString("...\n")
	sb.WriteString("```\n")

	return sb.String()
}

// formatVideoDuration formats a duration in a human-readable format for prompts.
func formatVideoDuration(d time.Duration) string {
	totalSeconds := int(d.Seconds())
	minutes := totalSeconds / 60
	seconds := totalSeconds % 60
	if minutes > 0 {
		return fmt.Sprintf("%d:%02d", minutes, seconds)
	}
	return fmt.Sprintf("0:%02d", seconds)
}

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

// AskMediaSelection sends mixed media (photos + videos) to Gemini and asks for unified selection
// using quality-agnostic, metadata-driven criteria.
// Photos are sent as thumbnails, videos are compressed and uploaded via Files API.
// tripContext provides optional user description of the trip/event.
// modelName allows specifying which Gemini model to use.
// Returns the structured selection with ranked list, scene grouping, and exclusion report.
// See DDR-020: Mixed Media Selection Strategy.
func AskMediaSelection(ctx context.Context, client *genai.Client, files []*filehandler.MediaFile, maxItems int, tripContext string, modelName string) (string, error) {
	// Count media types for logging
	var imageCount, videoCount int
	for _, file := range files {
		ext := strings.ToLower(filepath.Ext(file.Path))
		if filehandler.IsImage(ext) {
			imageCount++
		} else if filehandler.IsVideo(ext) {
			videoCount++
		}
	}

	log.Info().
		Int("total_media", len(files)).
		Int("images", imageCount).
		Int("videos", videoCount).
		Int("max_select", maxItems).
		Bool("has_context", tripContext != "").
		Str("model", modelName).
		Msg("Starting mixed media selection with Gemini")

	// Track resources for cleanup
	var uploadedFiles []*genai.File // Gemini files to delete after
	var cleanupFuncs []func()       // Temp file cleanup functions

	// Ensure cleanup happens regardless of success/failure
	defer func() {
		// Cleanup temp compressed files
		for _, cleanup := range cleanupFuncs {
			cleanup()
		}
		// Delete uploaded Gemini files to avoid quota accumulation
		for _, f := range uploadedFiles {
			if err := client.DeleteFile(ctx, f.Name); err != nil {
				log.Warn().Err(err).Str("file", f.Name).Msg("Failed to delete uploaded Gemini file")
			} else {
				log.Debug().Str("file", f.Name).Msg("Uploaded Gemini file deleted")
			}
		}
	}()

	// Build the prompt with metadata and context
	prompt := BuildMediaSelectionPrompt(files, maxItems, tripContext)

	// Configure model with system instruction
	model := client.GenerativeModel(modelName)
	model.SystemInstruction = &genai.Content{
		Parts: []genai.Part{
			genai.Text(MediaSelectionSystemInstruction),
		},
	}

	// Build parts: reference photo first, then media, then prompt
	var parts []genai.Part

	// Add Francis reference photo as the first image (DDR-017)
	log.Debug().
		Int("reference_bytes", len(assets.FrancisReferencePhoto)).
		Msg("Including Francis reference photo for identification")
	parts = append(parts, genai.Blob{
		MIMEType: assets.FrancisReferenceMIMEType,
		Data:     assets.FrancisReferencePhoto,
	})

	// Process each media file
	log.Info().Msg("Processing media files...")

	for i, file := range files {
		ext := strings.ToLower(filepath.Ext(file.Path))

		if filehandler.IsImage(ext) {
			// Generate thumbnail for images
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
				Msg("Image thumbnail ready")

			parts = append(parts, genai.Blob{
				MIMEType: mimeType,
				Data:     thumbData,
			})

		} else if filehandler.IsVideo(ext) {
			// Compress video for Gemini (DDR-018)
			log.Info().
				Str("file", filepath.Base(file.Path)).
				Int64("size_mb", file.Size/(1024*1024)).
				Msg("Compressing video for Gemini...")

			var videoMeta *filehandler.VideoMetadata
			if file.Metadata != nil {
				videoMeta, _ = file.Metadata.(*filehandler.VideoMetadata)
			}

			compressedPath, compressedSize, cleanup, err := filehandler.CompressVideoForGemini(ctx, file.Path, videoMeta)
			if err != nil {
				log.Warn().Err(err).Str("file", file.Path).Msg("Failed to compress video, skipping")
				continue
			}
			cleanupFuncs = append(cleanupFuncs, cleanup)

			log.Info().
				Str("file", filepath.Base(file.Path)).
				Int64("original_mb", file.Size/(1024*1024)).
				Int64("compressed_mb", compressedSize/(1024*1024)).
				Msg("Video compressed")

			// Upload to Files API
			log.Info().
				Str("file", filepath.Base(file.Path)).
				Msg("Uploading compressed video to Gemini...")

			uploadedFile, err := uploadVideoFile(ctx, client, compressedPath)
			if err != nil {
				log.Warn().Err(err).Str("file", file.Path).Msg("Failed to upload video, skipping")
				continue
			}
			uploadedFiles = append(uploadedFiles, uploadedFile)

			log.Debug().
				Int("index", i+1).
				Str("file", filepath.Base(file.Path)).
				Str("uri", uploadedFile.URI).
				Msg("Video uploaded")

			// Add file reference to parts
			parts = append(parts, genai.FileData{
				MIMEType: uploadedFile.MIMEType,
				URI:      uploadedFile.URI,
			})
		}
	}

	// Add the text prompt at the end
	parts = append(parts, genai.Text(prompt))

	log.Info().
		Int("num_images", imageCount).
		Int("num_videos", len(uploadedFiles)).
		Msg("Sending media to Gemini for unified selection...")

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
		Msg("Media selection complete")

	return response, nil
}

// uploadVideoFile uploads a video file to Gemini Files API and waits for processing.
func uploadVideoFile(ctx context.Context, client *genai.Client, filePath string) (*genai.File, error) {
	// Open the file for streaming upload
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	// Get file info for logging
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	log.Debug().
		Str("path", filePath).
		Int64("size_bytes", info.Size()).
		Msg("Uploading video to Files API")

	// Upload the file
	uploadStart := time.Now()
	file, err := client.UploadFile(ctx, "", f, &genai.UploadFileOptions{
		MIMEType: "video/webm", // Compressed output is always WebM
	})
	if err != nil {
		return nil, fmt.Errorf("failed to upload file: %w", err)
	}

	log.Debug().
		Str("name", file.Name).
		Str("uri", file.URI).
		Dur("upload_duration", time.Since(uploadStart)).
		Msg("Video uploaded, waiting for processing...")

	// Wait for file to be processed
	const uploadPollingInterval = 5 * time.Second
	const uploadTimeout = 10 * time.Minute
	deadline := time.Now().Add(uploadTimeout)

	for file.State == genai.FileStateProcessing {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout waiting for video processing after %v", uploadTimeout)
		}

		log.Debug().
			Str("state", string(file.State)).
			Msg("Video still processing, waiting...")

		time.Sleep(uploadPollingInterval)

		// Get updated file state
		file, err = client.GetFile(ctx, file.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to get file state: %w", err)
		}
	}

	if file.State == genai.FileStateFailed {
		return nil, fmt.Errorf("video processing failed")
	}

	log.Info().
		Str("name", file.Name).
		Str("state", string(file.State)).
		Dur("total_time", time.Since(uploadStart)).
		Msg("Video ready for inference")

	return file, nil
}
