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
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
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
		ID: event.JobID, Status: "processing",
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

	tmpDir := filepath.Join(os.TempDir(), "triage", event.SessionID)
	os.MkdirAll(tmpDir, 0755)

	var videoFileNames []string

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

		log.Debug().Str("key", key).Str("mimeType", mf.MIMEType).Int64("size", mf.Size).Msg("Media file loaded")
	}

	model := event.Model
	if model == "" {
		model = chat.DefaultModelName
	}

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

	log.Debug().Int("fileCount", len(allMediaFiles)).Str("model", model).Msg("Calling AskMediaTriage")
	triageResults, err := chat.AskMediaTriage(ctx, client, allMediaFiles, model, event.SessionID, storeCompressed, keyMapper)
	if err != nil {
		return setTriageError(ctx, event, fmt.Sprintf("Triage failed: %v", err))
	}

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
		item := store.TriageItem{
			Media:        tr.Media,
			Filename:     tr.Filename,
			Key:          key,
			Saveable:     tr.Saveable,
			Reason:       tr.Reason,
			ThumbnailURL: fmt.Sprintf("/api/media/thumbnail?key=%s", key),
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
			keep = append(keep, store.TriageItem{
				Media:        i + 1,
				Filename:     filepath.Base(mf.Path),
				Key:          key,
				Saveable:     true,
				Reason:       "Not evaluated by AI — kept by default",
				ThumbnailURL: fmt.Sprintf("/api/media/thumbnail?key=%s", key),
			})
		}
	}

	sessionStore.PutTriageJob(ctx, event.SessionID, &store.TriageJob{
		ID: event.JobID, Status: "complete", Keep: keep, Discard: discard,
	})

	log.Info().Int("keep", len(keep)).Int("discard", len(discard)).Dur("duration", time.Since(jobStart)).Msg("Triage complete")

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
