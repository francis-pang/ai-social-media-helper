package chat

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/fpang/gemini-media-cli/internal/assets"
	"github.com/fpang/gemini-media-cli/internal/filehandler"
	"github.com/fpang/gemini-media-cli/internal/metrics"
	"github.com/rs/zerolog/log"
	"google.golang.org/genai"
)

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
	config := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{{Text: SelectionSystemInstruction}},
		},
	}

	// Build parts: reference photo first, then thumbnails, then prompt
	var parts []*genai.Part

	// Add Francis reference photo as the first image (DDR-017)
	log.Debug().
		Int("reference_bytes", len(assets.FrancisReferencePhoto)).
		Msg("Including Francis reference photo for identification")
	parts = append(parts, &genai.Part{
		InlineData: &genai.Blob{
			MIMEType: assets.FrancisReferenceMIMEType,
			Data:     assets.FrancisReferencePhoto,
		},
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

		parts = append(parts, &genai.Part{
			InlineData: &genai.Blob{
				MIMEType: mimeType,
				Data:     thumbData,
			},
		})
	}

	// Add the text prompt at the end
	parts = append(parts, &genai.Part{Text: prompt})

	log.Info().
		Int("num_thumbnails", len(parts)-2). // -2 for reference photo and prompt
		Msg("Sending thumbnails to Gemini for quality-agnostic selection...")

	// Generate content
	contents := []*genai.Content{{Role: "user", Parts: parts}}
	geminiStart := time.Now()
	resp, err := client.Models.GenerateContent(ctx, GetModelName(), contents, config)
	geminiElapsed := time.Since(geminiStart)

	// Emit Gemini API metrics
	m := metrics.New("AiSocialMedia").
		Dimension("Operation", "photoSelection").
		Metric("GeminiApiLatencyMs", float64(geminiElapsed.Milliseconds()), metrics.UnitMilliseconds).
		Count("GeminiApiCalls")
	if err != nil {
		m.Count("GeminiApiErrors")
	}
	if resp != nil && resp.UsageMetadata != nil {
		m.Metric("GeminiInputTokens", float64(resp.UsageMetadata.PromptTokenCount), metrics.UnitCount)
		m.Metric("GeminiOutputTokens", float64(resp.UsageMetadata.CandidatesTokenCount), metrics.UnitCount)
	}
	m.Flush()

	if err != nil {
		log.Error().Err(err).Msg("Failed to generate selection from Gemini")
		return "", fmt.Errorf("failed to generate content: %w", err)
	}

	if resp == nil {
		log.Warn().Msg("Received empty response from Gemini")
		return "", fmt.Errorf("received empty response from Gemini API")
	}

	// Extract text from response
	response := resp.Text()
	log.Info().
		Int("response_length", len(response)).
		Msg("Photo selection complete")

	return response, nil
}
