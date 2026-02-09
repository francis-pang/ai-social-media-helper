// Package main provides a Lambda entry point for the Instagram OAuth
// callback handler (DDR-048).
//
// This is a lightweight Lambda (128 MB, 10s timeout) that handles:
//   - GET /oauth/callback?code=AUTH_CODE — exchange code for tokens, store in SSM
//   - GET /oauth/callback?error=... — user denied access
//
// Credentials are loaded from SSM Parameter Store at cold start:
//   - /ai-social-media/prod/instagram-app-id
//   - /ai-social-media/prod/instagram-app-secret
//   - /ai-social-media/prod/instagram-oauth-redirect-uri
//
// On successful token exchange, the Lambda writes the long-lived token
// and user ID to SSM, making them available to the API Lambda for
// Instagram publishing (DDR-040).
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"

	"github.com/fpang/gemini-media-cli/internal/instagram"
	"github.com/fpang/gemini-media-cli/internal/logging"
	"github.com/rs/zerolog/log"
)

var (
	ssmClient   *ssm.Client
	appID       string
	appSecret   string
	redirectURI string

	// SSM parameter paths for writing tokens (read from environment).
	tokenParam  string
	userIDParam string
)

func init() {
	logging.Init()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load AWS config")
	}

	ssmClient = ssm.NewFromConfig(cfg)

	// Load Instagram App ID from SSM.
	appID = os.Getenv("INSTAGRAM_APP_ID")
	if appID == "" {
		paramName := os.Getenv("SSM_APP_ID_PARAM")
		if paramName == "" {
			paramName = "/ai-social-media/prod/instagram-app-id"
		}
		result, err := ssmClient.GetParameter(context.Background(), &ssm.GetParameterInput{
			Name:           &paramName,
			WithDecryption: aws.Bool(false),
		})
		if err != nil {
			log.Fatal().Err(err).Str("param", paramName).Msg("Failed to read Instagram app ID from SSM")
		}
		appID = *result.Parameter.Value
		log.Info().Msg("Instagram app ID loaded from SSM")
	}

	// Load Instagram App Secret from SSM.
	appSecret = os.Getenv("INSTAGRAM_APP_SECRET")
	if appSecret == "" {
		paramName := os.Getenv("SSM_APP_SECRET_PARAM")
		if paramName == "" {
			paramName = "/ai-social-media/prod/instagram-app-secret"
		}
		result, err := ssmClient.GetParameter(context.Background(), &ssm.GetParameterInput{
			Name:           &paramName,
			WithDecryption: aws.Bool(true),
		})
		if err != nil {
			log.Fatal().Err(err).Str("param", paramName).Msg("Failed to read app secret from SSM")
		}
		appSecret = *result.Parameter.Value
		log.Info().Msg("Instagram app secret loaded from SSM")
	}

	// Load OAuth redirect URI from SSM.
	redirectURI = os.Getenv("OAUTH_REDIRECT_URI")
	if redirectURI == "" {
		paramName := os.Getenv("SSM_REDIRECT_URI_PARAM")
		if paramName == "" {
			paramName = "/ai-social-media/prod/instagram-oauth-redirect-uri"
		}
		result, err := ssmClient.GetParameter(context.Background(), &ssm.GetParameterInput{
			Name:           &paramName,
			WithDecryption: aws.Bool(false),
		})
		if err != nil {
			log.Fatal().Err(err).Str("param", paramName).Msg("Failed to read OAuth redirect URI from SSM")
		}
		redirectURI = *result.Parameter.Value
		log.Info().Str("redirectUri", redirectURI).Msg("OAuth redirect URI loaded from SSM")
	}

	// SSM parameter paths for writing tokens.
	tokenParam = os.Getenv("SSM_TOKEN_PARAM")
	if tokenParam == "" {
		tokenParam = "/ai-social-media/prod/instagram-access-token"
	}
	userIDParam = os.Getenv("SSM_USER_ID_PARAM")
	if userIDParam == "" {
		userIDParam = "/ai-social-media/prod/instagram-user-id"
	}

	log.Info().Msg("OAuth Lambda initialized")
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/callback", handleOAuthCallback)

	adapter := httpadapter.NewV2(mux)
	lambda.Start(adapter.ProxyWithContext)
}

