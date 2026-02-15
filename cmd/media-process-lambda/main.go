// Package main provides a Lambda entry point for per-file media processing (DDR-061).
//
// This Lambda is triggered by S3 ObjectCreated events on the media bucket.
// For each uploaded file, it:
//
//  1. Validates the file extension and MIME type
//  2. Extracts metadata (EXIF for images, ffprobe for videos)
//  3. Converts if needed (resize large photos, compress videos)
//  4. Generates a thumbnail
//  5. Writes the result to the file-processing DynamoDB table
//  6. Increments the processedCount on the session's TriageJob
//
// Container: Heavy (Dockerfile.heavy — ffmpeg needed for video processing)
// Memory: 1 GB
// Timeout: 5 minutes
package main

import (
	"context"
	"net/url"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/rs/zerolog/log"

	"github.com/fpang/gemini-media-cli/internal/lambdaboot"
	"github.com/fpang/gemini-media-cli/internal/logging"
	"github.com/fpang/gemini-media-cli/internal/store"
)

var coldStart = true

// Size thresholds for image processing decisions.
const (
	maxSmallPhotoBytes = 2 * 1024 * 1024 // 2 MB
	maxSmallPhotoPx    = 2000            // 2000px
	targetResizePx     = 1600            // Resize large photos to 1600px
	thumbnailPx        = 400             // Thumbnail dimension
)

// AWS clients initialized at cold start.
var (
	s3Client         *s3.Client
	mediaBucket      string
	sessionStore     *store.DynamoStore
	fileProcessStore *store.FileProcessingStore
)

func init() {
	initStart := time.Now()
	logging.Init()

	awsClients := lambdaboot.InitAWS()
	s3s := lambdaboot.InitS3(awsClients.Config, "MEDIA_BUCKET_NAME")
	s3Client = s3s.Client
	mediaBucket = s3s.Bucket
	sessionStore = lambdaboot.InitDynamo(awsClients.Config, "DYNAMO_TABLE_NAME")

	// Initialize file processing store (DDR-061)
	fpTableName := os.Getenv("FILE_PROCESSING_TABLE_NAME")
	if fpTableName == "" {
		log.Fatal().Msg("FILE_PROCESSING_TABLE_NAME environment variable is required")
	}
	ddbClient := sessionStore.Client()
	fileProcessStore = store.NewFileProcessingStore(ddbClient, fpTableName)

	lambdaboot.StartupLog("media-process-lambda", initStart).
		S3Bucket("mediaBucket", mediaBucket).
		DynamoTable("sessions", os.Getenv("DYNAMO_TABLE_NAME")).
		DynamoTable("fileProcessing", fpTableName).
		Log()
}

func main() {
	lambda.Start(handler)
}

func handler(ctx context.Context, s3Event events.S3Event) error {
	if coldStart {
		coldStart = false
		log.Info().Str("function", "media-process-lambda").Msg("Cold start — first invocation")
	}

	for _, record := range s3Event.Records {
		// S3 event notifications URL-encode object keys (spaces → "+",
		// special chars → "%XX"). Decode so S3 API calls use the real key.
		key, err := url.QueryUnescape(record.S3.Object.Key)
		if err != nil {
			log.Error().Err(err).Str("rawKey", record.S3.Object.Key).Msg("Failed to URL-decode S3 event key")
			key = record.S3.Object.Key
		}
		if err := processFile(ctx, key); err != nil {
			log.Error().Err(err).Str("key", key).Msg("Failed to process file")
			// Don't return error — process remaining files in the batch
		}
	}
	return nil
}
