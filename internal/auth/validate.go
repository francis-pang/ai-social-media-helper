package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/fpang/gemini-media-cli/internal/metrics"
	"github.com/rs/zerolog/log"
	"google.golang.org/genai"
)

// ValidationError represents a specific type of API key validation failure.
type ValidationError struct {
	Type    ValidationErrorType
	Message string
	Err     error
}

// ValidationErrorType categorizes validation failures.
type ValidationErrorType int

const (
	// ErrTypeNoKey indicates no API key was found.
	ErrTypeNoKey ValidationErrorType = iota
	// ErrTypeInvalidKey indicates the API key is invalid or revoked.
	ErrTypeInvalidKey
	// ErrTypeNetworkError indicates a network connectivity issue.
	ErrTypeNetworkError
	// ErrTypeQuotaExceeded indicates the API quota has been exceeded.
	ErrTypeQuotaExceeded
	// ErrTypeUnknown indicates an unknown error occurred.
	ErrTypeUnknown
)

func (e *ValidationError) Error() string {
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

func (e *ValidationError) Unwrap() error {
	return e.Err
}

// ValidateAPIKey verifies that the API key is valid by making a minimal API call.
// It returns nil if the key is valid, or a ValidationError with a specific type
// indicating the nature of the failure.
func ValidateAPIKey(ctx context.Context, client *genai.Client) error {
	log.Debug().Msg("Validating API key with Gemini API")

	// Use Gemini 3 Flash Preview (free tier compatible)
	// Make a minimal request to validate the API key
	start := time.Now()
	resp, err := client.Models.GenerateContent(ctx, "gemini-3-flash-preview", genai.Text("hi"), nil)
	elapsed := time.Since(start)

	// Emit validation metrics
	result := "success"
	if err != nil {
		valErr := classifyError(err)
		switch valErr.Type {
		case ErrTypeInvalidKey:
			result = "invalid"
		case ErrTypeNetworkError:
			result = "network_error"
		case ErrTypeQuotaExceeded:
			result = "quota"
		default:
			result = "unknown"
		}

		metrics.New("AiSocialMedia").
			Dimension("Result", result).
			Metric("ApiKeyValidationMs", float64(elapsed.Milliseconds()), metrics.UnitMilliseconds).
			Count("ApiKeyValidationResult").
			Flush()

		return valErr
	}

	// Verify we got a valid response
	if resp == nil || len(resp.Candidates) == 0 {
		log.Warn().Msg("API key validation returned empty response")
		metrics.New("AiSocialMedia").
			Dimension("Result", "empty_response").
			Metric("ApiKeyValidationMs", float64(elapsed.Milliseconds()), metrics.UnitMilliseconds).
			Count("ApiKeyValidationResult").
			Flush()
		return &ValidationError{
			Type:    ErrTypeUnknown,
			Message: "API returned empty response",
		}
	}

	metrics.New("AiSocialMedia").
		Dimension("Result", result).
		Metric("ApiKeyValidationMs", float64(elapsed.Milliseconds()), metrics.UnitMilliseconds).
		Count("ApiKeyValidationResult").
		Flush()

	log.Debug().
		Str("result", result).
		Dur("duration", elapsed).
		Msg("API key validation result")

	log.Info().Msg("API key validated successfully")
	return nil
}

// classifyError analyzes an error and returns a ValidationError with the appropriate type.
func classifyError(err error) *ValidationError {
	if err == nil {
		return nil
	}

	errMsg := err.Error()
	errLower := strings.ToLower(errMsg)

	// Check for Google API errors
	var apiErr *genai.APIError
	if errors.As(err, &apiErr) {
		return classifyAPIError(apiErr)
	}

	// Check for common error patterns in the error message
	switch {
	case strings.Contains(errLower, "api key not valid") ||
		strings.Contains(errLower, "invalid api key") ||
		strings.Contains(errLower, "api_key_invalid") ||
		strings.Contains(errLower, "permission denied"):
		log.Error().Err(err).Msg("Invalid API key")
		return &ValidationError{
			Type:    ErrTypeInvalidKey,
			Message: "API key is invalid or has been revoked",
			Err:     err,
		}

	case strings.Contains(errLower, "quota") ||
		strings.Contains(errLower, "resource exhausted") ||
		strings.Contains(errLower, "rate limit"):
		log.Error().Err(err).Msg("API quota exceeded")
		return &ValidationError{
			Type:    ErrTypeQuotaExceeded,
			Message: "API quota exceeded or rate limited",
			Err:     err,
		}

	case strings.Contains(errLower, "connection") ||
		strings.Contains(errLower, "network") ||
		strings.Contains(errLower, "timeout") ||
		strings.Contains(errLower, "dial") ||
		strings.Contains(errLower, "no such host") ||
		strings.Contains(errLower, "unreachable"):
		log.Error().Err(err).Msg("Network error during API validation")
		return &ValidationError{
			Type:    ErrTypeNetworkError,
			Message: "Network error - check your internet connection",
			Err:     err,
		}

	default:
		log.Error().Err(err).Msg("Unknown error during API validation")
		return &ValidationError{
			Type:    ErrTypeUnknown,
			Message: "Failed to validate API key",
			Err:     err,
		}
	}
}

// classifyAPIError categorizes a Google API error.
func classifyAPIError(err *genai.APIError) *ValidationError {
	switch err.Code {
	case 400:
		log.Error().Int("code", err.Code).Msg("Bad request - possibly invalid API key format")
		return &ValidationError{
			Type:    ErrTypeInvalidKey,
			Message: "Bad request - API key may be malformed",
			Err:     err,
		}

	case 401, 403:
		log.Error().Int("code", err.Code).Msg("Authentication failed - invalid API key")
		return &ValidationError{
			Type:    ErrTypeInvalidKey,
			Message: "API key is invalid, expired, or lacks permissions",
			Err:     err,
		}

	case 429:
		log.Error().Int("code", err.Code).Msg("Rate limit exceeded")
		return &ValidationError{
			Type:    ErrTypeQuotaExceeded,
			Message: "API rate limit exceeded - try again later",
			Err:     err,
		}

	case 500, 502, 503, 504:
		log.Error().Int("code", err.Code).Msg("Server error during validation")
		return &ValidationError{
			Type:    ErrTypeNetworkError,
			Message: "Gemini API server error - try again later",
			Err:     err,
		}

	default:
		log.Error().Int("code", err.Code).Str("message", err.Message).Msg("Google API error")
		return &ValidationError{
			Type:    ErrTypeUnknown,
			Message: err.Message,
			Err:     err,
		}
	}
}