// handleOAuthCallback processes the Instagram OAuth redirect.
// Meta redirects the user's browser here with ?code=AUTH_CODE (success)
// or ?error=ERROR&error_reason=REASON&error_description=DESC (denied).
func handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondHTML(w, http.StatusMethodNotAllowed, "Error", "Method not allowed.")
		return
	}

	// Check for error response (user denied access).
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		reason := r.URL.Query().Get("error_reason")
		desc := r.URL.Query().Get("error_description")
		log.Warn().Str("error", errParam).Str("reason", reason).Str("description", desc).
			Msg("OAuth authorization denied by user")
		respondHTML(w, http.StatusOK, "Authorization Denied",
			fmt.Sprintf("Instagram authorization was denied: %s.", reason))
		return
	}

	// Extract authorization code.
	code := r.URL.Query().Get("code")
	if code == "" {
		log.Error().Msg("OAuth callback received without code or error parameter")
		respondHTML(w, http.StatusBadRequest, "Error", "Missing authorization code.")
		return
	}

	ctx := r.Context()

	// Step 1: Exchange authorization code for short-lived token.
	shortResult, err := instagram.ExchangeCode(ctx, code, appID, appSecret, redirectURI)
	if err != nil {
		log.Error().Err(err).Msg("Failed to exchange authorization code")
		respondHTML(w, http.StatusBadGateway, "Token Exchange Failed",
			"Failed to exchange the authorization code for an access token. Please try again.")
		return
	}

	// Step 2: Exchange short-lived token for long-lived token.
	longResult, err := instagram.ExchangeLongLivedToken(ctx, shortResult.AccessToken, appSecret)
	if err != nil {
		log.Error().Err(err).Msg("Failed to exchange for long-lived token")
		respondHTML(w, http.StatusBadGateway, "Token Exchange Failed",
			"Failed to exchange for a long-lived token. Please try again.")
		return
	}

	// Step 3: Store long-lived token in SSM (SecureString, overwrite).
	_, err = ssmClient.PutParameter(ctx, &ssm.PutParameterInput{
		Name:      &tokenParam,
		Value:     &longResult.AccessToken,
		Type:      ssmtypes.ParameterTypeSecureString,
		Overwrite: aws.Bool(true),
	})
	if err != nil {
		log.Error().Err(err).Str("param", tokenParam).Msg("Failed to store access token in SSM")
		respondHTML(w, http.StatusInternalServerError, "Storage Failed",
			"Token was obtained but could not be stored. Please check Lambda logs.")
		return
	}
	log.Info().Str("param", tokenParam).Msg("Long-lived access token stored in SSM")

	// Step 4: Store user ID in SSM (String, overwrite).
	_, err = ssmClient.PutParameter(ctx, &ssm.PutParameterInput{
		Name:      &userIDParam,
		Value:     &shortResult.UserID,
		Type:      ssmtypes.ParameterTypeString,
		Overwrite: aws.Bool(true),
	})
	if err != nil {
		log.Error().Err(err).Str("param", userIDParam).Msg("Failed to store user ID in SSM")
		respondHTML(w, http.StatusInternalServerError, "Storage Failed",
			"Token was stored but user ID could not be saved. Please check Lambda logs.")
		return
	}
	log.Info().Str("param", userIDParam).Str("userId", shortResult.UserID).Msg("Instagram user ID stored in SSM")

	// Success — render confirmation page.
	days := longResult.ExpiresIn / 86400
	respondHTML(w, http.StatusOK, "Instagram Connected",
		fmt.Sprintf("Your Instagram account (user ID: %s) has been connected successfully.<br><br>"+
			"Long-lived token stored — expires in %d days.<br><br>"+
			"The API Lambda will use the new token on its next cold start.<br>"+
			"You can close this window.", shortResult.UserID, days))
}

// respondHTML writes a minimal HTML page with the given title and message.
func respondHTML(w http.ResponseWriter, status int, title, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>%s</title>
  <style>
    body { font-family: system-ui, -apple-system, sans-serif; max-width: 600px; margin: 80px auto; padding: 0 20px; text-align: center; color: #1a1a1a; }
    h1 { font-size: 1.5rem; margin-bottom: 1rem; }
    p { font-size: 1rem; line-height: 1.6; color: #444; }
  </style>
</head>
<body>
  <h1>%s</h1>
  <p>%s</p>
</body>
</html>`, title, title, message)
}
