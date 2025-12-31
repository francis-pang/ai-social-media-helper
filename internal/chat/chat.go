package chat

import (
	"context"
	"fmt"
	"strings"
	"time"

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

