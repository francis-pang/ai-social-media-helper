// Package main provides a Lambda entry point for the triage pipeline (DDR-053).
//
// This Lambda handles the 3 steps of the Triage Pipeline Step Function (DDR-052):
//   - triage-prepare: Download files, upload videos to Gemini Files API
//   - triage-check-gemini: Poll Gemini Files API for video processing status
//   - triage-run: Call AskMediaTriage with all prepared media
//
// Container: Light (Dockerfile.light — no ffmpeg needed)
// Memory: 2 GB
// Timeout: 10 minutes
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
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/rs/zerolog/log"

	"google.golang.org/genai"

	"github.com/fpang/gemini-media-cli/internal/chat"
	"github.com/fpang/gemini-media-cli/internal/filehandler"
	"github.com/fpang/gemini-media-cli/internal/lambdaboot"
	"github.com/fpang/gemini-media-cli/internal/logging"
	"github.com/fpang/gemini-media-cli/internal/metrics"
	"github.com/fpang/gemini-media-cli/internal/store"
)

var coldStart = true

// AWS clients initialized at cold start.
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

	lambdaboot.StartupLog("triage-lambda", initStart).
		S3Bucket("mediaBucket", mediaBucket).
		DynamoTable("sessions", os.Getenv("DYNAMO_TABLE_NAME")).
		SSMParam("geminiApiKey", logging.EnvOrDefault("SSM_API_KEY_PARAM", "/ai-social-media/prod/gemini-api-key")).
		Log()
}

func main() {
	lambda.Start(handler)
}

// --- Event and Result types ---

// TriageEvent is the input from Step Functions.
type TriageEvent struct {
	Type           string   `json:"type"`
	SessionID      string   `json:"sessionId"`
	JobID          string   `json:"jobId"`
	Model          string   `json:"model,omitempty"`
	VideoFileNames []string `json:"videoFileNames,omitempty"`
}

// TriagePrepareResult is returned by the triage-prepare handler.
type TriagePrepareResult struct {
	SessionID      string   `json:"sessionId"`
	JobID          string   `json:"jobId"`
	Model          string   `json:"model"`
	HasVideos      bool     `json:"hasVideos"`
	VideoFileNames []string `json:"videoFileNames"`
}

// TriageCheckGeminiResult is returned by the triage-check-gemini handler.
type TriageCheckGeminiResult struct {
	SessionID      string   `json:"sessionId"`
	JobID          string   `json:"jobId"`
	Model          string   `json:"model"`
	AllActive      bool     `json:"allActive"`
	VideoFileNames []string `json:"videoFileNames"`
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
	case "triage-prepare":
		return handleTriagePrepare(ctx, event)
	case "triage-check-gemini":
		return handleTriageCheckGemini(ctx, event)
	case "triage-run":
		return nil, handleTriageRun(ctx, event)
	default:
		return nil, fmt.Errorf("unknown event type: %s", event.Type)
	}
}

