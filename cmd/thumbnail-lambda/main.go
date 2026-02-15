// Package main provides a Lambda entry point for per-file thumbnail generation.
//
// This Lambda is invoked by the Step Functions SelectionPipeline Map state —
// one invocation per media file. It downloads a single media file from S3,
// generates a 400px JPEG thumbnail (images via pure Go, videos via ffmpeg),
// and uploads the thumbnail to S3.
//
// Container: Heavy (Dockerfile.heavy — includes ffmpeg for video frame extraction)
// Memory: 512 MB
// Timeout: 2 minutes
//
// See DDR-035: Multi-Lambda Deployment Architecture
// See DDR-043: Step Functions Lambda Entrypoints
package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/fpang/gemini-media-cli/internal/filehandler"
	"github.com/fpang/gemini-media-cli/internal/logging"
	"github.com/fpang/gemini-media-cli/internal/s3util"
	"github.com/rs/zerolog/log"
)

// thumbnailMaxDimension is the max width/height for cached thumbnails.
// 400px balances file size (~30KB) with UI display quality.
const thumbnailMaxDimension = 400

// AWS clients initialized at cold start.
var (
	s3Client    *s3.Client
	mediaBucket string
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
	mediaBucket = os.Getenv("MEDIA_BUCKET_NAME")
	if mediaBucket == "" {
		log.Fatal().Msg("MEDIA_BUCKET_NAME environment variable is required")
	}

	// Emit consolidated cold-start log for troubleshooting.
	logging.NewStartupLogger("thumbnail-lambda").
		InitDuration(time.Since(initStart)).
		S3Bucket("mediaBucket", mediaBucket).
		Log()
}

// ThumbnailEvent is the input payload from Step Functions.
// The Map state iterates over media keys and sends one event per file.
type ThumbnailEvent struct {
	SessionID string `json:"sessionId"`
	Key       string `json:"key"`
	Bucket    string `json:"bucket,omitempty"` // Optional override; defaults to MEDIA_BUCKET_NAME.
}

// ThumbnailResult is the output returned to Step Functions.
// The Map state collects all results for the next state (Selection Lambda).
type ThumbnailResult struct {
	ThumbnailKey string `json:"thumbnailKey"`
	OriginalKey  string `json:"originalKey"`
	Success      bool   `json:"success"`
	Error        string `json:"error,omitempty"`
}

func handler(ctx context.Context, event ThumbnailEvent) (ThumbnailResult, error) {
	handlerStart := time.Now()
	if coldStart {
		coldStart = false
		log.Info().Str("function", "thumbnail-lambda").Msg("Cold start — first invocation")
	}

	bucket := mediaBucket
	if event.Bucket != "" {
		bucket = event.Bucket
	}

	logger := log.With().
		Str("sessionId", event.SessionID).
		Str("key", event.Key).
		Logger()

	logger.Info().Msg("Processing thumbnail request")

	// Validate input.
	if event.SessionID == "" || event.Key == "" {
		return ThumbnailResult{
			OriginalKey: event.Key,
			Success:     false,
			Error:       "sessionId and key are required",
		}, fmt.Errorf("sessionId and key are required")
	}

	filename := filepath.Base(event.Key)
	ext := strings.ToLower(filepath.Ext(filename))

	if !filehandler.IsSupported(ext) {
		logger.Warn().Str("extension", ext).Msg("Unsupported file type rejected")
		return ThumbnailResult{
			OriginalKey: event.Key,
			Success:     false,
			Error:       fmt.Sprintf("unsupported file type: %s", ext),
		}, fmt.Errorf("unsupported file type: %s", ext)
	}

	// Download media file from S3 to /tmp.
	tmpPath := filepath.Join(os.TempDir(), "thumb-"+filename)
	if err := downloadToFile(ctx, bucket, event.Key, tmpPath); err != nil {
		logger.Error().Err(err).Msg("Failed to download media file")
		return ThumbnailResult{
			OriginalKey: event.Key,
			Success:     false,
			Error:       fmt.Sprintf("download failed: %v", err),
		}, err
	}
	defer os.Remove(tmpPath)

	// Log media file size after download
	if fileInfo, err := os.Stat(tmpPath); err == nil {
		logger.Debug().Int64("mediaFileSize", fileInfo.Size()).Msg("Media file downloaded")
	}

	// Load as MediaFile for thumbnail generation.
	mf, err := filehandler.LoadMediaFile(tmpPath)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to load media file")
		return ThumbnailResult{
			OriginalKey: event.Key,
			Success:     false,
			Error:       fmt.Sprintf("load failed: %v", err),
		}, err
	}

	// Generate thumbnail.
	thumbData, _, err := filehandler.GenerateThumbnail(mf, thumbnailMaxDimension)
	if err != nil {
		// Soft failure: return success=false but no function error.
		// This prevents the Step Functions Map from failing when ffmpeg
		// is unavailable for video thumbnails. The selection Lambda will
		// proceed without the thumbnail for this file.
		logger.Warn().Err(err).Msg("Thumbnail generation failed (soft failure — pipeline will continue)")
		return ThumbnailResult{
			OriginalKey: event.Key,
			Success:     false,
			Error:       fmt.Sprintf("thumbnail generation failed: %v", err),
		}, nil
	}
	logger.Debug().Int("thumbnailSize", len(thumbData)).Msg("Thumbnail generated")

	// Upload thumbnail to S3 at {sessionId}/thumbnails/{baseName}.jpg.
	baseName := strings.TrimSuffix(filename, filepath.Ext(filename))
	thumbKey := fmt.Sprintf("%s/thumbnails/%s.jpg", event.SessionID, baseName)
	contentType := "image/jpeg"

	_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      &bucket,
		Key:         &thumbKey,
		Body:        bytes.NewReader(thumbData),
		ContentType: &contentType,
		Tagging:     s3util.ProjectTagging(),
	})
	if err != nil {
		logger.Error().Err(err).Str("thumbKey", thumbKey).Msg("Failed to upload thumbnail")
		return ThumbnailResult{
			OriginalKey: event.Key,
			Success:     false,
			Error:       fmt.Sprintf("upload failed: %v", err),
		}, err
	}

	logger.Info().
		Str("thumbKey", thumbKey).
		Int("thumbSize", len(thumbData)).
		Dur("duration", time.Since(handlerStart)).
		Msg("Thumbnail generated and uploaded")

	return ThumbnailResult{
		ThumbnailKey: thumbKey,
		OriginalKey:  event.Key,
		Success:      true,
	}, nil
}

func main() {
	lambda.Start(handler)
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

	buf := make([]byte, 32*1024)
	for {
		n, readErr := result.Body.Read(buf)
		if n > 0 {
			if _, writeErr := f.Write(buf[:n]); writeErr != nil {
				return fmt.Errorf("write: %w", writeErr)
			}
		}
		if readErr != nil {
			if readErr.Error() == "EOF" {
				break
			}
			return fmt.Errorf("read: %w", readErr)
		}
	}
	return nil
}
