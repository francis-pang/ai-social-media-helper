package chat

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpang/gemini-media-cli/internal/assets"
	"github.com/fpang/gemini-media-cli/internal/filehandler"
	"github.com/fpang/gemini-media-cli/internal/metrics"
	"github.com/rs/zerolog/log"
	"google.golang.org/genai"
)

// MediaSelectionSystemInstruction provides context for mixed media (photo + video) selection.
// Loaded from embedded prompt file. See DDR-019, DDR-020.
var MediaSelectionSystemInstruction = assets.MediaSelectionSystemPrompt

// MediaSelectionJSONInstruction provides context for structured JSON media selection.
// Loaded from embedded prompt file. See DDR-019, DDR-030.
var MediaSelectionJSONInstruction = assets.MediaSelectionJSONSystemPrompt

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

	// Build media parts (thumbnails + uploaded videos)
	parts, cleanup, uploadedFiles, err := buildMediaParts(ctx, client, files)
	defer cleanup()
	if err != nil {
		return "", err
	}

	// Build the prompt with metadata and context
	prompt := BuildMediaSelectionPrompt(files, maxItems, tripContext)

	// Configure model with system instruction
	config := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{{Text: MediaSelectionSystemInstruction}},
		},
	}

	// Add the text prompt at the end
	parts = append(parts, &genai.Part{Text: prompt})

	log.Info().
		Int("num_images", imageCount).
		Int("num_videos", len(uploadedFiles)).
		Msg("Sending media to Gemini for unified selection...")

	// Generate content
	contents := []*genai.Content{{Role: "user", Parts: parts}}
	geminiStart := time.Now()
	log.Debug().
		Str("model", modelName).
		Int("part_count", len(parts)).
		Msg("Starting Gemini API call for media selection")
	resp, err := client.Models.GenerateContent(ctx, modelName, contents, config)
	geminiElapsed := time.Since(geminiStart)

	// Emit Gemini API metrics
	m := metrics.New("AiSocialMedia").
		Dimension("Operation", "mediaSelection").
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
		log.Error().Err(err).Dur("duration", geminiElapsed).Msg("Failed to generate selection from Gemini")
		return "", fmt.Errorf("failed to generate content: %w", err)
	}

	if resp == nil {
		log.Warn().Dur("duration", geminiElapsed).Msg("Received empty response from Gemini")
		return "", fmt.Errorf("received empty response from Gemini API")
	}

	log.Debug().
		Int("response_length", len(resp.Text())).
		Dur("duration", geminiElapsed).
		Msg("Gemini API response received for media selection")

	// Extract text from response
	response := resp.Text()
	log.Info().
		Int("response_length", len(response)).
		Msg("Media selection complete")

	return response, nil
}

