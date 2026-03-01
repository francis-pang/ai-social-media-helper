package cli

import (
	"context"

	"github.com/fpang/ai-social-media-helper/internal/auth"
	"github.com/fpang/ai-social-media-helper/internal/ai"
	"github.com/rs/zerolog/log"
	"google.golang.org/genai"
)

// InitGeminiClient creates and validates a Gemini client.
// Returns the context and client ready for use, or exits fatally on failure.
func InitGeminiClient() (context.Context, *genai.Client) {
	apiKey, err := auth.GetAPIKey()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to retrieve API key")
	}

	ctx := context.Background()
	client, err := ai.NewGeminiClient(ctx, apiKey)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create Gemini client")
	}

	log.Info().Msg("connection successful - Gemini client initialized")

	if err := auth.ValidateAPIKey(ctx, client); err != nil {
		HandleValidationError(err)
	}

	log.Info().Msg("API key validation complete - ready for operations")

	return ctx, client
}
