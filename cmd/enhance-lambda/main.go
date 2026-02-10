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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
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

// EnhanceEvent is the input payload from Step Functions or async invocation.
// For Step Functions (initial enhancement): type is empty, key + itemIndex are set.
// For async feedback (DDR-053): type is "enhancement-feedback", key + feedback are set.
type EnhanceEvent struct {
	Type      string `json:"type,omitempty"`
	SessionID string `json:"sessionId"`
	JobID     string `json:"jobId"`
	Key       string `json:"key"`
	ItemIndex int    `json:"itemIndex"`
	Bucket    string `json:"bucket,omitempty"`
	Feedback  string `json:"feedback,omitempty"` // DDR-053: enhancement feedback text
}

// EnhanceResult is the output returned to Step Functions.
type EnhanceResult struct {
	OriginalKey      string `json:"originalKey"`
	EnhancedKey      string `json:"enhancedKey"`
	EnhancedThumbKey string `json:"enhancedThumbKey"`
	Phase            string `json:"phase"`
	Phase1Text       string `json:"phase1Text,omitempty"`
	ImagenEdits      int    `json:"imagenEdits"`
	Error            string `json:"error,omitempty"`
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

func handleEnhance(ctx context.Context, event EnhanceEvent) (EnhanceResult, error) {
	handlerStart := time.Now()
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

	logger.Info().Msg("Starting photo enhancement")

	// Validate input.
	if event.SessionID == "" || event.JobID == "" || event.Key == "" {
		return EnhanceResult{
			OriginalKey: event.Key,
			Error:       "sessionId, jobId, and key are required",
		}, fmt.Errorf("sessionId, jobId, and key are required")
	}

	// Download photo from S3.
	tmpPath, cleanup, err := downloadFromS3(ctx, bucket, event.Key)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to download photo")
		updateItemError(ctx, event, "download failed")
		return EnhanceResult{
			OriginalKey: event.Key,
			Phase:       chat.PhaseError,
			Error:       fmt.Sprintf("download failed: %v", err),
		}, err
	}
	defer cleanup()

	// Read image data.
	imageData, err := os.ReadFile(tmpPath)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to read photo")
		updateItemError(ctx, event, "read failed")
		return EnhanceResult{
			OriginalKey: event.Key,
			Phase:       chat.PhaseError,
			Error:       fmt.Sprintf("read failed: %v", err),
		}, err
	}

	// Determine MIME type.
	ext := strings.ToLower(filepath.Ext(event.Key))
	mime := "image/jpeg"
	if m, ok := filehandler.SupportedImageExtensions[ext]; ok {
		mime = m
	}
	logger.Debug().Str("mimeType", mime).Str("extension", ext).Msg("MIME type determined")

	// Get image dimensions for mask generation.
	imgConfig, _, configErr := image.DecodeConfig(bytes.NewReader(imageData))
	imageWidth := 1024
	imageHeight := 1024
	if configErr == nil {
		imageWidth = imgConfig.Width
		imageHeight = imgConfig.Height
	}
	logger.Debug().Int("width", imageWidth).Int("height", imageHeight).Bool("decoded", configErr == nil).Msg("Image dimensions")

	// Initialize Gemini client.
	apiKey := os.Getenv("GEMINI_API_KEY")
	genaiClient, err := chat.NewGeminiClient(ctx, apiKey)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to create Gemini client")
		updateItemError(ctx, event, "Gemini client initialization failed")
		return EnhanceResult{
			OriginalKey: event.Key,
			Phase:       chat.PhaseError,
			Error:       fmt.Sprintf("Gemini client failed: %v", err),
		}, err
	}
	geminiImageClient := chat.NewGeminiImageClient(genaiClient)

	// Set up Imagen client (optional — only if Vertex AI is configured).
	var imagenClient *chat.ImagenClient
	vertexProject := os.Getenv("VERTEX_AI_PROJECT")
	vertexRegion := os.Getenv("VERTEX_AI_REGION")
	vertexToken := os.Getenv("VERTEX_AI_TOKEN")
	if vertexProject != "" && vertexRegion != "" && vertexToken != "" {
		imagenClient = chat.NewImagenClient(vertexProject, vertexRegion, vertexToken)
	}
	logger.Debug().Bool("imagenConfigured", imagenClient != nil).Msg("Imagen client status")

	// Run the full enhancement pipeline.
	state, err := chat.RunFullEnhancement(ctx, geminiImageClient, imagenClient, imageData, mime, imageWidth, imageHeight)
	if err != nil {
		logger.Warn().Err(err).Msg("Enhancement pipeline failed")
		updateItemError(ctx, event, err.Error())
		result := EnhanceResult{
			OriginalKey: event.Key,
			Phase:       chat.PhaseError,
			Error:       err.Error(),
		}
		if state != nil {
			result.Phase1Text = state.Phase1Text
		}
		return result, err
	}

	// Upload enhanced image to S3.
	enhancedKey := fmt.Sprintf("%s/enhanced/%s", event.SessionID, filepath.Base(event.Key))
	contentType := state.CurrentMIME
	if contentType == "" {
		contentType = mime
	}
	logger.Debug().Str("enhancedKey", enhancedKey).Int("size", len(state.CurrentData)).Msg("Uploading enhanced image to S3")
	_, uploadErr := s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      &bucket,
		Key:         &enhancedKey,
		Body:        bytes.NewReader(state.CurrentData),
		ContentType: &contentType,
	})
	if uploadErr != nil {
		logger.Error().Err(uploadErr).Str("enhancedKey", enhancedKey).Msg("Failed to upload enhanced image")
		updateItemError(ctx, event, "upload failed")
		return EnhanceResult{
			OriginalKey: event.Key,
			Phase:       chat.PhaseError,
			Error:       fmt.Sprintf("upload failed: %v", uploadErr),
		}, uploadErr
	}

	// Generate and upload thumbnail of enhanced version.
	enhancedThumbKey := fmt.Sprintf("%s/thumbnails/enhanced-%s.jpg", event.SessionID,
		strings.TrimSuffix(filepath.Base(event.Key), filepath.Ext(event.Key)))
	thumbData, _, thumbErr := generateThumbnailFromBytes(state.CurrentData, contentType, thumbnailMaxDimension)
	if thumbErr == nil {
		thumbContentType := "image/jpeg"
		s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket:      &bucket,
			Key:         &enhancedThumbKey,
			Body:        bytes.NewReader(thumbData),
			ContentType: &thumbContentType,
		})
	}

	// Update DynamoDB with the enhanced item results.
	updateItemComplete(ctx, event, enhancedKey, enhancedThumbKey, state)

	logger.Info().
		Str("enhancedKey", enhancedKey).
		Str("phase", state.Phase).
		Int("imagenEdits", state.ImagenEdits).
		Dur("duration", time.Since(handlerStart)).
		Msg("Photo enhancement complete")

	return EnhanceResult{
		OriginalKey:      event.Key,
		EnhancedKey:      enhancedKey,
		EnhancedThumbKey: enhancedThumbKey,
		Phase:            state.Phase,
		Phase1Text:       state.Phase1Text,
		ImagenEdits:      state.ImagenEdits,
	}, nil
}

