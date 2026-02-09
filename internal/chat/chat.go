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
	"github.com/rs/zerolog/log"
	"google.golang.org/genai"
)

// SystemInstruction provides context for media analysis with extracted metadata.
// Loaded from embedded prompt file. See DDR-019: Externalized Prompt Templates.
// See DDR-017: Francis Reference Photo for Person Identification.
var SystemInstruction = assets.SystemInstructionPrompt

// UploadPollingInterval is the interval between checking upload state.
const UploadPollingInterval = 5 * time.Second

// UploadTimeout is the maximum time to wait for upload processing.
const UploadTimeout = 10 * time.Minute

// NewGeminiClient creates a new Gemini API client using the provided API key.
// Uses the new google.golang.org/genai SDK (SDK-A migration).
func NewGeminiClient(ctx context.Context, apiKey string) (*genai.Client, error) {
	log.Debug().
		Bool("api_key_present", apiKey != "").
		Msg("Creating Gemini API client")
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}
	return client, nil
}

// AskTextQuestion sends a text-only question to the Gemini API and returns the response.
func AskTextQuestion(ctx context.Context, client *genai.Client, question string) (string, error) {
	modelName := GetModelName()
	callStart := time.Now()
	log.Debug().
		Str("model", modelName).
		Int("prompt_length", len(question)).
		Msg("Starting Gemini API call for text question")

	resp, err := client.Models.GenerateContent(ctx, modelName, genai.Text(question), nil)
	duration := time.Since(callStart)
	if err != nil {
		log.Error().Err(err).Dur("duration", duration).Msg("Failed to generate content")
		return "", fmt.Errorf("failed to generate content: %w", err)
	}

	response := resp.Text()
	log.Debug().
		Int("response_length", len(response)).
		Dur("duration", duration).
		Msg("Gemini API response received for text question")

	return response, nil
}

// BuildDailyNewsQuestion constructs a question about major news that includes
// the current date to ensure different responses on different days.
func BuildDailyNewsQuestion() string {
	now := time.Now()
	dateStr := now.Format("Monday, January 2, 2006")

	return fmt.Sprintf(
		"Today is %s. What are the major news events that happened in the past 24 hours? "+
			"Please provide a brief summary of the top 3-5 most significant news stories.",
		dateStr,
	)
}

