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
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/rs/zerolog/log"

	"github.com/fpang/gemini-media-cli/internal/filehandler"
	"github.com/fpang/gemini-media-cli/internal/lambdaboot"
	"github.com/fpang/gemini-media-cli/internal/logging"
	"github.com/fpang/gemini-media-cli/internal/metrics"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/fpang/gemini-media-cli/internal/store"
)

var coldStart = true

// Size thresholds for image processing decisions.
const (
	maxSmallPhotoBytes = 2 * 1024 * 1024 // 2 MB
	maxSmallPhotoPx    = 2000             // 2000px
	targetResizePx     = 1600             // Resize large photos to 1600px
	thumbnailPx        = 400              // Thumbnail dimension
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
		key := record.S3.Object.Key
		if err := processFile(ctx, key); err != nil {
			log.Error().Err(err).Str("key", key).Msg("Failed to process file")
			// Don't return error — process remaining files in the batch
		}
	}
	return nil
}

func processFile(ctx context.Context, key string) error {
	fileStart := time.Now()

	// Parse sessionId from key: {sessionId}/{filename}
	parts := strings.SplitN(key, "/", 2)
	if len(parts) != 2 {
		log.Debug().Str("key", key).Msg("Skipping key: not in {sessionId}/{filename} format")
		return nil
	}
	sessionID := parts[0]
	remainder := parts[1]

	// Filter: skip our own output directories
	if strings.Contains(remainder, "/") {
		// Key has subdirectory: thumbnails/, processed/, compressed/
		log.Debug().Str("key", key).Msg("Skipping key: subdirectory (generated artifact)")
		return nil
	}
	filename := remainder

	log.Info().Str("key", key).Str("sessionId", sessionID).Str("filename", filename).Msg("Processing file")

	// Validate extension
	ext := strings.ToLower(filepath.Ext(filename))
	if !filehandler.IsSupported(ext) {
		log.Warn().Str("key", key).Str("ext", ext).Msg("Unsupported file extension")
		return writeErrorResult(ctx, sessionID, filename, key, fmt.Sprintf("Unsupported file extension: %s", ext))
	}

	isImage := filehandler.IsImage(ext)
	isVideo := filehandler.IsVideo(ext)
	fileType := "image"
	if isVideo {
		fileType = "video"
	}

	// Head object to get size and content type
	headResult, err := s3Client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: &mediaBucket,
		Key:    &key,
	})
	if err != nil {
		return writeErrorResult(ctx, sessionID, filename, key, fmt.Sprintf("Failed to read file metadata: %v", err))
	}

	fileSize := *headResult.ContentLength
	contentType := ""
	if headResult.ContentType != nil {
		contentType = *headResult.ContentType
	}
	mimeType, _ := filehandler.GetMIMEType(ext)
	if mimeType == "" {
		mimeType = contentType
	}

	log.Debug().Str("key", key).Int64("size", fileSize).Str("contentType", contentType).Str("mimeType", mimeType).Msg("File metadata retrieved")

	// Download file to /tmp
	tmpDir := filepath.Join(os.TempDir(), "media-process", sessionID)
	os.MkdirAll(tmpDir, 0755)
	defer os.RemoveAll(tmpDir)

	localPath := filepath.Join(tmpDir, filename)
	if err := downloadToFile(ctx, key, localPath); err != nil {
		return writeErrorResult(ctx, sessionID, filename, key, fmt.Sprintf("Failed to download file: %v", err))
	}

	// Load media file (extracts metadata)
	mf, err := filehandler.LoadMediaFile(localPath)
	if err != nil {
		return writeErrorResult(ctx, sessionID, filename, key, fmt.Sprintf("Failed to load media file: %v", err))
	}

	// Extract metadata as string map for DDB storage
	metadataMap := make(map[string]string)
	if mf.Metadata != nil {
		metadataMap["mediaType"] = mf.Metadata.GetMediaType()
		if mf.Metadata.HasGPSData() {
			lat, lon := mf.Metadata.GetGPS()
			metadataMap["gpsLat"] = fmt.Sprintf("%.6f", lat)
			metadataMap["gpsLon"] = fmt.Sprintf("%.6f", lon)
		}
		if mf.Metadata.HasDateData() {
			metadataMap["date"] = mf.Metadata.GetDate().Format(time.RFC3339)
		}
	}

	// Determine processing strategy
	var processedKey string
	var thumbnailKey string
	converted := false

	if isImage {
		// Generate thumbnail (always)
		thumbData, _, err := filehandler.GenerateThumbnail(mf, thumbnailPx)
		if err != nil {
			log.Warn().Err(err).Str("key", key).Msg("Failed to generate thumbnail")
		} else {
			baseName := strings.TrimSuffix(filename, ext)
			thumbnailKey = fmt.Sprintf("%s/thumbnails/%s.jpg", sessionID, baseName)
			thumbContentType := "image/jpeg"
			_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
				Bucket:      &mediaBucket,
				Key:         &thumbnailKey,
				Body:        bytes.NewReader(thumbData),
				ContentType: &thumbContentType,
			})
			if err != nil {
				log.Warn().Err(err).Str("thumbnailKey", thumbnailKey).Msg("Failed to upload thumbnail")
				thumbnailKey = ""
			} else {
				log.Debug().Str("thumbnailKey", thumbnailKey).Int("size", len(thumbData)).Msg("Thumbnail uploaded")
			}
		}

		// Small photo: skip conversion, use original
		processedKey = key
		// Note: Image conversion (resize large photos) can be added later
		// For now, all images use the original and just get a thumbnail

	} else if isVideo {
		// Generate video thumbnail
		thumbData, _, err := filehandler.GenerateThumbnail(mf, thumbnailPx)
		if err != nil {
			log.Warn().Err(err).Str("key", key).Msg("Failed to generate video thumbnail")
		} else {
			baseName := strings.TrimSuffix(filename, ext)
			thumbnailKey = fmt.Sprintf("%s/thumbnails/%s.jpg", sessionID, baseName)
			thumbContentType := "image/jpeg"
			_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
				Bucket:      &mediaBucket,
				Key:         &thumbnailKey,
				Body:        bytes.NewReader(thumbData),
				ContentType: &thumbContentType,
			})
			if err != nil {
				log.Warn().Err(err).Str("thumbnailKey", thumbnailKey).Msg("Failed to upload video thumbnail")
				thumbnailKey = ""
			}
		}

		// Video compression for Gemini (reuse existing CompressVideoForGemini)
		if filehandler.IsFFmpegAvailable() {
			var videoMeta *filehandler.VideoMetadata
			if mf.Metadata != nil {
				if vm, ok := mf.Metadata.(*filehandler.VideoMetadata); ok {
					videoMeta = vm
				}
			}
			compressedPath, _, cleanup, err := filehandler.CompressVideoForGemini(ctx, localPath, videoMeta)
			if err != nil {
				log.Warn().Err(err).Str("key", key).Msg("Video compression failed — using original")
				processedKey = key
			} else {
				defer cleanup()
				// Upload compressed video
				baseName := strings.TrimSuffix(filename, ext)
				processedKey = fmt.Sprintf("%s/processed/%s.webm", sessionID, baseName)
				compressedFile, err := os.Open(compressedPath)
				if err != nil {
					log.Warn().Err(err).Msg("Failed to open compressed video")
					processedKey = key
				} else {
					compressedContentType := "video/webm"
					_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
						Bucket:      &mediaBucket,
						Key:         &processedKey,
						Body:        compressedFile,
						ContentType: &compressedContentType,
					})
					compressedFile.Close()
					if err != nil {
						log.Warn().Err(err).Str("processedKey", processedKey).Msg("Failed to upload compressed video")
						processedKey = key
					} else {
						converted = true
						log.Info().Str("processedKey", processedKey).Msg("Compressed video uploaded")
					}
				}
				os.Remove(compressedPath)
			}
		} else {
			log.Debug().Str("key", key).Msg("ffmpeg not available — using original video")
			processedKey = key
		}
	}

	// Look up the jobId from the TriageJob in the sessions table.
	// The jobId is stored in the triage job record for this session.
	// We need to find it by querying for TRIAGE# sort key prefix.
	jobID, err := findTriageJobID(ctx, sessionID)
	if err != nil {
		log.Warn().Err(err).Str("sessionId", sessionID).Msg("Could not find triage job ID — file result will be orphaned")
		// Still write the result with empty jobID so it's not lost
		jobID = ""
	}

	// Write result to file-processing table
	result := &store.FileResult{
		Filename:     filename,
		Status:       "valid",
		OriginalKey:  key,
		ProcessedKey: processedKey,
		ThumbnailKey: thumbnailKey,
		FileType:     fileType,
		MimeType:     mimeType,
		FileSize:     fileSize,
		Converted:    converted,
		Metadata:     metadataMap,
	}

	if jobID != "" {
		if err := fileProcessStore.PutFileResult(ctx, sessionID, jobID, result); err != nil {
			log.Error().Err(err).Str("key", key).Msg("Failed to write file result to DDB")
		}

		// Increment processedCount on the TriageJob
		newCount, err := sessionStore.IncrementTriageProcessedCount(ctx, sessionID, jobID)
		if err != nil {
			log.Error().Err(err).Str("key", key).Msg("Failed to increment processedCount")
		} else {
			log.Debug().Str("key", key).Int("processedCount", newCount).Msg("processedCount incremented")
		}
	}

	processingMs := time.Since(fileStart).Milliseconds()
	log.Info().
		Str("key", key).
		Str("fileType", fileType).
		Bool("converted", converted).
		Int64("processingMs", processingMs).
		Msg("File processing complete")

	// Emit EMF metrics
	metrics.New("AiSocialMedia").
		Dimension("Operation", "mediaProcess").
		Dimension("FileType", fileType).
		Metric("FileProcessingMs", float64(processingMs), metrics.UnitMilliseconds).
		Metric("FileSize", float64(fileSize), metrics.UnitBytes).
		Count("FilesProcessed").
		Property("sessionId", sessionID).
		Property("filename", filename).
		Property("converted", converted).
		Flush()

	return nil
}