func main() {
	lambda.Start(rawHandler)
}

// --- DynamoDB Helpers ---

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

// updateItemComplete updates the enhancement item in DynamoDB with success results.
// Best-effort — errors are logged but don't affect the Lambda response.
func updateItemComplete(ctx context.Context, event EnhanceEvent, enhancedKey, enhancedThumbKey string, state *chat.EnhancementState) {
	job, err := sessionStore.GetEnhancementJob(ctx, event.SessionID, event.JobID)
	if err != nil || job == nil {
		log.Warn().Err(err).Msg("Failed to get enhancement job for completion update")
		return
	}

	if event.ItemIndex >= 0 && event.ItemIndex < len(job.Items) {
		job.Items[event.ItemIndex].Phase = state.Phase
		job.Items[event.ItemIndex].EnhancedKey = enhancedKey
		job.Items[event.ItemIndex].EnhancedThumbKey = enhancedThumbKey
		job.Items[event.ItemIndex].OriginalThumbKey = fmt.Sprintf("%s/thumbnails/%s.jpg", event.SessionID,
			strings.TrimSuffix(filepath.Base(event.Key), filepath.Ext(event.Key)))
		job.Items[event.ItemIndex].Phase1Text = state.Phase1Text
		job.Items[event.ItemIndex].ImagenEdits = state.ImagenEdits
		if state.Analysis != nil {
			job.Items[event.ItemIndex].Analysis = &store.AnalysisResult{
				OverallAssessment:    state.Analysis.OverallAssessment,
				ProfessionalScore:    state.Analysis.ProfessionalScore,
				TargetScore:          state.Analysis.TargetScore,
				NoFurtherEditsNeeded: state.Analysis.NoFurtherEditsNeeded,
			}
			for _, imp := range state.Analysis.RemainingImprovements {
				job.Items[event.ItemIndex].Analysis.RemainingImprovements = append(
					job.Items[event.ItemIndex].Analysis.RemainingImprovements,
					store.ImprovementItem{
						Type:            imp.Type,
						Description:     imp.Description,
						Region:          imp.Region,
						Impact:          imp.Impact,
						ImagenSuitable:  imp.ImagenSuitable,
						EditInstruction: imp.EditInstruction,
					},
				)
			}
		}
		job.CompletedCount++
		if job.CompletedCount >= job.TotalCount {
			job.Status = "complete"
		}
		if err := sessionStore.PutEnhancementJob(ctx, event.SessionID, job); err != nil {
			log.Warn().Err(err).Msg("Failed to update enhancement job with completion")
		}
	}
}