// handleTriagePrepare downloads files from S3, loads metadata, uploads videos
// to the Gemini Files API, and returns file names for polling.
func handleTriagePrepare(ctx context.Context, event TriageEvent) (*TriagePrepareResult, error) {
	sessionStore.PutTriageJob(ctx, event.SessionID, &store.TriageJob{
		ID: event.JobID, Status: "processing", Phase: "uploading",
	})

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		setTriageError(ctx, event, "GEMINI_API_KEY not configured")
		return nil, fmt.Errorf("GEMINI_API_KEY not configured")
	}

	client, err := chat.NewGeminiClient(ctx, apiKey)
	if err != nil {
		setTriageError(ctx, event, fmt.Sprintf("Failed to create Gemini client: %v", err))
		return nil, fmt.Errorf("create Gemini client: %w", err)
	}

	// List S3 objects for the session.
	prefix := event.SessionID + "/"
	listResult, err := s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: &mediaBucket, Prefix: &prefix,
	})
	if err != nil {
		setTriageError(ctx, event, fmt.Sprintf("Failed to list S3 objects: %v", err))
		return nil, fmt.Errorf("list S3 objects: %w", err)
	}
	log.Debug().Int("objectCount", len(listResult.Contents)).Str("sessionId", event.SessionID).Msg("S3 ListObjectsV2 completed")
	if len(listResult.Contents) == 0 {
		setTriageError(ctx, event, "No files found for session")
		return nil, fmt.Errorf("no files found for session")
	}

	// Count supported files for progress tracking.
	var totalSupported int
	for _, obj := range listResult.Contents {
		ext := strings.ToLower(filepath.Ext(filepath.Base(*obj.Key)))
		if filehandler.IsSupported(ext) {
			totalSupported++
		}
	}

	// Update phase with total file count.
	sessionStore.PutTriageJob(ctx, event.SessionID, &store.TriageJob{
		ID: event.JobID, Status: "processing", Phase: "uploading",
		TotalFiles: totalSupported, UploadedFiles: 0,
	})

	tmpDir := filepath.Join(os.TempDir(), "triage", event.SessionID)
	os.MkdirAll(tmpDir, 0755)

	var videoFileNames []string
	uploadedCount := 0

	for _, obj := range listResult.Contents {
		key := *obj.Key
		filename := filepath.Base(key)
		ext := strings.ToLower(filepath.Ext(filename))

		if !filehandler.IsSupported(ext) {
			log.Debug().Str("key", key).Str("ext", ext).Msg("Skipping unsupported file")
			continue
		}

		localPath := filepath.Join(tmpDir, filename)
		if err := downloadToFile(ctx, key, localPath); err != nil {
			log.Warn().Err(err).Str("key", key).Msg("Failed to download file")
			continue
		}

		mf, err := filehandler.LoadMediaFile(localPath)
		if err != nil {
			log.Warn().Err(err).Str("key", key).Msg("Failed to load media file")
			continue
		}

		// Upload videos to Gemini Files API.
		if filehandler.IsVideo(strings.ToLower(filepath.Ext(mf.Path))) {
			f, err := os.Open(mf.Path)
			if err != nil {
				log.Warn().Err(err).Str("key", key).Msg("Failed to open video for Gemini upload")
				continue
			}
			file, err := client.Files.Upload(ctx, f, nil)
			f.Close()
			if err != nil {
				log.Warn().Err(err).Str("key", key).Msg("Failed to upload video to Gemini Files API")
				continue
			}
			videoFileNames = append(videoFileNames, file.Name)
			log.Debug().Str("fileName", file.Name).Str("key", key).Msg("Video uploaded to Gemini Files API")
		}

		uploadedCount++
		// Update upload progress in DynamoDB.
		sessionStore.PutTriageJob(ctx, event.SessionID, &store.TriageJob{
			ID: event.JobID, Status: "processing", Phase: "uploading",
			TotalFiles: totalSupported, UploadedFiles: uploadedCount,
		})

		log.Debug().Str("key", key).Str("mimeType", mf.MIMEType).Int64("size", mf.Size).Msg("Media file loaded")
	}

	model := event.Model
	if model == "" {
		model = chat.DefaultModelName
	}

	// Update phase: if videos exist, next step is Gemini processing; otherwise go straight to analyzing.
	nextPhase := "analyzing"
	if len(videoFileNames) > 0 {
		nextPhase = "gemini_processing"
	}
	sessionStore.PutTriageJob(ctx, event.SessionID, &store.TriageJob{
		ID: event.JobID, Status: "processing", Phase: nextPhase,
		TotalFiles: totalSupported, UploadedFiles: uploadedCount,
	})

	log.Info().Int("videoFileNames", len(videoFileNames)).Str("sessionId", event.SessionID).Msg("Triage prepare complete")

	return &TriagePrepareResult{
		SessionID:      event.SessionID,
		JobID:          event.JobID,
		Model:          model,
		HasVideos:      len(videoFileNames) > 0,
		VideoFileNames: videoFileNames,
	}, nil
}