// findTriageJobID finds the triage job ID for a session by querying for TRIAGE# prefix.
func findTriageJobID(ctx context.Context, sessionID string) (string, error) {
	// Use a simple approach: the API Lambda writes the job ID, and we look it up
	items, err := sessionStore.QueryBySKPrefix(ctx, sessionID, "TRIAGE#")
	if err != nil {
		return "", fmt.Errorf("query triage jobs: %w", err)
	}
	if len(items) == 0 {
		return "", fmt.Errorf("no triage job found for session %s", sessionID)
	}

	// Use the most recent triage job (last in the list)
	lastItem := items[len(items)-1]
	if skAttr, ok := lastItem["SK"].(*types.AttributeValueMemberS); ok {
		return strings.TrimPrefix(skAttr.Value, "TRIAGE#"), nil
	}
	return "", fmt.Errorf("could not extract job ID from SK")
}

func writeErrorResult(ctx context.Context, sessionID, filename, originalKey, errMsg string) error {
	jobID, _ := findTriageJobID(ctx, sessionID)
	if jobID == "" {
		log.Warn().Str("sessionId", sessionID).Str("filename", filename).Str("error", errMsg).Msg("Cannot write error result — no triage job found")
		return nil
	}

	result := &store.FileResult{
		Filename:    filename,
		Status:      "invalid",
		OriginalKey: originalKey,
		Error:       errMsg,
	}
	if err := fileProcessStore.PutFileResult(ctx, sessionID, jobID, result); err != nil {
		log.Error().Err(err).Str("filename", filename).Msg("Failed to write error result to DDB")
	}

	// Still increment count so the SFN doesn't wait forever
	if _, err := sessionStore.IncrementTriageProcessedCount(ctx, sessionID, jobID); err != nil {
		log.Error().Err(err).Str("filename", filename).Msg("Failed to increment processedCount for error result")
	}

	return nil
}

func downloadToFile(ctx context.Context, key, localPath string) error {
	result, err := s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &mediaBucket, Key: &key,
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
			if readErr == io.EOF {
				break
			}
			return fmt.Errorf("download: %w", readErr)
		}
	}
	return nil
}
