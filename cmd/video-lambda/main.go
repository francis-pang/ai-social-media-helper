// Package main provides a Lambda entry point for per-video enhancement.
//
// This Lambda is invoked by the Step Functions EnhancementPipeline Map state —
// one invocation per video. It downloads a single video from S3, runs the
// frame-based video enhancement pipeline (Gemini analysis + ffmpeg processing),
// uploads the enhanced version, and updates the enhancement job in DynamoDB.
//
// Container: Heavy (Dockerfile.heavy — includes ffmpeg for video processing)
// Memory: 4 GB
// Timeout: 15 minutes
//
// See DDR-032: Multi-Step Video Enhancement Pipeline
// See DDR-035: Multi-Lambda Deployment Architecture
// See DDR-043: Step Functions Lambda Entrypoints
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/fpang/gemini-media-cli/internal/chat"
	"github.com/fpang/gemini-media-cli/internal/filehandler"
	"github.com/fpang/gemini-media-cli/internal/logging"
	"github.com/fpang/gemini-media-cli/internal/store"
	"github.com/rs/zerolog/log"
)

// AWS clients and configuration initialized at cold start.
var (
	s3Client     *s3.Client
	sessionStore store.SessionStore
	mediaBucket  string
)

func init() {
	logging.Init()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load AWS config")
	}

	s3Client = s3.NewFromConfig(cfg)
	mediaBucket = os.Getenv("MEDIA_BUCKET_NAME")
	if mediaBucket == "" {
		log.Fatal().Msg("MEDIA_BUCKET_NAME environment variable is required")
	}

	// Initialize DynamoDB store.
	tableName := os.Getenv("DYNAMO_TABLE_NAME")
	if tableName == "" {
		tableName = "media-selection-sessions"
	}
	ddbClient := dynamodb.NewFromConfig(cfg)
	sessionStore = store.NewDynamoStore(ddbClient, tableName)

	// Load Gemini API key from SSM Parameter Store if not set.
	if os.Getenv("GEMINI_API_KEY") == "" {
		ssmClient := ssm.NewFromConfig(cfg)
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
}

// VideoEvent is the input payload from Step Functions.
// The Map state iterates over selected video keys and sends one event per video.
type VideoEvent struct {
	SessionID string `json:"sessionId"`
	JobID     string `json:"jobId"`
	Key       string `json:"key"`
	ItemIndex int    `json:"itemIndex"`
	Bucket    string `json:"bucket,omitempty"`
}

// VideoResult is the output returned to Step Functions.
type VideoResult struct {
	OriginalKey string `json:"originalKey"`
	EnhancedKey string `json:"enhancedKey"`
	Phase       string `json:"phase"`
	Summary     string `json:"summary,omitempty"`
	Error       string `json:"error,omitempty"`
}