// handleTriageCheckGemini polls the Gemini Files API to check if all uploaded
// videos have finished processing.
func handleTriageCheckGemini(ctx context.Context, event TriageEvent) (*TriageCheckGeminiResult, error) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY not configured")
	}

	client, err := chat.NewGeminiClient(ctx, apiKey)
	if err != nil {
		return nil, fmt.Errorf("create Gemini client: %w", err)
	}

	// Keep phase as gemini_processing while polling.
	sessionStore.PutTriageJob(ctx, event.SessionID, &store.TriageJob{
		ID: event.JobID, Status: "processing", Phase: "gemini_processing",
	})

	allActive := true
	for _, fileName := range event.VideoFileNames {
		file, err := client.Files.Get(ctx, fileName, nil)
		if err != nil {
			log.Warn().Err(err).Str("fileName", fileName).Msg("Failed to check Gemini file status")
			return nil, fmt.Errorf("check Gemini file %s: %w", fileName, err)
		}
		log.Debug().Str("fileName", fileName).Str("state", string(file.State)).Msg("Gemini file status")
		if file.State == genai.FileStateProcessing {
			allActive = false
		} else if file.State == genai.FileStateFailed {
			setTriageError(ctx, event, fmt.Sprintf("Gemini file processing failed: %s", fileName))
			return nil, fmt.Errorf("Gemini file processing failed: %s", fileName)
		}
	}

	log.Info().Bool("allActive", allActive).Int("videoCount", len(event.VideoFileNames)).Str("sessionId", event.SessionID).Msg("Gemini status check complete")

	return &TriageCheckGeminiResult{
		SessionID:      event.SessionID,
		JobID:          event.JobID,
		Model:          event.Model,
		AllActive:      allActive,
		VideoFileNames: event.VideoFileNames,
	}, nil
}