// --- S3 Helpers ---

// downloadFromS3 downloads an S3 object to a temp file and returns its path
// and a cleanup function. Caller must defer cleanup().
func downloadFromS3(ctx context.Context, bucket, key string) (string, func(), error) {
	tmpFile, err := os.CreateTemp("", "enhance-*"+filepath.Ext(key))
	if err != nil {
		return "", nil, fmt.Errorf("create temp file: %w", err)
	}

	result, err := s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return "", nil, fmt.Errorf("S3 GetObject: %w", err)
	}
	defer result.Body.Close()

	buf := make([]byte, 32*1024)
	for {
		n, readErr := result.Body.Read(buf)
		if n > 0 {
			if _, writeErr := tmpFile.Write(buf[:n]); writeErr != nil {
				tmpFile.Close()
				os.Remove(tmpFile.Name())
				return "", nil, fmt.Errorf("write: %w", writeErr)
			}
		}
		if readErr != nil {
			if readErr.Error() == "EOF" {
				break
			}
			tmpFile.Close()
			os.Remove(tmpFile.Name())
			return "", nil, fmt.Errorf("read: %w", readErr)
		}
	}
	tmpFile.Close()

	cleanup := func() { os.Remove(tmpFile.Name()) }
	return tmpFile.Name(), cleanup, nil
}

// --- Thumbnail Helper ---

