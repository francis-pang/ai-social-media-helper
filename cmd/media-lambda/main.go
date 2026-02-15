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
//	POST /api/upload-multipart/init     — create S3 multipart upload + presign part URLs (DDR-054)
//	POST /api/upload-multipart/complete — complete S3 multipart upload with ETags (DDR-054)
//	POST /api/upload-multipart/abort    — abort S3 multipart upload (DDR-054)
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
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	lambdasvc "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"

	"github.com/fpang/gemini-media-cli/internal/instagram"
	"github.com/fpang/gemini-media-cli/internal/logging"
	"github.com/fpang/gemini-media-cli/internal/store"
	"github.com/rs/zerolog/log"
)

var coldStart = true

func init() {
	initStart := time.Now()
	logging.Init()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load AWS config")
	}
	log.Debug().Str("region", cfg.Region).Msg("AWS config loaded")

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

	// Initialize DynamoDB session store (DDR-050: persistent job state).
	dynamoTableName := os.Getenv("DYNAMO_TABLE_NAME")
	if dynamoTableName != "" {
		ddbClient := dynamodb.NewFromConfig(cfg)
		sessionStore = store.NewDynamoStore(ddbClient, dynamoTableName)
		log.Info().Str("table", dynamoTableName).Msg("DynamoDB session store initialized")
	} else {
		log.Warn().Msg("DYNAMO_TABLE_NAME not set — DynamoDB store disabled")
	}

	// Initialize file processing store for per-file triage status (DDR-061).
	fpTableName := os.Getenv("FILE_PROCESSING_TABLE_NAME")
	if fpTableName != "" && sessionStore != nil {
		fileProcessStore = store.NewFileProcessingStore(sessionStore.Client(), fpTableName)
	}

	// Initialize Lambda client for async invocations (DDR-050, DDR-053).
	lambdaClient = lambdasvc.NewFromConfig(cfg)
	descriptionLambdaArn = os.Getenv("DESCRIPTION_LAMBDA_ARN")
	downloadLambdaArn = os.Getenv("DOWNLOAD_LAMBDA_ARN")
	enhanceLambdaArn = os.Getenv("ENHANCE_LAMBDA_ARN")
	if descriptionLambdaArn == "" || downloadLambdaArn == "" || enhanceLambdaArn == "" {
		log.Warn().Msg("One or more Lambda ARNs not set — async dispatch may be disabled (DDR-053)")
	}

	// Initialize Step Functions client for pipelines (DDR-050, DDR-052).
	sfnClient = sfn.NewFromConfig(cfg)
	selectionSfnArn = os.Getenv("SELECTION_STATE_MACHINE_ARN")
	enhancementSfnArn = os.Getenv("ENHANCEMENT_STATE_MACHINE_ARN")
	triageSfnArn = os.Getenv("TRIAGE_STATE_MACHINE_ARN")
	publishSfnArn = os.Getenv("PUBLISH_STATE_MACHINE_ARN")
	if selectionSfnArn == "" || enhancementSfnArn == "" {
		log.Warn().Msg("Selection/Enhancement state machine ARNs not set — Step Functions dispatch disabled")
	}
	if triageSfnArn == "" || publishSfnArn == "" {
		log.Warn().Msg("Triage/Publish state machine ARNs not set — Step Functions dispatch disabled (DDR-052)")
	}

	// Load Gemini API key from SSM Parameter Store if not set via env var.
	ssmClient := ssm.NewFromConfig(cfg)
	if os.Getenv("GEMINI_API_KEY") == "" {
		paramName := os.Getenv("SSM_API_KEY_PARAM")
		if paramName == "" {
			paramName = "/ai-social-media/prod/gemini-api-key"
		}
		ssmStart := time.Now()
		result, err := ssmClient.GetParameter(context.Background(), &ssm.GetParameterInput{
			Name:           &paramName,
			WithDecryption: aws.Bool(true),
		})
		if err != nil {
			log.Fatal().Err(err).Str("param", paramName).Msg("Failed to read API key from SSM")
		}
		os.Setenv("GEMINI_API_KEY", *result.Parameter.Value)
		log.Debug().Str("param", paramName).Dur("elapsed", time.Since(ssmStart)).Msg("Gemini API key loaded from SSM")
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

		ssmStart := time.Now()
		tokenResult, err := ssmClient.GetParameter(context.Background(), &ssm.GetParameterInput{
			Name:           &tokenParam,
			WithDecryption: aws.Bool(true),
		})
		if err != nil {
			log.Warn().Err(err).Str("param", tokenParam).Msg("Instagram access token not found in SSM — publishing disabled")
		} else {
			igAccessToken = *tokenResult.Parameter.Value
			log.Debug().Str("param", tokenParam).Dur("elapsed", time.Since(ssmStart)).Msg("Instagram token loaded from SSM")
		}

		ssmStart = time.Now()
		userIDResult, err := ssmClient.GetParameter(context.Background(), &ssm.GetParameterInput{
			Name:           &userIDParam,
			WithDecryption: aws.Bool(false),
		})
		if err != nil {
			log.Warn().Err(err).Str("param", userIDParam).Msg("Instagram user ID not found in SSM — publishing disabled")
		} else {
			igUserID = *userIDResult.Parameter.Value
			log.Debug().Str("param", userIDParam).Dur("elapsed", time.Since(ssmStart)).Msg("Instagram user ID loaded from SSM")
		}
	}
	if igAccessToken != "" && igUserID != "" {
		igClient = instagram.NewClient(igAccessToken, igUserID)
		log.Info().Str("userId", igUserID).Msg("Instagram client initialized")
	} else {
		log.Warn().Msg("Instagram credentials not configured — publishing disabled")
	}

	// Emit consolidated cold-start log for troubleshooting (DDR-062: version identity).
	logging.NewStartupLogger("media-lambda").
		CommitHash(commitHash).
		BuildTime(buildTime).
		InitDuration(time.Since(initStart)).
		S3Bucket("mediaBucket", mediaBucket).
		DynamoTable("sessions", dynamoTableName).
		SSMParam("geminiApiKey", logging.EnvOrDefault("SSM_API_KEY_PARAM", "/ai-social-media/prod/gemini-api-key")).
		SSMParam("instagramToken", logging.EnvOrDefault("SSM_INSTAGRAM_TOKEN_PARAM", "/ai-social-media/prod/instagram-access-token")).
		SSMParam("instagramUserId", logging.EnvOrDefault("SSM_INSTAGRAM_USER_ID_PARAM", "/ai-social-media/prod/instagram-user-id")).
		StateMachine("selectionPipeline", selectionSfnArn).
		StateMachine("enhancementPipeline", enhancementSfnArn).
		StateMachine("triagePipeline", triageSfnArn).
		StateMachine("publishPipeline", publishSfnArn).
		LambdaFunc("descriptionLambda", descriptionLambdaArn).
		LambdaFunc("downloadLambda", downloadLambdaArn).
		LambdaFunc("enhanceLambda", enhanceLambdaArn).
		Feature("instagram", igClient != nil).
		Feature("originVerify", originVerifySecret != "").
		Feature("dynamodb", sessionStore != nil).
		Log()
}

