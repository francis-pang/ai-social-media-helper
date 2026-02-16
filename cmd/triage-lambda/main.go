// Package main provides a Lambda entry point for the triage pipeline (DDR-053).
//
// This Lambda handles the steps of the Triage Pipeline Step Function (DDR-061):
//   - triage-init-session: Write session record with expectedFileCount (DDR-061)
//   - triage-prepare: List S3 objects, write file results, and set counts (start flow)
//   - triage-check-processing: Poll processedCount vs expectedFileCount (DDR-061)
//   - triage-run: Read file manifest from DDB, call AskMediaTriage (DDR-061)
//
// Container: Light (Dockerfile.light — no ffmpeg needed)
// Memory: 2 GB
// Timeout: 10 minutes
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/rs/zerolog/log"

	"github.com/fpang/gemini-media-cli/internal/chat"
	"github.com/fpang/gemini-media-cli/internal/filehandler"
	"github.com/fpang/gemini-media-cli/internal/lambdaboot"
	"github.com/fpang/gemini-media-cli/internal/logging"
	"github.com/fpang/gemini-media-cli/internal/store"
)

var coldStart = true

// maxPresignedURLVideoBytes is the largest video file that can be referenced
// via an S3 presigned URL in the Gemini FileData.FileURI field. The Gemini API
// returns INVALID_ARGUMENT for HTTPS-URL-referenced files above ~15 MiB.
// We use 10 MiB as a conservative threshold to leave headroom.
// Videos exceeding this limit are uploaded to the Gemini Files API instead.
const maxPresignedURLVideoBytes int64 = 10 * 1024 * 1024 // 10 MiB

// AWS clients initialized at cold start.
var (
	s3Client         *s3.Client
	presignClient    *s3.PresignClient
	mediaBucket      string
	sessionStore     *store.DynamoStore
	fileProcessStore *store.FileProcessingStore
)

func init() {
	initStart := time.Now()
	logging.Init()

	aws := lambdaboot.InitAWS()
	s3s := lambdaboot.InitS3(aws.Config, "MEDIA_BUCKET_NAME")
	s3Client = s3s.Client
	presignClient = s3s.Presigner
	mediaBucket = s3s.Bucket
	sessionStore = lambdaboot.InitDynamo(aws.Config, "DYNAMO_TABLE_NAME")
	lambdaboot.LoadGeminiKey(aws.SSM)

	fpTableName := os.Getenv("FILE_PROCESSING_TABLE_NAME")
	if fpTableName != "" {
		fileProcessStore = store.NewFileProcessingStore(sessionStore.Client(), fpTableName)
	}

	lambdaboot.StartupLog("triage-lambda", initStart).
		S3Bucket("mediaBucket", mediaBucket).
		DynamoTable("sessions", os.Getenv("DYNAMO_TABLE_NAME")).
		SSMParam("geminiApiKey", logging.EnvOrDefault("SSM_API_KEY_PARAM", "/ai-social-media/prod/gemini-api-key")).
		Log()
}

func main() {
	lambda.Start(handler)
}

func handler(ctx context.Context, event TriageEvent) (interface{}, error) {
	if coldStart {
		coldStart = false
		log.Info().Str("function", "triage-lambda").Msg("Cold start — first invocation")
	}
	log.Info().
		Str("type", event.Type).
		Str("sessionId", event.SessionID).
		Str("jobId", event.JobID).
		Msg("Triage Lambda invoked")

	switch event.Type {
	case "triage-init-session":
		return handleTriageInitSession(ctx, event)
	case "triage-prepare":
		return handleTriagePrepare(ctx, event)
	case "triage-check-processing":
		return handleTriageCheckProcessing(ctx, event)
	case "triage-run":
		return nil, handleTriageRun(ctx, event)
	default:
		return nil, fmt.Errorf("unknown event type: %s", event.Type)
	}
}

// handleTriageInitSession writes the session record with expectedFileCount
// and sets the phase to "uploading". (DDR-061)
func handleTriageInitSession(ctx context.Context, event TriageEvent) (*TriageInitResult, error) {
	model := event.Model
	if model == "" {
		model = chat.DefaultModelName
	}

	sessionStore.PutTriageJob(ctx, event.SessionID, &store.TriageJob{
		ID:                event.JobID,
		Status:            "processing",
		Phase:             "uploading",
		ExpectedFileCount: event.ExpectedFileCount,
	})

	log.Info().
		Str("sessionId", event.SessionID).
		Str("jobId", event.JobID).
		Int("expectedFileCount", event.ExpectedFileCount).
		Msg("Triage session initialized (DDR-061)")

	return &TriageInitResult{
		SessionID: event.SessionID,
		JobID:     event.JobID,
		Model:     model,
	}, nil
}