// AskMediaQuestion sends a media file (image or video) along with a question to the Gemini API.
// Always uses the Files API for consistent behavior and memory efficiency (DDR-012).
// For videos, always compresses before upload using AV1+Opus for optimal efficiency (DDR-018).
func AskMediaQuestion(ctx context.Context, client *genai.Client, mediaFile *filehandler.MediaFile, question string) (string, error) {
	mediaType := "media"
	if mediaFile.Metadata != nil {
		mediaType = mediaFile.Metadata.GetMediaType()
	}

	log.Debug().
		Str("path", mediaFile.Path).
		Str("mime_type", mediaFile.MIMEType).
		Int64("size_bytes", mediaFile.Size).
		Str("media_type", mediaType).
		Msg("Sending media question to Gemini via Files API")

	// For videos, compress before upload (DDR-018: always-on compression)
	uploadPath := mediaFile.Path
	uploadMIME := mediaFile.MIMEType
	var cleanupCompressed func()

	ext := strings.ToLower(filepath.Ext(mediaFile.Path))
	if filehandler.IsVideo(ext) {
		log.Info().Msg("Compressing video for Gemini optimization (AV1+Opus)...")

		// Get video metadata for smart compression (no-upscaling logic)
		var videoMeta *filehandler.VideoMetadata
		if mediaFile.Metadata != nil {
			videoMeta, _ = mediaFile.Metadata.(*filehandler.VideoMetadata)
		}

		compressedPath, compressedSize, cleanup, err := filehandler.CompressVideoForGemini(ctx, mediaFile.Path, videoMeta)
		if err != nil {
			return "", fmt.Errorf("failed to compress video: %w", err)
		}
		cleanupCompressed = cleanup
		uploadPath = compressedPath
		uploadMIME = "video/webm" // Compressed output is WebM

		log.Info().
			Int64("original_mb", mediaFile.Size/(1024*1024)).
			Int64("compressed_mb", compressedSize/(1024*1024)).
			Msg("Video compression complete")
	}

	// Ensure compressed file cleanup happens
	if cleanupCompressed != nil {
		defer cleanupCompressed()
	}

	// Create a temporary MediaFile for upload with the compressed path
	uploadFile := &filehandler.MediaFile{
		Path:     uploadPath,
		MIMEType: uploadMIME,
		Size:     mediaFile.Size, // Original size for logging (actual upload size is different)
		Metadata: mediaFile.Metadata, // Keep original metadata for prompt building!
	}

	// Update size if we compressed
	if uploadPath != mediaFile.Path {
		if info, err := os.Stat(uploadPath); err == nil {
			uploadFile.Size = info.Size()
		}
	}

	// Always use Files API for all media uploads (DDR-012)
	file, err := uploadAndWaitForProcessing(ctx, client, uploadFile)
	if err != nil {
		return "", fmt.Errorf("failed to upload file: %w", err)
	}
	defer func() {
		// Clean up uploaded file after inference to manage quota
		if _, err := client.Files.Delete(ctx, file.Name, nil); err != nil {
			log.Warn().Err(err).Str("file", file.Name).Msg("Failed to delete uploaded file")
		} else {
			log.Debug().Str("file", file.Name).Msg("Uploaded file deleted from Gemini storage")
		}
	}()

	// Build parts: reference photo + file reference + question (DDR-017)
	log.Debug().
		Int("reference_bytes", len(assets.FrancisReferencePhoto)).
		Msg("Including Francis reference photo for identification")
	parts := []*genai.Part{
		// Francis reference photo first for identification
		{InlineData: &genai.Blob{
			MIMEType: assets.FrancisReferenceMIMEType,
			Data:     assets.FrancisReferencePhoto,
		}},
		// Target media to analyze
		{FileData: &genai.FileData{
			MIMEType: file.MIMEType,
			FileURI:  file.URI,
		}},
		{Text: question},
	}

	// Configure model with system instruction for metadata context
	config := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{{Text: SystemInstruction}},
		},
	}

	// Generate content
	modelName := GetModelName()
	callStart := time.Now()
	log.Debug().
		Str("model", modelName).
		Int("prompt_length", len(question)).
		Int("media_part_count", 1).
		Msg("Starting Gemini API call for media question")
	contents := []*genai.Content{{Role: "user", Parts: parts}}
	resp, err := client.Models.GenerateContent(ctx, modelName, contents, config)
	duration := time.Since(callStart)
	if err != nil {
		log.Error().Err(err).Dur("duration", duration).Msg("Failed to generate content from media")
		return "", fmt.Errorf("failed to generate content: %w", err)
	}

	response := resp.Text()
	log.Debug().
		Int("response_length", len(response)).
		Dur("duration", duration).
		Msg("Gemini API response received for media question")

	return response, nil
}