// handleTriageRun calls AskMediaTriage with all prepared media and writes
// results to DynamoDB.
func handleTriageRun(ctx context.Context, event TriageEvent) error {
	jobStart := time.Now()

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return setTriageError(ctx, event, "GEMINI_API_KEY not configured")
	}

	client, err := chat.NewGeminiClient(ctx, apiKey)
	if err != nil {
		return setTriageError(ctx, event, fmt.Sprintf("Failed to create Gemini client: %v", err))
	}

	// Re-download files from S3 (Standard Workflows run each step in separate invocations).
	prefix := event.SessionID + "/"
	listResult, err := s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: &mediaBucket, Prefix: &prefix,
	})
	if err != nil {
		return setTriageError(ctx, event, fmt.Sprintf("Failed to list S3 objects: %v", err))
	}

	tmpDir := filepath.Join(os.TempDir(), "triage", event.SessionID)
	os.MkdirAll(tmpDir, 0755)
	defer os.RemoveAll(tmpDir)

	var allMediaFiles []*filehandler.MediaFile
	var s3Keys []string
	pathToKeyMap := make(map[string]string) // Map local path -> S3 key

	for _, obj := range listResult.Contents {
		key := *obj.Key
		filename := filepath.Base(key)
		ext := strings.ToLower(filepath.Ext(filename))

		if !filehandler.IsSupported(ext) {
			continue
		}

		localPath := filepath.Join(tmpDir, filename)
		if err := downloadToFile(ctx, key, localPath); err != nil {
			log.Warn().Err(err).Str("key", key).Msg("Failed to download file")
			continue
		}

		mf, err := filehandler.LoadMediaFile(localPath)
		if err != nil {
			log.Warn().Err(err).Str("key", key).Msg("Failed to load media file")
			continue
		}

		allMediaFiles = append(allMediaFiles, mf)
		s3Keys = append(s3Keys, key)
		pathToKeyMap[localPath] = key
	}

	if len(allMediaFiles) == 0 {
		return setTriageError(ctx, event, "No supported media files found in the uploaded session")
	}

	model := event.Model
	if model == "" {
		model = chat.DefaultModelName
	}

	// Create key mapper function
	keyMapper := func(localPath string) string {
		return pathToKeyMap[localPath]
	}

	// Create compressed video store callback
	storeCompressed := func(ctx context.Context, sessionID, originalKey, compressedPath string) (string, error) {
		return uploadCompressedVideo(ctx, sessionID, originalKey, compressedPath)
	}

	// Update phase: sending query to Gemini and waiting for AI response.
	sessionStore.PutTriageJob(ctx, event.SessionID, &store.TriageJob{
		ID: event.JobID, Status: "processing", Phase: "analyzing",
		TotalFiles: len(allMediaFiles),
	})

	log.Debug().Int("fileCount", len(allMediaFiles)).Str("model", model).Msg("Calling AskMediaTriage")
	triageResults, err := chat.AskMediaTriage(ctx, client, allMediaFiles, model, event.SessionID, storeCompressed, keyMapper)
	if err != nil {
		return setTriageError(ctx, event, fmt.Sprintf("Triage failed: %v", err))
	}

	// --- DDR-059: Generate and store thumbnails for images ---
	// Generate thumbnails from temp files still on disk, upload to S3 at
	// {sessionId}/thumbnails/{baseName}.webp. This allows us to delete the
	// originals immediately after, since the review UI only needs thumbnails.
	thumbnailURLs := make(map[int]string) // media index -> thumbnail URL
	for i, mf := range allMediaFiles {
		ext := strings.ToLower(filepath.Ext(mf.Path))
		if !filehandler.IsImage(ext) {
			continue // Videos use placeholder SVG; no thumbnail needed
		}

		thumbData, _, err := filehandler.GenerateThumbnail(mf, 400)
		if err != nil {
			log.Warn().Err(err).Str("path", mf.Path).Msg("Failed to generate thumbnail — falling back to original key URL")
			continue
		}

		baseName := strings.TrimSuffix(filepath.Base(mf.Path), filepath.Ext(mf.Path))
		thumbKey := fmt.Sprintf("%s/thumbnails/%s.webp", event.SessionID, baseName)
		contentType := "image/webp"

		_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket:      &mediaBucket,
			Key:         &thumbKey,
			Body:        bytes.NewReader(thumbData),
			ContentType: &contentType,
		})
		if err != nil {
			log.Warn().Err(err).Str("thumbKey", thumbKey).Msg("Failed to upload thumbnail to S3 — falling back to original key URL")
			continue
		}

		thumbnailURLs[i] = fmt.Sprintf("/api/media/thumbnail?key=%s", thumbKey)
		log.Debug().Str("thumbKey", thumbKey).Int("size", len(thumbData)).Msg("Thumbnail uploaded to S3")
	}
	log.Info().Int("thumbnailsUploaded", len(thumbnailURLs)).Int("totalImages", len(allMediaFiles)).Msg("Thumbnail generation complete")

	// Map results to store items.
	var keep, discard []store.TriageItem
	seen := make(map[int]bool) // track which media indices got a verdict
	for _, tr := range triageResults {
		idx := tr.Media - 1
		if idx < 0 || idx >= len(allMediaFiles) {
			continue
		}
		seen[idx] = true
		key := s3Keys[idx]

		// Use pre-generated thumbnail URL for images, original key URL for videos (DDR-059).
		thumbURL := fmt.Sprintf("/api/media/thumbnail?key=%s", key)
		if url, ok := thumbnailURLs[idx]; ok {
			thumbURL = url
		}

		item := store.TriageItem{
			Media:        tr.Media,
			Filename:     tr.Filename,
			Key:          key,
			Saveable:     tr.Saveable,
			Reason:       tr.Reason,
			ThumbnailURL: thumbURL,
		}
		if tr.Saveable {
			keep = append(keep, item)
		} else {
			discard = append(discard, item)
		}
	}

	// Safety net: any media items missing from the AI response default to "keep"
	// so that nothing is silently lost.
	for i, mf := range allMediaFiles {
		if !seen[i] {
			key := s3Keys[i]
			log.Warn().
				Int("media", i+1).
				Str("filename", filepath.Base(mf.Path)).
				Msg("Media item missing from AI triage results — defaulting to keep")

			thumbURL := fmt.Sprintf("/api/media/thumbnail?key=%s", key)
			if url, ok := thumbnailURLs[i]; ok {
				thumbURL = url
			}

			keep = append(keep, store.TriageItem{
				Media:        i + 1,
				Filename:     filepath.Base(mf.Path),
				Key:          key,
				Saveable:     true,
				Reason:       "Not evaluated by AI — kept by default",
				ThumbnailURL: thumbURL,
			})
		}
	}

	sessionStore.PutTriageJob(ctx, event.SessionID, &store.TriageJob{
		ID: event.JobID, Status: "complete", Keep: keep, Discard: discard,
	})

	log.Info().Int("keep", len(keep)).Int("discard", len(discard)).Dur("duration", time.Since(jobStart)).Msg("Triage complete")

	// --- DDR-059: Delete original files from S3 ---
	// Now that thumbnails are stored and results are in DynamoDB, delete the
	// original files to free S3 storage. Exclude thumbnails/ and compressed/ prefixes.
	deleteOriginals(ctx, event.SessionID, s3Keys)

	metrics.New("AiSocialMedia").
		Dimension("JobType", "triage").
		Metric("JobDurationMs", float64(time.Since(jobStart).Milliseconds()), metrics.UnitMilliseconds).
		Metric("JobFilesProcessed", float64(len(allMediaFiles)), metrics.UnitCount).
		Count("JobSuccess").
		Property("jobId", event.JobID).
		Property("sessionId", event.SessionID).
		Flush()

	return nil
}

