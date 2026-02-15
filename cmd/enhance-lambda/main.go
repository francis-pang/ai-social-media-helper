// Package main provides a Lambda entry point for per-photo AI enhancement (DDR-053).
//
// This Lambda handles both initial enhancement and feedback-driven re-enhancement:
//   - Step Functions invocation: EnhancementPipeline Map state (one per photo)
//   - Async invocation: enhancement-feedback from the API Lambda
//
// Container: Light (Dockerfile.light — no ffmpeg needed for photo enhancement)
// Memory: 2 GB
// Timeout: 5 minutes
//
// See DDR-031: Multi-Step Photo Enhancement Pipeline
// See DDR-035: Multi-Lambda Deployment Architecture
// See DDR-043: Step Functions Lambda Entrypoints
// See DDR-053: Granular Lambda Split (absorbed enhancement-feedback)
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/rs/zerolog/log"

	"github.com/fpang/gemini-media-cli/internal/chat"
	"github.com/fpang/gemini-media-cli/internal/lambdaboot"
	"github.com/fpang/gemini-media-cli/internal/logging"
	"github.com/fpang/gemini-media-cli/internal/store"
)

// thumbnailMaxDimension is the max width/height for enhanced photo thumbnails.
const thumbnailMaxDimension = 400

// AWS clients and configuration initialized at cold start.
var (
	s3Client     *s3.Client
	sessionStore *store.DynamoStore
	mediaBucket  string
)

var coldStart = true

func init() {
	initStart := time.Now()
	logging.Init()

	aws := lambdaboot.InitAWS()
	s3s := lambdaboot.InitS3(aws.Config, "MEDIA_BUCKET_NAME")
	s3Client = s3s.Client
	mediaBucket = s3s.Bucket
	sessionStore = lambdaboot.InitDynamo(aws.Config, "DYNAMO_TABLE_NAME")
	lambdaboot.LoadGeminiKey(aws.SSM)

	lambdaboot.StartupLog("enhance-lambda", initStart).
		S3Bucket("mediaBucket", mediaBucket).
		DynamoTable("sessions", os.Getenv("DYNAMO_TABLE_NAME")).
		SSMParam("geminiApiKey", logging.EnvOrDefault("SSM_API_KEY_PARAM", "/ai-social-media/prod/gemini-api-key")).
		Log()
}

// rawHandler accepts raw JSON to route between enhancement and feedback handlers.
func rawHandler(ctx context.Context, raw json.RawMessage) (interface{}, error) {
	if coldStart {
		coldStart = false
		log.Info().Str("function", "enhance-lambda").Msg("Cold start — first invocation")
	}

	// Peek at the "type" field to route.
	var peek struct {
		Type string `json:"type"`
	}
	json.Unmarshal(raw, &peek)

	if peek.Type == "enhancement-feedback" {
		var event EnhanceEvent
		if err := json.Unmarshal(raw, &event); err != nil {
			return nil, fmt.Errorf("unmarshal feedback event: %w", err)
		}
		return nil, handleEnhancementFeedback(ctx, event)
	}

	// Default: Step Functions enhancement invocation.
	var event EnhanceEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		return nil, fmt.Errorf("unmarshal enhance event: %w", err)
	}
	return handleEnhance(ctx, event)
}

func main() {
	lambda.Start(rawHandler)
}

// updateItemError updates the enhancement item in DynamoDB with an error status.
// Best-effort — errors are logged but don't affect the Lambda response.
func updateItemError(ctx context.Context, event EnhanceEvent, errMsg string) {
	job, err := sessionStore.GetEnhancementJob(ctx, event.SessionID, event.JobID)
	if err != nil || job == nil {
		log.Warn().Err(err).Msg("Failed to get enhancement job for error update")
		return
	}

	if event.ItemIndex >= 0 && event.ItemIndex < len(job.Items) {
		job.Items[event.ItemIndex].Phase = chat.PhaseError
		job.Items[event.ItemIndex].Error = errMsg
		job.CompletedCount++
		if job.CompletedCount >= job.TotalCount {
			job.Status = "complete"
		}
		if err := sessionStore.PutEnhancementJob(ctx, event.SessionID, job); err != nil {
			log.Warn().Err(err).Msg("Failed to update enhancement job with error")
		}
	}
}