func handler(ctx context.Context, event VideoEvent) (VideoResult, error) {
	bucket := mediaBucket
	if event.Bucket != "" {
		bucket = event.Bucket
	}

	logger := log.With().
		Str("sessionId", event.SessionID).
		Str("jobId", event.JobID).
		Str("key", event.Key).
		Int("itemIndex", event.ItemIndex).
		Logger()

	logger.Info().Msg("Starting video enhancement")

	// Validate input.
	if event.SessionID == "" || event.JobID == "" || event.Key == "" {
		return VideoResult{
			OriginalKey: event.Key,
			Error:       "sessionId, jobId, and key are required",
		}, fmt.Errorf("sessionId, jobId, and key are required")
	}

	// Download video from S3 to /tmp.
	tmpDir := filepath.Join(os.TempDir(), "video", event.SessionID)
	os.MkdirAll(tmpDir, 0755)
	defer os.RemoveAll(tmpDir)

	filename := filepath.Base(event.Key)
	inputPath := filepath.Join(tmpDir, filename)
	outputPath := filepath.Join(tmpDir, "enhanced-"+filename)

	if err := downloadToFile(ctx, bucket, event.Key, inputPath); err != nil {
		logger.Error().Err(err).Msg("Failed to download video")
		updateItemError(ctx, event, "download failed")
		return VideoResult{
			OriginalKey: event.Key,
			Phase:       "error",
			Error:       fmt.Sprintf("download failed: %v", err),
		}, err
	}

	// Extract video metadata for the enhancement pipeline.
	metadata, err := filehandler.ExtractVideoMetadata(inputPath)
	if err != nil {
		logger.Warn().Err(err).Msg("Failed to extract video metadata, using defaults")
		// Use basic defaults — enhancement can still proceed with limited metadata.
		metadata = &filehandler.VideoMetadata{
			FrameRate: 30.0,
			Duration:  0,
		}
	}

	// Build enhancement config from environment.
	config := chat.VideoEnhancementConfig{
		GeminiAPIKey:            os.Getenv("GEMINI_API_KEY"),
		VertexAIProject:         os.Getenv("VERTEX_AI_PROJECT"),
		VertexAIRegion:          os.Getenv("VERTEX_AI_REGION"),
		VertexAIAccessToken:     os.Getenv("VERTEX_AI_TOKEN"),
		SimilarityThreshold:     0.92,
		MaxAnalysisIterations:   3,
		TargetProfessionalScore: 8.5,
	}

	// Run video enhancement pipeline.
	result, err := chat.EnhanceVideo(ctx, inputPath, outputPath, metadata, config)
	if err != nil {
		logger.Error().Err(err).Msg("Video enhancement failed")
		updateItemError(ctx, event, err.Error())
		return VideoResult{
			OriginalKey: event.Key,
			Phase:       "error",
			Error:       fmt.Sprintf("enhancement failed: %v", err),
		}, err
	}

	// Upload enhanced video to S3.
	enhancedKey := fmt.Sprintf("%s/enhanced/%s", event.SessionID, filename)

	enhancedFile, err := os.Open(outputPath)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to open enhanced video")
		updateItemError(ctx, event, "failed to open enhanced video")
		return VideoResult{
			OriginalKey: event.Key,
			Phase:       "error",
			Error:       fmt.Sprintf("open failed: %v", err),
		}, err
	}
	defer enhancedFile.Close()

	// Determine content type from extension.
	ext := strings.ToLower(filepath.Ext(filename))
	contentType := "video/mp4"
	if ct, ok := filehandler.SupportedVideoExtensions[ext]; ok {
		contentType = ct
	}

	_, uploadErr := s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      &bucket,
		Key:         &enhancedKey,
		Body:        enhancedFile,
		ContentType: &contentType,
	})
	if uploadErr != nil {
		logger.Error().Err(uploadErr).Str("enhancedKey", enhancedKey).Msg("Failed to upload enhanced video")
		updateItemError(ctx, event, "upload failed")
		return VideoResult{
			OriginalKey: event.Key,
			Phase:       "error",
			Error:       fmt.Sprintf("upload failed: %v", uploadErr),
		}, uploadErr
	}

	// Update DynamoDB with completion status.
	updateVideoComplete(ctx, event, enhancedKey, result)

	logger.Info().
		Str("enhancedKey", enhancedKey).
		Int("groups", result.TotalGroups).
		Int("frames", result.TotalFrames).
		Dur("duration", result.TotalDuration).
		Msg("Video enhancement complete")

	return VideoResult{
		OriginalKey: event.Key,
		EnhancedKey: enhancedKey,
		Phase:       "complete",
		Summary:     result.EnhancementSummary,
	}, nil
}

func main() {
	lambda.Start(handler)
}

// --- DynamoDB Helpers ---

// updateItemError updates the enhancement item in DynamoDB with an error status.
func updateItemError(ctx context.Context, event VideoEvent, errMsg string) {
	job, err := sessionStore.GetEnhancementJob(ctx, event.SessionID, event.JobID)
	if err != nil || job == nil {
		log.Warn().Err(err).Msg("Failed to get enhancement job for error update")
		return
	}

	if event.ItemIndex >= 0 && event.ItemIndex < len(job.Items) {
		job.Items[event.ItemIndex].Phase = "error"
		job.Items[event.ItemIndex].Error = errMsg
		job.CompletedCount++
		if err := sessionStore.PutEnhancementJob(ctx, event.SessionID, job); err != nil {
			log.Warn().Err(err).Msg("Failed to update enhancement job with error")
		}
	}
}

// updateVideoComplete updates the enhancement item in DynamoDB with success results.
func updateVideoComplete(ctx context.Context, event VideoEvent, enhancedKey string, result *chat.VideoEnhancementResult) {
	job, err := sessionStore.GetEnhancementJob(ctx, event.SessionID, event.JobID)
	if err != nil || job == nil {
		log.Warn().Err(err).Msg("Failed to get enhancement job for completion update")
		return
	}

	if event.ItemIndex >= 0 && event.ItemIndex < len(job.Items) {
		job.Items[event.ItemIndex].Phase = "complete"
		job.Items[event.ItemIndex].EnhancedKey = enhancedKey
		job.Items[event.ItemIndex].OriginalThumbKey = fmt.Sprintf("%s/thumbnails/%s.jpg", event.SessionID,
			strings.TrimSuffix(filepath.Base(event.Key), filepath.Ext(event.Key)))
		if result != nil {
			job.Items[event.ItemIndex].Phase1Text = result.EnhancementSummary
		}
		job.CompletedCount++
		if err := sessionStore.PutEnhancementJob(ctx, event.SessionID, job); err != nil {
			log.Warn().Err(err).Msg("Failed to update enhancement job with completion")
		}
	}
}

// --- S3 Helpers ---

// downloadToFile downloads an S3 object to a specific local path.
func downloadToFile(ctx context.Context, bucket, key, localPath string) error {
	result, err := s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		return fmt.Errorf("S3 GetObject: %w", err)
	}
	defer result.Body.Close()

	f, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, result.Body); err != nil {
		return fmt.Errorf("download: %w", err)
	}
	return nil
}