// --- Helpers ---

func setTriageError(ctx context.Context, event TriageEvent, msg string) error {
	log.Error().Str("job", event.JobID).Str("error", msg).Str("sessionId", event.SessionID).Msg("Triage job failed")
	sessionStore.PutTriageJob(ctx, event.SessionID, &store.TriageJob{
		ID: event.JobID, Status: "error", Error: msg,
	})
	return nil
}

// uploadCompressedVideo uploads a compressed video file to S3 at {sessionId}/compressed/{filename}.webm
// Returns the S3 key of the uploaded file.
func uploadCompressedVideo(ctx context.Context, sessionID, originalKey, compressedPath string) (string, error) {
	// Extract filename from original key
	filename := filepath.Base(originalKey)
	// Change extension to .webm
	baseName := strings.TrimSuffix(filename, filepath.Ext(filename))
	compressedFilename := baseName + ".webm"

	compressedKey := fmt.Sprintf("%s/compressed/%s", sessionID, compressedFilename)

	log.Debug().
		Str("original_key", originalKey).
		Str("compressed_key", compressedKey).
		Str("compressed_path", compressedPath).
		Msg("Uploading compressed video to S3")

	// Open the compressed file
	compressedFile, err := os.Open(compressedPath)
	if err != nil {
		return "", fmt.Errorf("failed to open compressed file: %w", err)
	}
	defer compressedFile.Close()

	// Upload to S3
	contentType := "video/webm"
	_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      &mediaBucket,
		Key:         &compressedKey,
		Body:        compressedFile,
		ContentType: &contentType,
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload compressed video to S3: %w", err)
	}

	log.Info().
		Str("compressed_key", compressedKey).
		Msg("Compressed video uploaded to S3")

	return compressedKey, nil
}

// deleteOriginals deletes the original media files from S3 after thumbnails
// have been stored. Best-effort — errors are logged but do not fail the job.
// The 1-day S3 lifecycle policy acts as a safety net (DDR-059).
func deleteOriginals(ctx context.Context, sessionID string, originalKeys []string) {
	deleted := 0
	for _, key := range originalKeys {
		// Skip keys under thumbnails/ or compressed/ — those are generated artifacts.
		parts := strings.SplitN(key, "/", 2)
		if len(parts) == 2 {
			suffix := parts[1]
			if strings.HasPrefix(suffix, "thumbnails/") || strings.HasPrefix(suffix, "compressed/") {
				continue
			}
		}

		_, err := s3Client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(mediaBucket),
			Key:    aws.String(key),
		})
		if err != nil {
			log.Warn().Err(err).Str("key", key).Msg("Failed to delete original file from S3")
			continue
		}
		deleted++
	}

	log.Info().
		Int("deleted", deleted).
		Int("total", len(originalKeys)).
		Str("sessionId", sessionID).
		Msg("Original files deleted from S3 (DDR-059)")
}

func downloadToFile(ctx context.Context, key, localPath string) error {
	log.Debug().Str("key", key).Str("localPath", localPath).Msg("Downloading from S3")
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
			if readErr.Error() == "EOF" {
				break
			}
			return fmt.Errorf("download: %w", readErr)
		}
	}
	return nil
}