func main() {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/health", handleHealth)
	mux.HandleFunc("/api/upload-url", handleUploadURL)
	mux.HandleFunc("/api/upload-multipart/init", handleMultipartInit)         // DDR-054
	mux.HandleFunc("/api/upload-multipart/complete", handleMultipartComplete) // DDR-054
	mux.HandleFunc("/api/upload-multipart/abort", handleMultipartAbort)       // DDR-054
	mux.HandleFunc("/api/triage/init", handleTriageInit)
	mux.HandleFunc("/api/triage/update-files", handleTriageUpdateFiles)
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
	mux.HandleFunc("/api/publish/start", handlePublishStart)           // DDR-040
	mux.HandleFunc("/api/publish/", handlePublishRoutes)               // DDR-040
	mux.HandleFunc("/api/session/invalidate", handleSessionInvalidate) // DDR-037
	mux.HandleFunc("/api/media/thumbnail", handleThumbnail)
	mux.HandleFunc("/api/media/full", handleFullImage)
	mux.HandleFunc("/api/media/compressed", handleCompressedVideo)

	// Catch-all: log unmatched routes explicitly (DDR-062: distinguish mux-404 from handler-404).
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Warn().Str("method", r.Method).Str("path", r.URL.Path).Msg("No route matched — returning 404")
		httpError(w, http.StatusNotFound, "not found")
	})

	// Log registered routes at cold start for troubleshooting (DDR-062).
	routes := []string{
		"/api/health", "/api/upload-url",
		"/api/upload-multipart/init", "/api/upload-multipart/complete", "/api/upload-multipart/abort",
		"/api/triage/init", "/api/triage/update-files", "/api/triage/start", "/api/triage/",
		"/api/selection/start", "/api/selection/",
		"/api/enhance/start", "/api/enhance/",
		"/api/download/start", "/api/download/",
		"/api/description/generate", "/api/description/",
		"/api/publish/start", "/api/publish/",
		"/api/session/invalidate",
		"/api/media/thumbnail", "/api/media/full", "/api/media/compressed",
	}
	log.Info().Strs("routes", routes).Int("count", len(routes)).Msg("HTTP routes registered")

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
		"commitHash":          commitHash,
		"buildTime":           buildTime,
		"instagramConfigured": igClient != nil,
	})
}
