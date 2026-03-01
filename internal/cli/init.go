package cli

import (
	"context"
	"os"

	"github.com/fpang/ai-social-media-helper/internal/auth"
	"github.com/fpang/ai-social-media-helper/internal/ai"
	"github.com/rs/zerolog/log"
	"google.golang.org/genai"
)

// InitGeminiClient creates and validates a Gemini client.
// Returns the context and client ready for use, or exits fatally on failure.
func InitGeminiClient() (context.Context, *genai.Client) {
	if err := ai.LoadGCPServiceAccount(); err != nil {
		log.Fatal().Err(err).Msg("failed to load GCP service account")
	}

	apiKey, err := auth.GetAPIKey()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to retrieve API key")
	}
	// Ensure key is in env for NewAIClient (e.g. when loaded from GPG)
	if apiKey != "" && os.Getenv("GEMINI_API_KEY") == "" {
		os.Setenv("GEMINI_API_KEY", apiKey)
	}

	ctx := context.Background()
	client, err := ai.NewAIClient(ctx)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create AI client")
	}

	log.Info().Msg("connection successful - Gemini client initialized")

	if err := auth.ValidateAPIKey(ctx, client); err != nil {
		HandleValidationError(err)
	}

	log.Info().Msg("API key validation complete - ready for operations")

	return ctx, client
}
