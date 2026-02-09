// Package main provides a Lambda entry point for the Instagram webhook
// handler (DDR-044).
//
// This is a lightweight Lambda (128 MB, 10s timeout) that handles:
//   - GET /webhook — Meta verification handshake
//   - POST /webhook — Meta event notifications with HMAC-SHA256 validation
//
// Credentials are loaded from SSM Parameter Store at cold start:
//   - /ai-social-media/prod/instagram-webhook-verify-token
//   - /ai-social-media/prod/instagram-app-secret
//
// This Lambda has no access to S3, DynamoDB, or Gemini — it only reads
// SSM parameters and logs event payloads for future processing.
package main

import (
	"context"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"

	"github.com/fpang/gemini-media-cli/internal/logging"
	"github.com/fpang/gemini-media-cli/internal/webhook"
	"github.com/rs/zerolog/log"
)

var webhookHandler *webhook.Handler
var coldStart = true

func init() {
	initStart := time.Now()
	logging.Init()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load AWS config")
	}
	log.Debug().Str("region", cfg.Region).Msg("AWS config loaded")

	ssmClient := ssm.NewFromConfig(cfg)

	var ssmStart time.Time
	// Load webhook verify token from SSM.
	verifyToken := os.Getenv("WEBHOOK_VERIFY_TOKEN")
	if verifyToken == "" {
		paramName := os.Getenv("SSM_WEBHOOK_VERIFY_TOKEN_PARAM")
		if paramName == "" {
			paramName = "/ai-social-media/prod/instagram-webhook-verify-token"
		}
		ssmStart = time.Now()
		result, err := ssmClient.GetParameter(context.Background(), &ssm.GetParameterInput{
			Name:           &paramName,
			WithDecryption: aws.Bool(true),
		})
		if err != nil {
			log.Fatal().Err(err).Str("param", paramName).Msg("Failed to read webhook verify token from SSM")
		}
		verifyToken = *result.Parameter.Value
		log.Debug().Dur("elapsed", time.Since(ssmStart)).Msg("Webhook verify token loaded from SSM")
	}

	// Load app secret from SSM.
	appSecret := os.Getenv("INSTAGRAM_APP_SECRET")
	if appSecret == "" {
		paramName := os.Getenv("SSM_APP_SECRET_PARAM")
		if paramName == "" {
			paramName = "/ai-social-media/prod/instagram-app-secret"
		}
		ssmStart = time.Now()
		result, err := ssmClient.GetParameter(context.Background(), &ssm.GetParameterInput{
			Name:           &paramName,
			WithDecryption: aws.Bool(true),
		})
		if err != nil {
			log.Fatal().Err(err).Str("param", paramName).Msg("Failed to read app secret from SSM")
		}
		appSecret = *result.Parameter.Value
		log.Debug().Dur("elapsed", time.Since(ssmStart)).Msg("Instagram app secret loaded from SSM")
	}

	webhookHandler = webhook.NewHandler(verifyToken, appSecret)
	log.Info().
		Str("function", "webhook-lambda").
		Str("goVersion", runtime.Version()).
		Str("region", cfg.Region).
		Dur("initDuration", time.Since(initStart)).
		Msg("Webhook Lambda init complete")
}

func main() {
	mux := http.NewServeMux()
	mux.Handle("/webhook", webhookHandler)

	adapter := httpadapter.NewV2(mux)
	lambda.Start(adapter.ProxyWithContext)
}