// generateThumbnailFromBytes creates a thumbnail from raw image bytes.
func generateThumbnailFromBytes(imageData []byte, mimeType string, maxDimension int) ([]byte, string, error) {
	tmpFile, err := os.CreateTemp("", "enhance-thumb-*")
	if err != nil {
		return nil, "", err
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.Write(imageData); err != nil {
		tmpFile.Close()
		return nil, "", err
	}
	tmpFile.Close()

	info, _ := os.Stat(tmpPath)
	mf := &filehandler.MediaFile{
		Path:     tmpPath,
		MIMEType: mimeType,
		Size:     info.Size(),
	}

	return filehandler.GenerateThumbnail(mf, maxDimension)
}

// ===== Enhancement Feedback Handler (DDR-053: absorbed from Worker Lambda) =====

// handleEnhancementFeedback applies user feedback to an already-enhanced photo.
// Invoked asynchronously by the API Lambda (not via Step Functions).
func handleEnhancementFeedback(ctx context.Context, event EnhanceEvent) error {
	jobStart := time.Now()
	job, err := sessionStore.GetEnhancementJob(ctx, event.SessionID, event.JobID)
	if err != nil || job == nil {
		log.Error().Err(err).Str("jobId", event.JobID).Msg("Enhancement job not found for feedback")
		return nil
	}

	// Find the target item.
	var targetIdx int = -1
	for i, item := range job.Items {
		if item.Key == event.Key || item.EnhancedKey == event.Key {
			targetIdx = i
			break
		}
	}
	if targetIdx == -1 {
		log.Error().Str("key", event.Key).Str("jobId", event.JobID).Msg("Item not found in enhancement job")
		return nil
	}
	item := job.Items[targetIdx]

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		log.Error().Str("jobId", event.JobID).Msg("GEMINI_API_KEY not configured for feedback")
		return nil
	}

	genaiClient, err := chat.NewGeminiClient(ctx, apiKey)
	if err != nil {
		log.Error().Err(err).Str("jobId", event.JobID).Msg("Failed to create Gemini client for feedback")
		return nil
	}
	geminiImageClient := chat.NewGeminiImageClient(genaiClient)

	enhancedKey := item.EnhancedKey
	if enhancedKey == "" {
		enhancedKey = item.Key
	}

	tmpPath, cleanup, err := downloadFromS3(ctx, mediaBucket, enhancedKey)
	if err != nil {
		log.Error().Err(err).Str("key", enhancedKey).Msg("Failed to download enhanced image for feedback")
		return nil
	}
	defer cleanup()

	imageData, err := os.ReadFile(tmpPath)
	if err != nil {
		log.Error().Err(err).Str("key", enhancedKey).Msg("Failed to read downloaded file for feedback")
		return nil
	}

	ext := strings.ToLower(filepath.Ext(enhancedKey))
	mime := "image/jpeg"
	if m, ok := filehandler.SupportedImageExtensions[ext]; ok {
		mime = m
	}

	imgConfig, _, err := image.DecodeConfig(bytes.NewReader(imageData))
	imageWidth, imageHeight := 1024, 1024
	if err == nil {
		imageWidth = imgConfig.Width
		imageHeight = imgConfig.Height
	}

	var imagenClient *chat.ImagenClient
	vertexProject := os.Getenv("VERTEX_AI_PROJECT")
	vertexRegion := os.Getenv("VERTEX_AI_REGION")
	vertexToken := os.Getenv("VERTEX_AI_TOKEN")
	if vertexProject != "" && vertexRegion != "" && vertexToken != "" {
		imagenClient = chat.NewImagenClient(vertexProject, vertexRegion, vertexToken)
	}

	// Convert store feedback history to chat format.
	var feedbackHistory []chat.FeedbackEntry
	for _, fe := range item.FeedbackHistory {
		feedbackHistory = append(feedbackHistory, chat.FeedbackEntry{
			UserFeedback:  fe.UserFeedback,
			ModelResponse: fe.ModelResponse,
			Method:        fe.Method,
			Success:       fe.Success,
		})
	}

	resultData, resultMIME, feedbackEntry, err := chat.ProcessFeedback(
		ctx, geminiImageClient, imagenClient,
		imageData, mime, event.Feedback,
		feedbackHistory, imageWidth, imageHeight,
	)
	if err != nil {
		log.Warn().Err(err).Msg("Feedback processing failed")
	}

	if resultData != nil && len(resultData) > 0 {
		feedbackKey := fmt.Sprintf("%s/enhanced/%s", event.SessionID, filepath.Base(item.Key))
		contentType := resultMIME
		_, uploadErr := s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: &mediaBucket, Key: &feedbackKey,
			Body: bytes.NewReader(resultData), ContentType: &contentType,
		})
		if uploadErr != nil {
			log.Error().Err(uploadErr).Str("key", feedbackKey).Msg("Failed to upload feedback result")
			return nil
		}

		// Generate and upload thumbnail.
		thumbKey := fmt.Sprintf("%s/thumbnails/enhanced-%s.jpg", event.SessionID,
			strings.TrimSuffix(filepath.Base(item.Key), filepath.Ext(item.Key)))
		thumbData, _, thumbErr := generateThumbnailFromBytes(resultData, resultMIME, thumbnailMaxDimension)
		if thumbErr == nil {
			thumbContentType := "image/jpeg"
			s3Client.PutObject(ctx, &s3.PutObjectInput{
				Bucket: &mediaBucket, Key: &thumbKey,
				Body: bytes.NewReader(thumbData), ContentType: &thumbContentType,
			})
		}

		// Update DynamoDB.
		job.Items[targetIdx].EnhancedKey = feedbackKey
		job.Items[targetIdx].EnhancedThumbKey = thumbKey
		job.Items[targetIdx].Phase = chat.PhaseFeedback
		if feedbackEntry != nil {
			job.Items[targetIdx].FeedbackHistory = append(job.Items[targetIdx].FeedbackHistory, store.FeedbackEntry{
				UserFeedback:  feedbackEntry.UserFeedback,
				ModelResponse: feedbackEntry.ModelResponse,
				Method:        feedbackEntry.Method,
				Success:       feedbackEntry.Success,
			})
		}
		sessionStore.PutEnhancementJob(ctx, event.SessionID, job)
		log.Info().Str("jobId", event.JobID).Str("feedbackKey", feedbackKey).Dur("duration", time.Since(jobStart)).Msg("Enhancement feedback complete")
	}

	return nil
}
