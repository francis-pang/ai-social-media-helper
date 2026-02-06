package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpang/gemini-media-cli/internal/assets"
	"github.com/fpang/gemini-media-cli/internal/filehandler"
	"github.com/google/generative-ai-go/genai"
	"github.com/rs/zerolog/log"
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

// AskMediaTriage sends a batch of media files to Gemini for triage evaluation.
// Photos are sent as thumbnails (inline blobs), videos as compressed file references.
// Returns a slice of TriageResult with one verdict per media item.
// See DDR-021: Media Triage Command with Batch AI Evaluation.
func AskMediaTriage(ctx context.Context, client *genai.Client, files []*filehandler.MediaFile, modelName string) ([]TriageResult, error) {
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
			if err := client.DeleteFile(ctx, f.Name); err != nil {
				log.Warn().Err(err).Str("file", f.Name).Msg("Failed to delete uploaded Gemini file")
			} else {
				log.Debug().Str("file", f.Name).Msg("Uploaded Gemini file deleted")
			}
		}
	}()

	// Build the prompt with metadata
	prompt := BuildMediaTriagePrompt(files)

	// Configure model with triage system instruction
	model := client.GenerativeModel(modelName)
	model.SystemInstruction = &genai.Content{
		Parts: []genai.Part{
			genai.Text(assets.TriageSystemPrompt),
		},
	}

	// Build parts: media files then prompt (no reference photo for triage)
	var parts []genai.Part

	// Process each media file
	log.Info().Msg("Processing media files for triage...")

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
				Msg("Image thumbnail ready for triage")

			parts = append(parts, genai.Blob{
				MIMEType: mimeType,
				Data:     thumbData,
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
		Msg("Sending media to Gemini for batch triage...")

	// Generate content
	resp, err := model.GenerateContent(ctx, parts...)
	if err != nil {
		log.Error().Err(err).Msg("Failed to generate triage from Gemini")
		return nil, fmt.Errorf("failed to generate content: %w", err)
	}

	if resp == nil || len(resp.Candidates) == 0 {
		log.Warn().Msg("Received empty response from Gemini")
		return nil, fmt.Errorf("received empty response from Gemini API")
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

	responseText := result.String()
	log.Debug().
		Int("response_length", len(responseText)).
		Msg("Received triage response from Gemini")

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
// It handles cases where Gemini wraps the JSON in markdown code fences.
func parseTriageResponse(response string) ([]TriageResult, error) {
	// Trim whitespace
	text := strings.TrimSpace(response)

	// Strip markdown code fences if present (```json ... ``` or ``` ... ```)
	if strings.HasPrefix(text, "```") {
		lines := strings.Split(text, "\n")
		// Remove first line (```json or ```) and last line (```)
		if len(lines) >= 3 {
			startIdx := 1
			endIdx := len(lines) - 1
			// Find the closing ```
			for i := len(lines) - 1; i >= 0; i-- {
				if strings.TrimSpace(lines[i]) == "```" {
					endIdx = i
					break
				}
			}
			text = strings.Join(lines[startIdx:endIdx], "\n")
		}
	}

	text = strings.TrimSpace(text)

	// Try to find JSON array in the text if it's embedded in other text
	if !strings.HasPrefix(text, "[") {
		startIdx := strings.Index(text, "[")
		if startIdx == -1 {
			// Log the raw response for debugging
			log.Error().Str("response", response).Msg("No JSON array found in triage response")
			return nil, fmt.Errorf("no JSON array found in response")
		}
		text = text[startIdx:]
	}

	// Find the matching closing bracket
	if endIdx := strings.LastIndex(text, "]"); endIdx != -1 {
		text = text[:endIdx+1]
	}

	var results []TriageResult
	if err := json.Unmarshal([]byte(text), &results); err != nil {
		log.Error().
			Err(err).
			Str("json_text", text[:min(len(text), 500)]).
			Msg("Failed to parse triage JSON")
		return nil, fmt.Errorf("invalid JSON in triage response: %w", err)
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("empty results array in triage response")
	}

	return results, nil
}

// min returns the smaller of two integers.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
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
