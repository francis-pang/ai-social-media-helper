package chat

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fpang/gemini-media-cli/internal/filehandler"
	"github.com/google/generative-ai-go/genai"
	"github.com/rs/zerolog/log"
)

const modelName = "gemini-3-flash-preview"

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

// AskImageQuestion sends an image along with a question to the Gemini API and returns the response.
func AskImageQuestion(ctx context.Context, client *genai.Client, mediaFile *filehandler.MediaFile, question string) (string, error) {
	log.Debug().
		Str("image_path", mediaFile.Path).
		Str("mime_type", mediaFile.MIMEType).
		Int64("size_bytes", mediaFile.Size).
		Msg("Sending image question to Gemini")

	model := client.GenerativeModel(modelName)

	// Create the image part
	imagePart := genai.Blob{
		MIMEType: mediaFile.MIMEType,
		Data:     mediaFile.Data,
	}

	// Send both the image and the question
	resp, err := model.GenerateContent(ctx, imagePart, genai.Text(question))
	if err != nil {
		log.Error().Err(err).Msg("Failed to generate content from image")
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
	log.Debug().Int("response_length", len(response)).Msg("Received image analysis response from Gemini")

	return response, nil
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

