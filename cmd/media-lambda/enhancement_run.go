package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/fpang/gemini-media-cli/internal/chat"
	"github.com/fpang/gemini-media-cli/internal/filehandler"
	"github.com/rs/zerolog/log"
)

// POST /api/enhance/{id}/feedback
// Body: {"sessionId": "uuid", "key": "uuid/file.jpg", "feedback": "make it brighter"}
func handleEnhanceFeedback(w http.ResponseWriter, r *http.Request, job *enhancementJob) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		SessionID string `json:"sessionId"`
		Key       string `json:"key"`
		Feedback  string `json:"feedback"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Ownership check (DDR-028)
	if req.SessionID == "" || req.SessionID != job.sessionID {
		httpError(w, http.StatusNotFound, "not found")
		return
	}
	if req.Key == "" || req.Feedback == "" {
		httpError(w, http.StatusBadRequest, "key and feedback are required")
		return
	}

	// Find the item
	job.mu.Lock()
	var targetIdx int = -1
	for i, item := range job.items {
		if item.Key == req.Key || item.EnhancedKey == req.Key {
			targetIdx = i
			break
		}
	}
	if targetIdx == -1 {
		job.mu.Unlock()
		httpError(w, http.StatusNotFound, "item not found in enhancement job")
		return
	}
	item := job.items[targetIdx]
	job.mu.Unlock()

	// Run feedback processing asynchronously
	go func() {
		ctx := context.Background()
		apiKey := os.Getenv("GEMINI_API_KEY")
		if apiKey == "" {
			return
		}

		genaiClient, err := chat.NewGeminiClient(ctx, apiKey)
		if err != nil {
			log.Error().Err(err).Msg("Failed to create Gemini client for feedback")
			return
		}
		geminiImageClient := chat.NewGeminiImageClient(genaiClient)

		// Download the current enhanced image
		enhancedKey := item.EnhancedKey
		if enhancedKey == "" {
			enhancedKey = item.Key
		}

		tmpPath, cleanup, err := downloadFromS3(ctx, enhancedKey)
		if err != nil {
			log.Error().Err(err).Str("key", enhancedKey).Msg("Failed to download enhanced image for feedback")
			return
		}
		defer cleanup()

		imageData, err := os.ReadFile(tmpPath)
		if err != nil {
			log.Error().Err(err).Msg("Failed to read enhanced image")
			return
		}

		// Determine MIME type
		ext := strings.ToLower(filepath.Ext(enhancedKey))
		mime := "image/jpeg"
		if m, ok := filehandler.SupportedImageExtensions[ext]; ok {
			mime = m
		}

		// Get image dimensions for mask generation
		imgConfig, _, err := image.DecodeConfig(bytes.NewReader(imageData))
		imageWidth := 1024
		imageHeight := 1024
		if err == nil {
			imageWidth = imgConfig.Width
			imageHeight = imgConfig.Height
		}

		// Set up Imagen client (optional)
		var imagenClient *chat.ImagenClient
		vertexProject := os.Getenv("VERTEX_AI_PROJECT")
		vertexRegion := os.Getenv("VERTEX_AI_REGION")
		vertexToken := os.Getenv("VERTEX_AI_TOKEN")
		if vertexProject != "" && vertexRegion != "" && vertexToken != "" {
			imagenClient = chat.NewImagenClient(vertexProject, vertexRegion, vertexToken)
		}

		// Process feedback
		resultData, resultMIME, feedbackEntry, err := chat.ProcessFeedback(
			ctx, geminiImageClient, imagenClient,
			imageData, mime, req.Feedback,
			item.FeedbackHistory, imageWidth, imageHeight,
		)
		if err != nil {
			log.Warn().Err(err).Msg("Feedback processing failed")
		}

		// Upload the result to S3
		if resultData != nil && len(resultData) > 0 {
			feedbackKey := fmt.Sprintf("%s/enhanced/%s", job.sessionID, filepath.Base(item.Key))
			contentType := resultMIME
			_, uploadErr := s3Client.PutObject(ctx, &s3.PutObjectInput{
				Bucket:      &mediaBucket,
				Key:         &feedbackKey,
				Body:        bytes.NewReader(resultData),
				ContentType: &contentType,
			})
			if uploadErr != nil {
				log.Error().Err(uploadErr).Str("key", feedbackKey).Msg("Failed to upload feedback result")
				return
			}

			// Generate and upload thumbnail
			thumbKey := fmt.Sprintf("%s/thumbnails/enhanced-%s.jpg", job.sessionID,
				strings.TrimSuffix(filepath.Base(item.Key), filepath.Ext(item.Key)))
			thumbData, _, thumbErr := generateThumbnailFromBytes(resultData, resultMIME, 400)
			if thumbErr == nil {
				thumbContentType := "image/jpeg"
				s3Client.PutObject(ctx, &s3.PutObjectInput{
					Bucket:      &mediaBucket,
					Key:         &thumbKey,
					Body:        bytes.NewReader(thumbData),
					ContentType: &thumbContentType,
				})
			}

			// Update job state
			job.mu.Lock()
			job.items[targetIdx].EnhancedKey = feedbackKey
			job.items[targetIdx].EnhancedThumbKey = thumbKey
			job.items[targetIdx].Phase = chat.PhaseFeedback
			if feedbackEntry != nil {
				job.items[targetIdx].FeedbackHistory = append(job.items[targetIdx].FeedbackHistory, *feedbackEntry)
			}
			job.mu.Unlock()
		}
	}()

	respondJSON(w, http.StatusAccepted, map[string]string{
		"status": "processing",
	})
}

// --- Enhancement Processing ---

func runEnhancementJob(job *enhancementJob, photoKeys []string) {
	job.mu.Lock()
	job.status = "processing"
	job.totalCount = len(photoKeys)
	// Initialize items
	for _, key := range photoKeys {
		job.items = append(job.items, enhancementResultItem{
			Key:         key,
			Filename:    filepath.Base(key),
			Phase:       chat.PhaseInitial,
			OriginalKey: key,
		})
	}
	job.mu.Unlock()

	ctx := context.Background()

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		setEnhancementJobError(job, "GEMINI_API_KEY not configured")
		return
	}

	genaiClient, err := chat.NewGeminiClient(ctx, apiKey)
	if err != nil {
		setEnhancementJobError(job, fmt.Sprintf("Failed to create Gemini client: %v", err))
		return
	}
	geminiImageClient := chat.NewGeminiImageClient(genaiClient)

	// Set up Imagen client (optional — only if Vertex AI is configured)
	var imagenClient *chat.ImagenClient
	vertexProject := os.Getenv("VERTEX_AI_PROJECT")
	vertexRegion := os.Getenv("VERTEX_AI_REGION")
	vertexToken := os.Getenv("VERTEX_AI_TOKEN")
	if vertexProject != "" && vertexRegion != "" && vertexToken != "" {
		imagenClient = chat.NewImagenClient(vertexProject, vertexRegion, vertexToken)
		log.Info().Msg("Imagen 3 client configured for Phase 3 surgical edits")
	} else {
		log.Info().Msg("Imagen 3 not configured — Phase 3 will be skipped")
	}

	// Process each photo sequentially (to stay within rate limits)
	// Future: use Step Functions Map state for parallel processing
	for i, key := range photoKeys {
		log.Info().
			Int("index", i+1).
			Int("total", len(photoKeys)).
			Str("key", key).
			Msg("Enhancing photo")

		// Download from S3
		tmpPath, cleanup, err := downloadFromS3(ctx, key)
		if err != nil {
			job.mu.Lock()
			job.items[i].Phase = chat.PhaseError
			job.items[i].Error = fmt.Sprintf("Download failed: %v", err)
			job.mu.Unlock()
			log.Warn().Err(err).Str("key", key).Msg("Failed to download photo for enhancement")
			continue
		}

		imageData, err := os.ReadFile(tmpPath)
		cleanup()
		if err != nil {
			job.mu.Lock()
			job.items[i].Phase = chat.PhaseError
			job.items[i].Error = fmt.Sprintf("Read failed: %v", err)
			job.mu.Unlock()
			continue
		}

		// Determine MIME type
		ext := strings.ToLower(filepath.Ext(key))
		mime := "image/jpeg"
		if m, ok := filehandler.SupportedImageExtensions[ext]; ok {
			mime = m
		}

		// Get image dimensions for mask generation
		imgConfig, _, configErr := image.DecodeConfig(bytes.NewReader(imageData))
		imageWidth := 1024
		imageHeight := 1024
		if configErr == nil {
			imageWidth = imgConfig.Width
			imageHeight = imgConfig.Height
		}

		// Run the full enhancement pipeline
		job.mu.Lock()
		job.items[i].Phase = chat.PhaseOne
		job.mu.Unlock()

		state, err := chat.RunFullEnhancement(ctx, geminiImageClient, imagenClient, imageData, mime, imageWidth, imageHeight)
		if err != nil {
			job.mu.Lock()
			job.items[i].Phase = chat.PhaseError
			job.items[i].Error = err.Error()
			if state != nil {
				job.items[i].Phase1Text = state.Phase1Text
				job.items[i].Analysis = state.Analysis
			}
			job.mu.Unlock()
			log.Warn().Err(err).Str("key", key).Msg("Enhancement pipeline failed")
			// Continue with next photo — partial success is acceptable
			job.mu.Lock()
			job.completedCount++
			job.mu.Unlock()
			continue
		}

		// Upload enhanced image to S3
		enhancedKey := fmt.Sprintf("%s/enhanced/%s", job.sessionID, filepath.Base(key))
		contentType := state.CurrentMIME
		if contentType == "" {
			contentType = mime
		}
		_, uploadErr := s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket:      &mediaBucket,
			Key:         &enhancedKey,
			Body:        bytes.NewReader(state.CurrentData),
			ContentType: &contentType,
		})
		if uploadErr != nil {
			job.mu.Lock()
			job.items[i].Phase = chat.PhaseError
			job.items[i].Error = fmt.Sprintf("Upload failed: %v", uploadErr)
			job.mu.Unlock()
			log.Error().Err(uploadErr).Str("key", enhancedKey).Msg("Failed to upload enhanced image")
			job.mu.Lock()
			job.completedCount++
			job.mu.Unlock()
			continue
		}

		// Generate and upload thumbnail of enhanced version
		enhancedThumbKey := fmt.Sprintf("%s/thumbnails/enhanced-%s.jpg", job.sessionID,
			strings.TrimSuffix(filepath.Base(key), filepath.Ext(key)))
		thumbData, _, thumbErr := generateThumbnailFromBytes(state.CurrentData, contentType, 400)
		if thumbErr == nil {
			thumbContentType := "image/jpeg"
			s3Client.PutObject(ctx, &s3.PutObjectInput{
				Bucket:      &mediaBucket,
				Key:         &enhancedThumbKey,
				Body:        bytes.NewReader(thumbData),
				ContentType: &thumbContentType,
			})
		}

		// Update job state
		job.mu.Lock()
		job.items[i].Phase = state.Phase
		job.items[i].EnhancedKey = enhancedKey
		job.items[i].EnhancedThumbKey = enhancedThumbKey
		job.items[i].OriginalThumbKey = fmt.Sprintf("%s/thumbnails/%s.jpg", job.sessionID,
			strings.TrimSuffix(filepath.Base(key), filepath.Ext(key)))
		job.items[i].Phase1Text = state.Phase1Text
		job.items[i].Analysis = state.Analysis
		job.items[i].ImagenEdits = state.ImagenEdits
		job.completedCount++
		job.mu.Unlock()

		log.Info().
			Int("index", i+1).
			Str("key", key).
			Str("phase", state.Phase).
			Float64("score", 0). // Will be filled from analysis
			Msg("Photo enhancement complete")
	}

	job.mu.Lock()
	job.status = "complete"
	job.mu.Unlock()

	log.Info().
		Int("total", job.totalCount).
		Int("completed", job.completedCount).
		Msg("Enhancement job complete")
}
