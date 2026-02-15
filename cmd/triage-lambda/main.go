// Package main provides a Lambda entry point for the triage pipeline (DDR-053).
//
// This Lambda handles the 3 steps of the Triage Pipeline Step Function (DDR-061):
//   - triage-init-session: Write session record with expectedFileCount (DDR-061)
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

// --- Event and Result types ---

// TriageEvent is the input from Step Functions.
type TriageEvent struct {
	Type              string   `json:"type"`
	SessionID         string   `json:"sessionId"`
	JobID             string   `json:"jobId"`
	Model             string   `json:"model,omitempty"`
	ExpectedFileCount int      `json:"expectedFileCount,omitempty"`
	VideoFileNames    []string `json:"videoFileNames,omitempty"`
}

// TriageInitResult is returned by the triage-init-session handler.
type TriageInitResult struct {
	SessionID string `json:"sessionId"`
	JobID     string `json:"jobId"`
	Model     string `json:"model"`
}

// TriageCheckProcessingResult is returned by the triage-check-processing handler.
type TriageCheckProcessingResult struct {
	SessionID      string `json:"sessionId"`
	JobID          string `json:"jobId"`
	Model          string `json:"model"`
	AllProcessed   bool   `json:"allProcessed"`
	ProcessedCount int    `json:"processedCount"`
	ExpectedCount  int    `json:"expectedCount"`
	ErrorCount     int    `json:"errorCount"`
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

// handleTriageRun reads the pre-processed file manifest from the file-processing
// table, generates presigned URLs, calls Gemini for AI triage, and writes results.
// Simplified from the original that downloaded/processed files (DDR-061).
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

	// Read processed file manifest from file-processing table (DDR-061)
	if fileProcessStore == nil {
		return setTriageError(ctx, event, "File processing store not configured")
	}

	fileResults, err := fileProcessStore.GetFileResults(ctx, event.SessionID, event.JobID)
	if err != nil {
		return setTriageError(ctx, event, fmt.Sprintf("Failed to read file results: %v", err))
	}

	// Filter to valid files only
	var validFiles []store.FileResult
	for _, fr := range fileResults {
		if fr.Status == "valid" {
			validFiles = append(validFiles, fr)
		}
	}

	if len(validFiles) == 0 {
		return setTriageError(ctx, event, "No valid media files found after processing")
	}

	log.Info().Int("totalResults", len(fileResults)).Int("validFiles", len(validFiles)).Str("sessionId", event.SessionID).Msg("File manifest read from DDB (DDR-061)")

	// Build MediaFile list from file results using presigned URLs
	var allMediaFiles []*filehandler.MediaFile
	var s3Keys []string
	pathToKeyMap := make(map[string]string)

	for _, fr := range validFiles {
		// Use processedKey (converted file) if available, otherwise originalKey
		useKey := fr.ProcessedKey
		if useKey == "" {
			useKey = fr.OriginalKey
		}

		// Generate presigned URL for the file
		url, err := generatePresignedURL(ctx, useKey)
		if err != nil {
			log.Warn().Err(err).Str("key", useKey).Msg("Failed to generate presigned URL")
			continue
		}

		mimeType := fr.MimeType
		if mimeType == "" {
			mimeType, _ = filehandler.GetMIMEType(strings.ToLower(filepath.Ext(fr.Filename)))
		}

		mf := &filehandler.MediaFile{
			Path:         fr.Filename, // Use filename as path (for key mapping)
			MIMEType:     mimeType,
			Size:         fr.FileSize,
			PresignedURL: url,
		}

		allMediaFiles = append(allMediaFiles, mf)
		s3Keys = append(s3Keys, fr.OriginalKey)
		pathToKeyMap[fr.Filename] = fr.OriginalKey
	}

	if len(allMediaFiles) == 0 {
		return setTriageError(ctx, event, "No media files with valid presigned URLs")
	}

	model := event.Model
	if model == "" {
		model = chat.DefaultModelName
	}

	keyMapper := func(localPath string) string {
		return pathToKeyMap[localPath]
	}

	// No storeCompressed callback needed — files are already processed (DDR-061)
	storeCompressed := func(ctx context.Context, sessionID, originalKey, compressedPath string) (string, error) {
		return uploadCompressedVideo(ctx, sessionID, originalKey, compressedPath)
	}

	sessionStore.PutTriageJob(ctx, event.SessionID, &store.TriageJob{
		ID: event.JobID, Status: "processing", Phase: "analyzing",
		TotalFiles: len(allMediaFiles),
	})

	log.Debug().Int("fileCount", len(allMediaFiles)).Str("model", model).Msg("Calling AskMediaTriage (DDR-061: presigned URLs from manifest)")
	triageResults, err := chat.AskMediaTriage(ctx, client, allMediaFiles, model, event.SessionID, storeCompressed, keyMapper)
	if err != nil {
		return setTriageError(ctx, event, fmt.Sprintf("Triage failed: %v", err))
	}

	// Build thumbnail URL map from file results
	thumbnailURLs := make(map[int]string)
	for i, fr := range validFiles {
		if fr.ThumbnailKey != "" {
			thumbnailURLs[i] = fmt.Sprintf("/api/media/thumbnail?key=%s", fr.ThumbnailKey)
		}
	}

	// Map results to store items
	var keep, discard []store.TriageItem
	seen := make(map[int]bool)
	for _, tr := range triageResults {
		idx := tr.Media - 1
		if idx < 0 || idx >= len(allMediaFiles) {
			continue
		}
		seen[idx] = true
		key := s3Keys[idx]

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

	// Safety net: missing items default to "keep"
	for i, mf := range allMediaFiles {
		if !seen[i] {
			key := s3Keys[i]
			log.Warn().Int("media", i+1).Str("filename", filepath.Base(mf.Path)).Msg("Media item missing from AI triage results — defaulting to keep")

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

	log.Info().Int("keep", len(keep)).Int("discard", len(discard)).Dur("duration", time.Since(jobStart)).Msg("Triage complete (DDR-061)")

	// Delete original uploads (processed files and thumbnails remain)
	originalKeys := make([]string, 0, len(validFiles))
	for _, fr := range validFiles {
		originalKeys = append(originalKeys, fr.OriginalKey)
	}
	deleteOriginals(ctx, event.SessionID, originalKeys)

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

// generatePresignedURL creates a short-lived S3 presigned GET URL for the
// given key. Gemini fetches the file directly from S3 via this URL (DDR-060).
func generatePresignedURL(ctx context.Context, key string) (string, error) {
	result, err := presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: &mediaBucket, Key: &key,
	}, func(opts *s3.PresignOptions) {
		opts.Expires = 15 * time.Minute
	})
	if err != nil {
		return "", fmt.Errorf("presign GetObject: %w", err)
	}
	return result.URL, nil
}

// uploadVideoToGemini uploads a local video file to the Gemini Files API and
// waits for it to finish processing. Returns the File object whose URI can be
// used in FileData.FileURI for GenerateContent calls. The caller is responsible
// for deleting the uploaded file after use via client.Files.Delete.
func uploadVideoToGemini(ctx context.Context, client *genai.Client, localPath, mimeType string) (*genai.File, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat file: %w", err)
	}

	log.Debug().
		Str("path", localPath).
		Int64("size_bytes", info.Size()).
		Str("mime_type", mimeType).
		Msg("Starting Gemini Files API upload for large video")

	uploadStart := time.Now()
	file, err := client.Files.Upload(ctx, f, &genai.UploadFileConfig{
		MIMEType: mimeType,
	})
	if err != nil {
		return nil, fmt.Errorf("upload file: %w", err)
	}

	log.Debug().
		Str("name", file.Name).
		Str("uri", file.URI).
		Dur("upload_duration", time.Since(uploadStart)).
		Msg("Video uploaded to Gemini, waiting for processing...")

	// Poll until the file is ACTIVE (processed) or FAILED.
	const pollInterval = 5 * time.Second
	const pollTimeout = 5 * time.Minute
	deadline := time.Now().Add(pollTimeout)

	for file.State == genai.FileStateProcessing {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout waiting for Gemini file processing after %v", pollTimeout)
		}
		time.Sleep(pollInterval)
		file, err = client.Files.Get(ctx, file.Name, nil)
		if err != nil {
			return nil, fmt.Errorf("get file state: %w", err)
		}
	}

	if file.State == genai.FileStateFailed {
		return nil, fmt.Errorf("Gemini file processing failed: %s", file.Name)
	}

	log.Info().
		Str("name", file.Name).
		Str("uri", file.URI).
		Str("state", string(file.State)).
		Dur("total_duration", time.Since(uploadStart)).
		Msg("Gemini Files API upload complete")

	return file, nil
}

