package cli

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/fpang/gemini-media-cli/internal/auth"
	"github.com/rs/zerolog/log"
)

// ValidateAndResolveDirectory checks that the path exists and is a directory,
// then returns the absolute path. Exits fatally on failure.
func ValidateAndResolveDirectory(dirPath string) string {
	info, err := os.Stat(dirPath)
	if err != nil {
		if os.IsNotExist(err) {
			log.Fatal().Str("path", dirPath).Msg("Directory not found")
		}
		log.Fatal().Err(err).Str("path", dirPath).Msg("Failed to access directory")
	}
	if !info.IsDir() {
		log.Fatal().Str("path", dirPath).Msg("Path is not a directory")
	}

	absPath, err := filepath.Abs(dirPath)
	if err == nil {
		dirPath = absPath
	}

	return dirPath
}

// HandleValidationError processes auth.ValidationError and exits with appropriate messaging.
func HandleValidationError(err error) {
	var validationErr *auth.ValidationError
	if errors.As(err, &validationErr) {
		switch validationErr.Type {
		case auth.ErrTypeNoKey:
			log.Fatal().Msg("No API key configured. Set GEMINI_API_KEY or run scripts/setup-gpg-credentials.sh")
		case auth.ErrTypeInvalidKey:
			log.Fatal().Err(err).Msg("Invalid API key. Please check your API key and try again")
		case auth.ErrTypeNetworkError:
			log.Fatal().Err(err).Msg("Network error. Please check your internet connection")
		case auth.ErrTypeQuotaExceeded:
			log.Fatal().Err(err).Msg("API quota exceeded. Please try again later or check your usage limits")
		default:
			log.Fatal().Err(err).Msg("API key validation failed")
		}
	} else {
		log.Fatal().Err(err).Msg("unexpected error during API key validation")
	}
	os.Exit(1)
}