// handleTriagePrepare lists S3 objects already uploaded for the session, writes
// FileResult entries, and sets expectedFileCount = processedCount so the pipeline
// can immediately proceed to the triage-run step. Used by POST /api/triage/start
// when files were uploaded before the triage pipeline was started.
func handleTriagePrepare(ctx context.Context, event TriageEvent) (*TriageInitResult, error) {
	model := event.Model
	if model == "" {
		model = chat.DefaultModelName
	}

	prefix := event.SessionID + "/"
	input := &s3.ListObjectsV2Input{
		Bucket: &mediaBucket,
		Prefix: &prefix,
	}

	result, err := s3Client.ListObjectsV2(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("list S3 objects for session %s: %w", event.SessionID, err)
	}

	var mediaCount int
	for _, obj := range result.Contents {
		key := *obj.Key
		// Skip subdirectories (thumbnails/, compressed/) and non-media files
		relPath := strings.TrimPrefix(key, prefix)
		if strings.Contains(relPath, "/") {
			continue
		}
		ext := strings.ToLower(filepath.Ext(relPath))
		if !filehandler.IsSupported(ext) {
			continue
		}

		mimeType, _ := filehandler.GetMIMEType(ext)
		fileType := "image"
		if filehandler.IsVideo(ext) {
			fileType = "video"
		}

		// Write FileResult entry so triage-run can read the manifest
		if fileProcessStore != nil {
			fr := &store.FileResult{
				Filename:    relPath,
				Status:      "valid",
				OriginalKey: key,
				FileType:    fileType,
				MimeType:    mimeType,
				FileSize:    *obj.Size,
			}
			if err := fileProcessStore.PutFileResult(ctx, event.SessionID, event.JobID, fr); err != nil {
				log.Warn().Err(err).Str("key", key).Msg("Failed to write FileResult during prepare")
			}
		}
		mediaCount++
	}

	if mediaCount == 0 {
		return nil, fmt.Errorf("no media files found under s3://%s/%s", mediaBucket, prefix)
	}

	// Set both counts equal so check-processing immediately sees allProcessed=true
	sessionStore.PutTriageJob(ctx, event.SessionID, &store.TriageJob{
		ID:                event.JobID,
		Status:            "processing",
		Phase:             "analyzing",
		ExpectedFileCount: mediaCount,
		ProcessedCount:    mediaCount,
		TotalFiles:        mediaCount,
		UploadedFiles:     mediaCount,
	})

	log.Info().
		Str("sessionId", event.SessionID).
		Str("jobId", event.JobID).
		Int("mediaCount", mediaCount).
		Msg("Triage prepare: listed S3 objects and wrote file results")

	return &TriageInitResult{
		SessionID: event.SessionID,
		JobID:     event.JobID,
		Model:     model,
	}, nil
}

// handleTriageCheckProcessing reads processedCount and expectedFileCount from DDB
// and returns whether all files are processed. (DDR-061)
func handleTriageCheckProcessing(ctx context.Context, event TriageEvent) (*TriageCheckProcessingResult, error) {
	job, err := sessionStore.GetTriageJob(ctx, event.SessionID, event.JobID)
	if err != nil || job == nil {
		return nil, fmt.Errorf("failed to read triage job: %v", err)
	}

	processedCount := job.ProcessedCount
	expectedCount := job.ExpectedFileCount
	allProcessed := expectedCount > 0 && processedCount >= expectedCount

	// Count errors from file processing table
	errorCount := 0
	if fileProcessStore != nil {
		results, err := fileProcessStore.GetFileResults(ctx, event.SessionID, event.JobID)
		if err == nil {
			for _, r := range results {
				if r.Status == "invalid" || r.Status == "error" {
					errorCount++
				}
			}
		}
	}

	// Update phase based on progress
	phase := "uploading"
	if processedCount > 0 {
		phase = "processing"
	}
	if allProcessed {
		phase = "analyzing"
	}
	sessionStore.PutTriageJob(ctx, event.SessionID, &store.TriageJob{
		ID:                event.JobID,
		Status:            "processing",
		Phase:             phase,
		ProcessedCount:    processedCount,
		ExpectedFileCount: expectedCount,
		TotalFiles:        expectedCount,
		UploadedFiles:     processedCount,
	})

	log.Info().
		Bool("allProcessed", allProcessed).
		Int("processedCount", processedCount).
		Int("expectedCount", expectedCount).
		Int("errorCount", errorCount).
		Str("sessionId", event.SessionID).
		Msg("Processing status check (DDR-061)")

	return &TriageCheckProcessingResult{
		SessionID:      event.SessionID,
		JobID:          event.JobID,
		Model:          event.Model,
		AllProcessed:   allProcessed,
		ProcessedCount: processedCount,
		ExpectedCount:  expectedCount,
		ErrorCount:     errorCount,
	}, nil
}
