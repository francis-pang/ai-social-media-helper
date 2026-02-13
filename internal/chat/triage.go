package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"time"

	"github.com/fpang/gemini-media-cli/internal/assets"
	"github.com/fpang/gemini-media-cli/internal/filehandler"
	"github.com/fpang/gemini-media-cli/internal/jsonutil"
	"github.com/fpang/gemini-media-cli/internal/metrics"
	"github.com/rs/zerolog/log"
	"google.golang.org/genai"
)

// TriageResult represents the AI's verdict for a single media item.
type TriageResult struct {
	Media    int    `json:"media"`
	Filename string `json:"filename"`
	Saveable bool   `json:"saveable"`
	Reason   string `json:"reason"`
}

// BuildMediaTriagePrompt creates a prompt asking Gemini to evaluate each media item
// for saveability. Media metadata is included so Gemini can reference items by number.
func BuildMediaTriagePrompt(files []*filehandler.MediaFile) string {
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

	sb.WriteString("## Media Triage Task\n\n")
	sb.WriteString(fmt.Sprintf("You are evaluating %d media items (%d photos, %d videos) to determine which are worth keeping.\n\n",
		len(files), imageCount, videoCount))

	sb.WriteString("### Evaluation Criteria\n\n")
	sb.WriteString("For each item, decide: is this media SAVEABLE or UNSAVEABLE?\n")
	sb.WriteString("- SAVEABLE: A normal person would find it meaningful, and light editing could make it decent\n")
	sb.WriteString("- UNSAVEABLE: Too flawed for any reasonable light editing to produce a decent result\n\n")
	sb.WriteString("Be generous — if there is any recognizable subject and light editing could help, mark as saveable.\n\n")

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
			}
		} else {
			sb.WriteString("- No metadata available\n")
		}
		sb.WriteString("\n")
	}

	sb.WriteString("### Required Output\n\n")
	sb.WriteString("Respond with ONLY a valid JSON array. One entry per media item, in order.\n")
	sb.WriteString("Each entry: {\"media\": N, \"filename\": \"name\", \"saveable\": true/false, \"reason\": \"brief explanation\"}\n")

	return sb.String()
}

// triageBatchSize is the maximum number of media items to send in a single
// Gemini API call. Large batches cause the model to silently drop items from
// its response — batching into smaller groups ensures every item gets a verdict.
const triageBatchSize = 20

// AskMediaTriage sends media files to Gemini for triage evaluation.
// When len(files) > triageBatchSize the work is split into smaller batches
// so the model reliably covers every item; results are merged and Media
// indices adjusted to the caller's original file positions.
//
// Photos are sent as thumbnails (inline blobs), videos as compressed file references.
// sessionID is used for storing compressed videos in S3 (optional).
// storeCompressed is an optional callback to store compressed videos in S3.
// keyMapper maps local file paths to S3 keys (optional, for cloud mode).
// Returns a slice of TriageResult with one verdict per media item.
// See DDR-021: Media Triage Command with Batch AI Evaluation.
func AskMediaTriage(ctx context.Context, client *genai.Client, files []*filehandler.MediaFile, modelName string, sessionID string, storeCompressed CompressedVideoStore, keyMapper KeyMapper) ([]TriageResult, error) {
	if len(files) <= triageBatchSize {
		return askMediaTriageSingle(ctx, client, files, modelName, sessionID, storeCompressed, keyMapper)
	}

	totalBatches := (len(files) + triageBatchSize - 1) / triageBatchSize
	log.Info().
		Int("total_files", len(files)).
		Int("batch_size", triageBatchSize).
		Int("total_batches", totalBatches).
		Msg("Batching media triage — too many files for a single request")

	var allResults []TriageResult

	for batchStart := 0; batchStart < len(files); batchStart += triageBatchSize {
		batchEnd := batchStart + triageBatchSize
		if batchEnd > len(files) {
			batchEnd = len(files)
		}
		batch := files[batchStart:batchEnd]
		batchNum := (batchStart / triageBatchSize) + 1

		log.Info().
			Int("batch", batchNum).
			Int("total_batches", totalBatches).
			Int("batch_size", len(batch)).
			Int("offset", batchStart).
			Msg("Processing triage batch")

		batchResults, err := askMediaTriageSingle(ctx, client, batch, modelName, sessionID, storeCompressed, keyMapper)
		if err != nil {
			log.Error().Err(err).Int("batch", batchNum).Msg("Batch triage failed")
			return nil, fmt.Errorf("batch %d/%d triage failed: %w", batchNum, totalBatches, err)
		}

		// Adjust Media indices from batch-local (1-based) to global (1-based).
		for i := range batchResults {
			batchResults[i].Media += batchStart
		}
		allResults = append(allResults, batchResults...)

		log.Info().
			Int("batch", batchNum).
			Int("batch_results", len(batchResults)).
			Int("total_so_far", len(allResults)).
			Msg("Batch triage complete")
	}

	log.Info().
		Int("total_results", len(allResults)).
		Int("total_files", len(files)).
		Msg("All triage batches complete")

	return allResults, nil
}