// AskMediaSelectionJSON sends mixed media to Gemini and returns structured selection results.
// Unlike AskMediaSelection which returns freeform text, this returns a parsed SelectionResult.
// No item limit — the AI selects all worthy items. See DDR-030.
func AskMediaSelectionJSON(ctx context.Context, client *genai.Client, files []*filehandler.MediaFile, tripContext string, modelName string) (*SelectionResult, error) {
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
		Bool("has_context", tripContext != "").
		Str("model", modelName).
		Msg("Starting structured JSON media selection with Gemini (DDR-030)")

	// Build media parts (thumbnails + uploaded videos)
	parts, cleanup, uploadedFiles, err := buildMediaParts(ctx, client, files)
	defer cleanup()
	if err != nil {
		return nil, err
	}

	// Build the prompt
	prompt := BuildMediaSelectionJSONPrompt(files, tripContext)

	// Configure model with JSON system instruction
	config := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{{Text: MediaSelectionJSONInstruction}},
		},
	}

	// Add the text prompt at the end
	parts = append(parts, &genai.Part{Text: prompt})

	log.Info().
		Int("num_images", imageCount).
		Int("num_videos", len(uploadedFiles)).
		Msg("Sending media to Gemini for JSON selection...")

	// Generate content
	contents := []*genai.Content{{Role: "user", Parts: parts}}
	geminiStart := time.Now()
	log.Debug().
		Str("model", modelName).
		Int("part_count", len(parts)).
		Msg("Starting Gemini API call for JSON media selection")
	resp, err := client.Models.GenerateContent(ctx, modelName, contents, config)
	geminiElapsed := time.Since(geminiStart)

	// Emit Gemini API metrics
	m := metrics.New("AiSocialMedia").
		Dimension("Operation", "jsonSelection").
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
		log.Error().Err(err).Dur("duration", geminiElapsed).Msg("Failed to generate JSON selection from Gemini")
		return nil, fmt.Errorf("failed to generate content: %w", err)
	}

	if resp == nil {
		log.Warn().Dur("duration", geminiElapsed).Msg("Received empty response from Gemini")
		return nil, fmt.Errorf("received empty response from Gemini API")
	}

	// Extract text from response
	responseText := resp.Text()
	log.Debug().
		Int("response_length", len(responseText)).
		Dur("duration", geminiElapsed).
		Msg("Gemini API response received for JSON media selection")

	// Parse JSON response
	selectionResult, err := parseSelectionResponse(responseText)
	if err != nil {
		return nil, fmt.Errorf("failed to parse selection response: %w", err)
	}

	log.Info().
		Int("selected", len(selectionResult.Selected)).
		Int("excluded", len(selectionResult.Excluded)).
		Int("scenes", len(selectionResult.SceneGroups)).
		Msg("JSON media selection complete")

	return selectionResult, nil
}

// buildMediaParts processes mixed media files into Gemini API parts.
// Images are converted to thumbnails (inline data), videos are compressed and uploaded via Files API.
// Returns the parts list, a cleanup function, the list of uploaded files, and any error.
func buildMediaParts(ctx context.Context, client *genai.Client, files []*filehandler.MediaFile) ([]*genai.Part, func(), []*genai.File, error) {
	var uploadedFiles []*genai.File
	var cleanupFuncs []func()

	cleanupAll := func() {
		for _, fn := range cleanupFuncs {
			fn()
		}
		for _, f := range uploadedFiles {
			if _, err := client.Files.Delete(ctx, f.Name, nil); err != nil {
				log.Warn().Err(err).Str("file", f.Name).Msg("Failed to delete uploaded Gemini file")
			} else {
				log.Debug().Str("file", f.Name).Msg("Uploaded Gemini file deleted")
			}
		}
	}

	// Start with Francis reference photo (DDR-017)
	var parts []*genai.Part
	log.Debug().
		Int("reference_bytes", len(assets.FrancisReferencePhoto)).
		Msg("Including Francis reference photo for identification")
	parts = append(parts, &genai.Part{
		InlineData: &genai.Blob{
			MIMEType: assets.FrancisReferenceMIMEType,
			Data:     assets.FrancisReferencePhoto,
		},
	})

	log.Info().Msg("Processing media files...")

	for i, file := range files {
		ext := strings.ToLower(filepath.Ext(file.Path))

		if filehandler.IsImage(ext) {
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

			parts = append(parts, &genai.Part{
				InlineData: &genai.Blob{
					MIMEType: mimeType,
					Data:     thumbData,
				},
			})

		} else if filehandler.IsVideo(ext) {
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

			uploadedFile, err := uploadVideoFile(ctx, client, compressedPath)
			if err != nil {
				log.Warn().Err(err).Str("file", file.Path).Msg("Failed to upload video, skipping")
				continue
			}
			uploadedFiles = append(uploadedFiles, uploadedFile)

			parts = append(parts, &genai.Part{
				FileData: &genai.FileData{
					MIMEType: uploadedFile.MIMEType,
					FileURI:  uploadedFile.URI,
				},
			})
		}
	}

	return parts, cleanupAll, uploadedFiles, nil
}
