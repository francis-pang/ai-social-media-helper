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
func BuildSocialMediaImagePrompt() string {
	now := time.Now()
	dateStr := now.Format("Monday, January 2, 2006")

	return fmt.Sprintf(`You are analyzing a personal photo for social media content creation. Today is %s.

## About the Person
The person in this image is Francis, the owner of this photo. When you see someone in the image, assume it is Francis unless there are clearly multiple distinct people.

## Your Task
Please provide a comprehensive analysis of this image and generate social media content. Structure your response as follows:

### 1. Image Analysis
- Describe what you see in the image in detail
- Identify the setting, environment, and atmosphere
- Note any significant objects, landmarks, or features
- Describe Francis's appearance, expression, and posture if visible
- Identify any activities or actions taking place

### 2. Metadata Analysis
- Based on visual cues (lighting, shadows, sun position), estimate the time of day
- Analyze weather conditions if outdoors
- Estimate the season based on clothing, vegetation, or other environmental factors
- Note the image quality and any camera/photography characteristics you can infer

### 3. Location Analysis
- Identify the location if recognizable (city, landmark, type of venue)
- If location cannot be determined precisely, describe the type of environment (urban, nature, indoor, beach, mountain, etc.)
- Note any text, signs, or identifying markers visible in the image
- Provide any geographic or cultural context clues

### 4. Social Media Post Generation
Based on your analysis, generate an engaging social media post that includes:

**Caption Options (provide 3 variations):**
1. **Casual/Personal**: A friendly, conversational caption for close friends
2. **Professional/LinkedIn**: A more refined version suitable for professional networking
3. **Inspirational/Motivational**: A caption with a deeper message or reflection

**Hashtag Suggestions:**
- Provide 5-10 relevant hashtags for maximum engagement
- Include a mix of popular and niche hashtags

**Best Posting Time Recommendation:**
- Suggest optimal posting times based on the content type

**Engagement Tips:**
- Brief suggestions on how to maximize engagement with this post

Please be specific, creative, and authentic in your analysis and content generation.`, dateStr)
}

