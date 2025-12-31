package chat

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fpang/gemini-media-cli/internal/filehandler"
	"github.com/google/generative-ai-go/genai"
	"github.com/rs/zerolog/log"
)

const modelName = "gemini-2.0-flash"

// SystemInstruction provides context for media analysis with extracted metadata.
const SystemInstruction = `You are an expert media analyst. Use the provided EXIF/FFmpeg metadata 
as the absolute ground truth for time, location, and camera settings while describing the visual 
content of the media. The metadata has been extracted locally and is authoritative.`

// UploadPollingInterval is the interval between checking upload state.
const UploadPollingInterval = 5 * time.Second

// UploadTimeout is the maximum time to wait for upload processing.
const UploadTimeout = 10 * time.Minute

// AskTextQuestion sends a text-only question to the Gemini API and returns the response.
func AskTextQuestion(ctx context.Context, client *genai.Client, question string) (string, error) {
	log.Debug().Str("question", question).Msg("Sending text question to Gemini")

	model := client.GenerativeModel(modelName)

	resp, err := model.GenerateContent(ctx, genai.Text(question))
	if err != nil {
		log.Error().Err(err).Msg("Failed to generate content")
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
	log.Debug().Int("response_length", len(response)).Msg("Received response from Gemini")

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

	// Always use Files API for all media uploads (DDR-012)
	file, err := uploadAndWaitForProcessing(ctx, client, mediaFile)
	if err != nil {
		return "", fmt.Errorf("failed to upload file: %w", err)
	}
	defer func() {
		// Clean up uploaded file after inference to manage quota
		if err := client.DeleteFile(ctx, file.Name); err != nil {
			log.Warn().Err(err).Str("file", file.Name).Msg("Failed to delete uploaded file")
		} else {
			log.Debug().Str("file", file.Name).Msg("Uploaded file deleted from Gemini storage")
		}
	}()

	// Configure model with system instruction for metadata context
	model := client.GenerativeModel(modelName)
	model.SystemInstruction = &genai.Content{
		Parts: []genai.Part{
			genai.Text(SystemInstruction),
		},
	}

	// Build parts: file reference + question
	parts := []genai.Part{
		genai.FileData{
			MIMEType: file.MIMEType,
			URI:      file.URI,
		},
		genai.Text(question),
	}

	// Generate content
	resp, err := model.GenerateContent(ctx, parts...)
	if err != nil {
		log.Error().Err(err).Msg("Failed to generate content from media")
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
	log.Debug().Int("response_length", len(response)).Msg("Received media analysis response from Gemini")

	return response, nil
}

// uploadAndWaitForProcessing uploads a file using the Files API and waits for it to be processed.
func uploadAndWaitForProcessing(ctx context.Context, client *genai.Client, mediaFile *filehandler.MediaFile) (*genai.File, error) {
	log.Info().
		Str("path", mediaFile.Path).
		Int64("size_mb", mediaFile.Size/(1024*1024)).
		Msg("Uploading file using Files API...")

	// Open the file for streaming upload
	f, err := os.Open(mediaFile.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	// Upload the file
	uploadStart := time.Now()
	file, err := client.UploadFile(ctx, "", f, &genai.UploadFileOptions{
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
	for file.State == genai.FileStateProcessing {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout waiting for file processing after %v", UploadTimeout)
		}

		log.Debug().
			Str("state", string(file.State)).
			Msg("File still processing, waiting...")

		time.Sleep(UploadPollingInterval)

		// Get updated file state
		file, err = client.GetFile(ctx, file.Name)
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
// and uses the appropriate prompt.
func BuildSocialMediaPrompt(metadata filehandler.MediaMetadata) string {
	if metadata == nil {
		return buildGenericSocialMediaPrompt("")
	}

	metadataContext := metadata.FormatMetadataContext()

	switch metadata.GetMediaType() {
	case "video":
		return buildSocialMediaVideoPrompt(metadataContext)
	case "image":
		return buildSocialMediaImagePrompt(metadataContext)
	default:
		return buildGenericSocialMediaPrompt(metadataContext)
	}
}

// BuildSocialMediaImagePrompt creates a comprehensive prompt for analyzing an image
// and generating a social media post description.
// If metadata is provided, it will be included in the prompt for context.
//
// Future expansion: Pre-resolve GPS coordinates using Google Maps Geocoding API
// before sending to Gemini. This would provide the resolved address, place name,
// and other location details as part of the prompt context, reducing reliance
// on Gemini's native Google Maps integration for reverse geocoding.
func BuildSocialMediaImagePrompt(metadataContext string) string {
	return buildSocialMediaImagePrompt(metadataContext)
}

func buildSocialMediaImagePrompt(metadataContext string) string {
	basePrompt := `You are analyzing a personal photo for social media content creation.

## About the Person
The person in this image is Francis, the owner of this photo.

`

	if metadataContext != "" {
		basePrompt += metadataContext + "\n"
	}

	basePrompt += `## Your Task

Using the metadata provided above (GPS coordinates and timestamp), analyze this image and generate social media content.

### 1. Reverse Geocoding (REQUIRED - Use Google Maps Integration)
You have native access to Google Maps. Use it to perform reverse geocoding on the provided GPS coordinates.

**For the GPS coordinates provided, look up and report:**
- **Exact Place Name**: The specific venue, business, landmark, or point of interest at these coordinates
- **Street Address**: The full street address
- **City**: City or town name
- **State/Region**: State, province, or region
- **Country**: Country name
- **Place Type**: Category (e.g., restaurant, park, stadium, historic site, etc.)
- **Known For**: What this place is famous for, any historical or cultural significance
- **Nearby Landmarks**: Other notable places nearby

### 2. Temporal Analysis (Use the Date/Time Provided)
- What time of day was this photo taken?
- What day of the week was it?
- What season is this?
- Is there any significance to this date (holiday, event, weekend)?
- Does the lighting in the image match the timestamp?

### 3. Visual Analysis
Describe what you see in the image:
- Francis's appearance, outfit, expression, and pose
- The environment and setting visible in the photo
- Notable features, landmarks, or interesting elements
- The overall mood and atmosphere
- Does the visual content match the location from reverse geocoding?

### 4. Social Media Post Generation
Based on the REAL location from reverse geocoding and the actual date/time, generate:

**Caption Options (provide 3 variations):**
1. **Casual/Personal**: Friendly, conversational - mention the actual location by name
2. **Professional/LinkedIn**: Polished, suitable for networking - reference the real place
3. **Inspirational/Motivational**: Deeper message tied to the location or moment

**Hashtag Suggestions (10-15 hashtags):**
- Location-specific hashtags (city name, venue name, country)
- Activity or context hashtags
- Date-relevant hashtags if applicable (e.g., #NewYearsEve if Dec 31)
- General engagement hashtags`

	return basePrompt
}

// BuildSocialMediaVideoPrompt creates a comprehensive prompt for analyzing a video
// and generating a social media post description.
func BuildSocialMediaVideoPrompt(metadataContext string) string {
	return buildSocialMediaVideoPrompt(metadataContext)
}

func buildSocialMediaVideoPrompt(metadataContext string) string {
	basePrompt := `You are analyzing a personal video for social media content creation.

## About the Person
The person in this video is Francis, the owner of this video.

`

	if metadataContext != "" {
		basePrompt += metadataContext + "\n"
	}

	basePrompt += `## Your Task

Using the metadata provided above (GPS coordinates, timestamp, and video properties), analyze this video and generate social media content.

### 1. Reverse Geocoding (REQUIRED - Use Google Maps Integration)
You have native access to Google Maps. Use it to perform reverse geocoding on the provided GPS coordinates.

**For the GPS coordinates provided, look up and report:**
- **Exact Place Name**: The specific venue, business, landmark, or point of interest at these coordinates
- **Street Address**: The full street address
- **City**: City or town name
- **State/Region**: State, province, or region
- **Country**: Country name
- **Place Type**: Category (e.g., restaurant, park, stadium, event venue, etc.)
- **Known For**: What this place is famous for, any historical or cultural significance
- **Nearby Landmarks**: Other notable places nearby

### 2. Temporal Analysis (Use the Date/Time Provided)
- What time of day was this video recorded?
- What day of the week was it?
- What season is this?
- Is there any significance to this date (holiday, event, weekend)?
- Does the lighting in the video match the timestamp?

### 3. Video Content Analysis
Analyze the video content thoroughly:
- **Opening Scene**: What happens at the beginning?
- **Key Moments**: Identify 3-5 highlight moments or key frames
- **People & Activities**: Who appears and what are they doing?
- **Setting & Environment**: Describe the location visible in the video
- **Audio Content**: Describe any speech, music, or ambient sounds
- **Movement & Action**: Describe the primary action or movement
- **Mood & Atmosphere**: What is the overall vibe?
- **Video Quality**: Note if it's 4K, HDR, slow-motion, etc.

### 4. Social Media Post Generation
Based on the REAL location from reverse geocoding and the actual date/time, generate:

**Caption Options (provide 3 variations):**
1. **Casual/Personal**: Friendly, conversational - mention the actual location by name, reference what happens in the video
2. **Professional/LinkedIn**: Polished, suitable for networking - focus on the experience or achievement
3. **Inspirational/Motivational**: Deeper message tied to the location, moment, or activity

**Platform-Specific Recommendations:**
- **Instagram Reels/TikTok**: Best segments to use, suggested cuts, trending audio ideas
- **YouTube Shorts**: Title suggestions, thumbnail moment recommendation
- **Twitter/X**: Brief caption version (under 280 characters)

**Hashtag Suggestions (10-15 hashtags):**
- Location-specific hashtags (city name, venue name, country)
- Activity or event hashtags
- Video content hashtags (#travel, #adventure, #vlog, etc.)
- Date-relevant hashtags if applicable (e.g., #NewYearsEve if Dec 31)
- Platform-specific hashtags (#reels, #shorts, etc.)`

	return basePrompt
}

func buildGenericSocialMediaPrompt(metadataContext string) string {
	basePrompt := `You are analyzing media content for social media content creation.

## About the Person
The person in this media is Francis, the owner of this content.

`

	if metadataContext != "" {
		basePrompt += metadataContext + "\n"
	}

	basePrompt += `## Your Task

Analyze this media and generate social media content including:
1. Description of what you see
2. Three caption variations (casual, professional, inspirational)
3. 10-15 relevant hashtags`

	return basePrompt
}