// askMediaTriageSingle sends a single batch of media files to Gemini for
// triage evaluation. Callers should prefer AskMediaTriage which handles
// batching automatically.
func askMediaTriageSingle(ctx context.Context, client *genai.Client, files []*filehandler.MediaFile, modelName string, sessionID string, storeCompressed CompressedVideoStore, keyMapper KeyMapper) ([]TriageResult, error) {
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
		Str("model", modelName).
		Msg("Starting batch media triage with Gemini")

	// Track resources for cleanup
	var uploadedFiles []*genai.File // Gemini files to delete after
	var cleanupFuncs []func()       // Temp file cleanup functions

	// Ensure cleanup happens regardless of success/failure
	defer func() {
		for _, cleanup := range cleanupFuncs {
			cleanup()
		}
		for _, f := range uploadedFiles {
			if _, err := client.Files.Delete(ctx, f.Name, nil); err != nil {
				log.Warn().Err(err).Str("file", f.Name).Msg("Failed to delete uploaded Gemini file")
			} else {
				log.Debug().Str("file", f.Name).Msg("Uploaded Gemini file deleted")
			}
		}
	}()

	// Build the prompt with metadata
	prompt := BuildMediaTriagePrompt(files)

	// Configure model with triage system instruction
	// MaxOutputTokens must be set high enough for large batches — each media item
	// produces ~80-100 tokens of JSON output, and the default limit can truncate responses.
	config := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{{Text: assets.TriageSystemPrompt}},
		},
		MaxOutputTokens: 65536,
	}

	// Build parts: media files then prompt (no reference photo for triage)
	var parts []*genai.Part

	// Process each media file
	log.Info().Msg("Processing media files for triage...")

	for i, file := range files {
		ext := strings.ToLower(filepath.Ext(file.Path))

		if filehandler.IsImage(ext) {
			// Generate thumbnail for images
			log.Debug().
				Int("index", i+1).
				Str("file", filepath.Base(file.Path)).
				Msg("Processing image file for triage")
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
				Msg("Image thumbnail ready for triage")

			parts = append(parts, &genai.Part{
				InlineData: &genai.Blob{
					MIMEType: mimeType,
					Data:     thumbData,
				},
			})

		} else if filehandler.IsVideo(ext) {
			// Compress video for Gemini
			log.Info().
				Str("file", filepath.Base(file.Path)).
				Int64("size_mb", file.Size/(1024*1024)).
				Msg("Compressing video for triage...")

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
				Msg("Video compressed for triage")

			// Store compressed video in S3 if callback provided
			originalKey := file.Path // Default to local path
			if keyMapper != nil {
				if s3Key := keyMapper(file.Path); s3Key != "" {
					originalKey = s3Key // Use S3 key if available
				}
			}
			if storeCompressed != nil && sessionID != "" {
				compressedKey, err := storeCompressed(ctx, sessionID, originalKey, compressedPath)
				if err != nil {
					log.Warn().Err(err).Str("file", file.Path).Msg("Failed to store compressed video in S3, continuing without storage")
				} else {
					log.Info().
						Str("file", filepath.Base(file.Path)).
						Str("compressed_key", compressedKey).
						Msg("Compressed video stored in S3")
				}
			}

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
				Msg("Video uploaded for triage")

			parts = append(parts, &genai.Part{
				FileData: &genai.FileData{
					MIMEType: uploadedFile.MIMEType,
					FileURI:  uploadedFile.URI,
				},
			})
		}
	}

	// Add the text prompt at the end
	parts = append(parts, &genai.Part{Text: prompt})

	log.Info().
		Int("num_images", imageCount).
		Int("num_videos", len(uploadedFiles)).
		Msg("Sending media to Gemini for batch triage...")

	// Generate content
	contents := []*genai.Content{{Role: "user", Parts: parts}}
	geminiStart := time.Now()
	log.Debug().
		Str("model", modelName).
		Int("prompt_length", len(prompt)).
		Int("media_part_count", len(parts)-1). // -1 for prompt
		Msg("Starting Gemini API call for media triage")
	resp, err := client.Models.GenerateContent(ctx, modelName, contents, config)
	geminiElapsed := time.Since(geminiStart)

	// Emit Gemini API metrics
	m := metrics.New("AiSocialMedia").
		Dimension("Operation", "triage").
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
		log.Error().Err(err).Dur("duration", geminiElapsed).Msg("Failed to generate triage from Gemini")
		return nil, fmt.Errorf("failed to generate content: %w", err)
	}

	if resp == nil || resp.Text() == "" {
		log.Warn().Dur("duration", geminiElapsed).Msg("Received empty response from Gemini")
		return nil, fmt.Errorf("received empty response from Gemini API")
	}

	log.Debug().
		Int("response_length", len(resp.Text())).
		Dur("duration", geminiElapsed).
		Msg("Gemini API response received for media triage")

	// Extract text from response
	responseText := resp.Text()

	// Parse JSON response
	results, err := parseTriageResponse(responseText)
	if err != nil {
		return nil, fmt.Errorf("failed to parse triage response: %w", err)
	}

	log.Info().
		Int("total_results", len(results)).
		Msg("Media triage complete")

	return results, nil
}

// parseTriageResponse extracts and parses the JSON array from Gemini's response.
func parseTriageResponse(response string) ([]TriageResult, error) {
	log.Debug().
		Int("response_length", len(response)).
		Msg("Parsing triage response JSON")
	results, err := jsonutil.ParseJSON[[]TriageResult](response)
	if err != nil {
		log.Error().Err(err).Str("response", response).Msg("Failed to parse triage response")
		return nil, fmt.Errorf("triage response: %w", err)
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("empty results array in triage response")
	}
	log.Debug().
		Int("item_count", len(results)).
		Msg("Triage response parsed successfully")
	return results, nil
}

// WriteTriageReport writes the triage results as a JSON file alongside the media directory.
func WriteTriageReport(results []TriageResult, outputPath string) error {
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal triage results: %w", err)
	}

	if err := os.WriteFile(outputPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write triage report: %w", err)
	}

	log.Info().Str("path", outputPath).Msg("Triage report written")
	return nil
}