// interleaveMedia distributes videos evenly among images so that triage
// batches receive a balanced mix of media types. Given 20 videos and 49
// images, interleaving produces a sequence like [V, I, I, V, I, I, V, I, I, ...]
// ensuring each batch of 20 items contains roughly 6 videos and 14 images
// instead of one all-video batch that overwhelms the Gemini API.
//
// Both slices must have the same length; pathToKeyMap is keyed by local path
// and does not need reordering.
func interleaveMedia(files []*filehandler.MediaFile, keys []string) ([]*filehandler.MediaFile, []string) {
	// Separate into videos and images, preserving their paired keys.
	type entry struct {
		file *filehandler.MediaFile
		key  string
	}
	var videos, images []entry
	for i, mf := range files {
		ext := strings.ToLower(filepath.Ext(mf.Path))
		if filehandler.IsVideo(ext) {
			videos = append(videos, entry{mf, keys[i]})
		} else {
			images = append(images, entry{mf, keys[i]})
		}
	}

	if len(videos) == 0 || len(images) == 0 {
		return files, keys // Nothing to interleave.
	}

	// Distribute: for each video, emit a proportional number of images.
	imagesPerSlot := max(len(images)/len(videos), 1)

	result := make([]entry, 0, len(files))
	vi, ii := 0, 0
	for vi < len(videos) || ii < len(images) {
		if vi < len(videos) {
			result = append(result, videos[vi])
			vi++
		}
		for j := 0; j < imagesPerSlot && ii < len(images); j++ {
			result = append(result, images[ii])
			ii++
		}
	}
	// Append any remaining images.
	for ii < len(images) {
		result = append(result, images[ii])
		ii++
	}

	// Rebuild the parallel slices.
	newFiles := make([]*filehandler.MediaFile, len(result))
	newKeys := make([]string, len(result))
	for i, e := range result {
		newFiles[i] = e.file
		newKeys[i] = e.key
	}

	log.Info().
		Int("videos", len(videos)).
		Int("images", len(images)).
		Int("imagesPerSlot", imagesPerSlot).
		Msg("Media files interleaved to distribute videos across triage batches")

	return newFiles, newKeys
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
