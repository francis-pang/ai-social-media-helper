// Package main provides a Lambda entry point for the media triage API.
//
// It wraps the same triage logic from the chat package behind API Gateway,
// using S3 for media storage instead of the local filesystem.
//
// Security (DDR-028):
//   - Origin-verify middleware blocks direct API Gateway access (CloudFront-only)
//   - Input validation on sessionId (UUID), filename (safe chars), S3 key (uuid/filename)
//   - Content-type allowlist and file size limits for uploads
//   - Cryptographically random job IDs prevent enumeration
//   - Session ownership enforced on triage results/confirm
//
// Endpoints:
//
//	GET  /api/health               — health check (no auth required)
//	GET  /api/upload-url           — presigned S3 PUT URL for browser upload
//	POST /api/triage/start         — start triage from uploaded S3 files
//	GET  /api/triage/{id}/results  — poll triage results
//	POST /api/triage/{id}/confirm  — delete confirmed files from S3
//	POST /api/download/start       — start ZIP bundle creation for a post group (DDR-034)
//	GET  /api/download/{id}/results — poll download bundle status and URLs (DDR-034)
//	POST /api/description/generate — generate AI Instagram caption for a post group (DDR-036)
//	GET  /api/description/{id}/results — poll caption generation results (DDR-036)
//	POST /api/description/{id}/feedback — regenerate caption with user feedback (DDR-036)
//	POST /api/publish/start         — start publishing a post group to Instagram (DDR-040)
//	GET  /api/publish/{id}/status  — poll publishing progress (DDR-040)
//	POST /api/session/invalidate   — invalidate downstream state on back-navigation (DDR-037)
//	GET  /api/media/thumbnail      — generate thumbnail from S3 object
//	GET  /api/media/full           — presigned GET URL for full-resolution image
package main

import (
	"context"
	"net/http"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"

	"github.com/fpang/gemini-media-cli/internal/instagram"
	"github.com/fpang/gemini-media-cli/internal/logging"
	"github.com/rs/zerolog/log"
)

func init() {
	logging.Init()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load AWS config")
	}

	s3Client = s3.NewFromConfig(cfg)
	presigner = s3.NewPresignClient(s3Client)
	mediaBucket = os.Getenv("MEDIA_BUCKET_NAME")
	if mediaBucket == "" {
		log.Fatal().Msg("MEDIA_BUCKET_NAME environment variable is required")
	}

	originVerifySecret = os.Getenv("ORIGIN_VERIFY_SECRET")
	if originVerifySecret == "" {
		log.Warn().Msg("ORIGIN_VERIFY_SECRET not set — origin verification disabled")
	}

	// Load Gemini API key from SSM Parameter Store if not set via env var.
	ssmClient := ssm.NewFromConfig(cfg)
	if os.Getenv("GEMINI_API_KEY") == "" {
		paramName := os.Getenv("SSM_API_KEY_PARAM")
		if paramName == "" {
			paramName = "/ai-social-media/prod/gemini-api-key"
		}
		result, err := ssmClient.GetParameter(context.Background(), &ssm.GetParameterInput{
			Name:           &paramName,
			WithDecryption: aws.Bool(true),
		})
		if err != nil {
			log.Fatal().Err(err).Str("param", paramName).Msg("Failed to read API key from SSM")
		}
		os.Setenv("GEMINI_API_KEY", *result.Parameter.Value)
		log.Info().Msg("Gemini API key loaded from SSM Parameter Store")
	}

	// Load Instagram credentials from SSM Parameter Store (DDR-040).
	// Non-fatal: if credentials are not configured, publishing is disabled.
	igAccessToken := os.Getenv("INSTAGRAM_ACCESS_TOKEN")
	igUserID := os.Getenv("INSTAGRAM_USER_ID")
	if igAccessToken == "" || igUserID == "" {
		tokenParam := os.Getenv("SSM_INSTAGRAM_TOKEN_PARAM")
		if tokenParam == "" {
			tokenParam = "/ai-social-media/prod/instagram-access-token"
		}
		userIDParam := os.Getenv("SSM_INSTAGRAM_USER_ID_PARAM")
		if userIDParam == "" {
			userIDParam = "/ai-social-media/prod/instagram-user-id"
		}

		tokenResult, err := ssmClient.GetParameter(context.Background(), &ssm.GetParameterInput{
			Name:           &tokenParam,
			WithDecryption: aws.Bool(true),
		})
		if err != nil {
			log.Warn().Err(err).Str("param", tokenParam).Msg("Instagram access token not found in SSM — publishing disabled")
		} else {
			igAccessToken = *tokenResult.Parameter.Value
		}

		userIDResult, err := ssmClient.GetParameter(context.Background(), &ssm.GetParameterInput{
			Name:           &userIDParam,
			WithDecryption: aws.Bool(false),
		})
		if err != nil {
			log.Warn().Err(err).Str("param", userIDParam).Msg("Instagram user ID not found in SSM — publishing disabled")
		} else {
			igUserID = *userIDResult.Parameter.Value
		}
	}
	if igAccessToken != "" && igUserID != "" {
		igClient = instagram.NewClient(igAccessToken, igUserID)
		log.Info().Str("userId", igUserID).Msg("Instagram client initialized")
	} else {
		log.Warn().Msg("Instagram credentials not configured — publishing disabled")
	}
}

func main() {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/health", handleHealth)
	mux.HandleFunc("/api/upload-url", handleUploadURL)
	mux.HandleFunc("/api/triage/start", handleTriageStart)
	mux.HandleFunc("/api/triage/", handleTriageRoutes)
	mux.HandleFunc("/api/selection/start", handleSelectionStart)
	mux.HandleFunc("/api/selection/", handleSelectionRoutes)
	mux.HandleFunc("/api/enhance/start", handleEnhanceStart)
	mux.HandleFunc("/api/enhance/", handleEnhanceRoutes)
	mux.HandleFunc("/api/download/start", handleDownloadStart)
	mux.HandleFunc("/api/download/", handleDownloadRoutes)
	mux.HandleFunc("/api/description/generate", handleDescriptionGenerate)
	mux.HandleFunc("/api/description/", handleDescriptionRoutes)
	mux.HandleFunc("/api/publish/start", handlePublishStart)       // DDR-040
	mux.HandleFunc("/api/publish/", handlePublishRoutes)           // DDR-040
	mux.HandleFunc("/api/session/invalidate", handleSessionInvalidate) // DDR-037
	mux.HandleFunc("/api/media/thumbnail", handleThumbnail)
	mux.HandleFunc("/api/media/full", handleFullImage)

	// Wrap with middleware chain: metrics -> origin-verify -> handler
	handler := withMetrics(withOriginVerify(mux))

	adapter := httpadapter.NewV2(handler)
	lambda.Start(adapter.ProxyWithContext)
}

// --- Health ---

func handleHealth(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"status":              "ok",
		"service":             "ai-social-media-helper",
		"instagramConfigured": igClient != nil,
	})
}
