// Package main provides a Lambda entry point for description generation (DDR-053).
//
// This Lambda handles AI-powered Instagram caption generation:
//   - description: Generate a caption from media thumbnails
//   - description-feedback: Regenerate a caption with user feedback
//
// Invoked asynchronously by the API Lambda via lambda:Invoke (Event type).
//
// Container: Light (Dockerfile.light — no ffmpeg needed)
// Memory: 2 GB
// Timeout: 5 minutes
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/rs/zerolog/log"

	"github.com/fpang/gemini-media-cli/internal/lambdaboot"
	"github.com/fpang/gemini-media-cli/internal/logging"
	"github.com/fpang/gemini-media-cli/internal/store"
)

var coldStart = true

var (
	s3Client     *s3.Client
	mediaBucket  string
	sessionStore *store.DynamoStore
)

func init() {
	initStart := time.Now()
	logging.Init()

	aws := lambdaboot.InitAWS()
	s3s := lambdaboot.InitS3(aws.Config, "MEDIA_BUCKET_NAME")
	s3Client = s3s.Client
	mediaBucket = s3s.Bucket
	sessionStore = lambdaboot.InitDynamo(aws.Config, "DYNAMO_TABLE_NAME")
	lambdaboot.LoadGeminiKey(aws.SSM)

	lambdaboot.StartupLog("description-lambda", initStart).
		S3Bucket("mediaBucket", mediaBucket).
		DynamoTable("sessions", os.Getenv("DYNAMO_TABLE_NAME")).
		SSMParam("geminiApiKey", logging.EnvOrDefault("SSM_API_KEY_PARAM", "/ai-social-media/prod/gemini-api-key")).
		Log()
}

func main() {
	lambda.Start(handler)
}

func handler(ctx context.Context, event DescriptionEvent) error {
	if coldStart {
		coldStart = false
		log.Info().Str("function", "description-lambda").Msg("Cold start — first invocation")
	}
	log.Info().
		Str("type", event.Type).
		Str("sessionId", event.SessionID).
		Str("jobId", event.JobID).
		Msg("Description Lambda invoked")

	switch event.Type {
	case "description":
		return handleDescription(ctx, event)
	case "description-feedback":
		return handleDescriptionFeedback(ctx, event)
	default:
		return fmt.Errorf("unknown event type: %s", event.Type)
	}
}