// uploadAndWaitForProcessing uploads a file using the Files API and waits for it to be processed.
func uploadAndWaitForProcessing(ctx context.Context, client *genai.Client, mediaFile *filehandler.MediaFile) (*genai.File, error) {
	log.Debug().
		Str("path", mediaFile.Path).
		Int64("size_bytes", mediaFile.Size).
		Str("mime_type", mediaFile.MIMEType).
		Msg("Starting Gemini Files API upload")

	// Open the file for streaming upload
	f, err := os.Open(mediaFile.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	// Upload the file
	uploadStart := time.Now()
	file, err := client.Files.Upload(ctx, f, &genai.UploadFileConfig{
		MIMEType: mediaFile.MIMEType,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to upload file: %w", err)
	}

	log.Info().
		Str("name", file.Name).
		Str("uri", file.URI).
		Dur("upload_duration", time.Since(uploadStart)).
		Msg("File uploaded, waiting for processing...")

	// Wait for file to be processed
	deadline := time.Now().Add(UploadTimeout)
	pollIteration := 0
	for file.State == genai.FileStateProcessing {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout waiting for file processing after %v", UploadTimeout)
		}

		pollIteration++
		log.Debug().
			Str("state", string(file.State)).
			Int("poll_iteration", pollIteration).
			Msg("File still processing, waiting...")

		time.Sleep(UploadPollingInterval)

		// Get updated file state
		file, err = client.Files.Get(ctx, file.Name, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to get file state: %w", err)
		}
	}

	if file.State == genai.FileStateFailed {
		return nil, fmt.Errorf("file processing failed")
	}

	log.Info().
		Str("name", file.Name).
		Str("state", string(file.State)).
		Dur("total_time", time.Since(uploadStart)).
		Int("poll_iterations", pollIteration).
		Msg("File ready for inference")

	return file, nil
}

// AskImageQuestion sends an image along with a question to the Gemini API and returns the response.
// Deprecated: Use AskMediaQuestion instead for unified image/video handling.
func AskImageQuestion(ctx context.Context, client *genai.Client, mediaFile *filehandler.MediaFile, question string) (string, error) {
	return AskMediaQuestion(ctx, client, mediaFile, question)
}

// BuildSocialMediaPrompt creates a comprehensive prompt for analyzing media (image or video)
// and generating a social media post description. It automatically detects the media type
// and uses the appropriate embedded prompt template.
// See DDR-019: Externalized Prompt Templates.
func BuildSocialMediaPrompt(metadata filehandler.MediaMetadata) string {
	if metadata == nil {
		return assets.RenderSocialMediaGenericPrompt("")
	}

	metadataContext := metadata.FormatMetadataContext()

	switch metadata.GetMediaType() {
	case "video":
		return assets.RenderSocialMediaVideoPrompt(metadataContext)
	case "image":
		return assets.RenderSocialMediaImagePrompt(metadataContext)
	default:
		return assets.RenderSocialMediaGenericPrompt(metadataContext)
	}
}

// BuildSocialMediaImagePrompt creates a comprehensive prompt for analyzing an image
// and generating a social media post description.
// Prompt content loaded from embedded template. See DDR-019: Externalized Prompt Templates.
//
// Future expansion: Pre-resolve GPS coordinates using Google Maps Geocoding API
// before sending to Gemini. This would provide the resolved address, place name,
// and other location details as part of the prompt context, reducing reliance
// on Gemini's native Google Maps integration for reverse geocoding.
func BuildSocialMediaImagePrompt(metadataContext string) string {
	return assets.RenderSocialMediaImagePrompt(metadataContext)
}

// BuildSocialMediaVideoPrompt creates a comprehensive prompt for analyzing a video
// and generating a social media post description.
// Prompt content loaded from embedded template. See DDR-019: Externalized Prompt Templates.
func BuildSocialMediaVideoPrompt(metadataContext string) string {
	return assets.RenderSocialMediaVideoPrompt(metadataContext)
}

// extractTextFromResponse extracts all text from a GenerateContentResponse.
// This is a helper for cases where resp.Text() is not sufficient (e.g., when
// we need to handle partial responses or multiple candidates).
func extractTextFromResponse(resp *genai.GenerateContentResponse) string {
	if resp == nil || len(resp.Candidates) == 0 {
		return ""
	}

	var result strings.Builder
	for _, candidate := range resp.Candidates {
		if candidate.Content != nil {
			for _, part := range candidate.Content.Parts {
				if part.Text != "" {
					result.WriteString(part.Text)
				}
			}
		}
	}

	return result.String()
}
