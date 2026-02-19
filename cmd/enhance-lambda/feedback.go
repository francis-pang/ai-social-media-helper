package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/rs/zerolog/log"

	"github.com/fpang/gemini-media-cli/internal/chat"
	"github.com/fpang/gemini-media-cli/internal/filehandler"
	"github.com/fpang/gemini-media-cli/internal/s3util"
	"github.com/fpang/gemini-media-cli/internal/store"
)

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

	tmpPath, cleanup, err := s3util.DownloadToTempFile(ctx, s3Client, mediaBucket, enhancedKey)
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
			Tagging: s3util.ProjectTagging(),
		})
		if uploadErr != nil {
			log.Error().Err(uploadErr).Str("key", feedbackKey).Msg("Failed to upload feedback result")
			return nil
		}

		// Generate and upload thumbnail.
		thumbKey := fmt.Sprintf("%s/thumbnails/enhanced-%s.jpg", event.SessionID,
			strings.TrimSuffix(filepath.Base(item.Key), filepath.Ext(item.Key)))
		thumbData, _, thumbErr := s3util.GenerateThumbnailFromBytes(resultData, resultMIME, thumbnailMaxDimension)
		if thumbErr == nil {
			thumbContentType := "image/jpeg"
			s3Client.PutObject(ctx, &s3.PutObjectInput{
				Bucket: &mediaBucket, Key: &thumbKey,
				Body: bytes.NewReader(thumbData), ContentType: &thumbContentType,
				Tagging: s3util.ProjectTagging(),
			})
		}

		// Atomically update only this item (no counter change for feedback).
		updatedItem := item
		updatedItem.EnhancedKey = feedbackKey
		updatedItem.EnhancedThumbKey = thumbKey
		updatedItem.Phase = chat.PhaseFeedback
		if feedbackEntry != nil {
			updatedItem.FeedbackHistory = append(updatedItem.FeedbackHistory, store.FeedbackEntry{
				UserFeedback:  feedbackEntry.UserFeedback,
				ModelResponse: feedbackEntry.ModelResponse,
				Method:        feedbackEntry.Method,
				Success:       feedbackEntry.Success,
			})
		}
		if err := sessionStore.UpdateEnhancementItemFields(ctx, event.SessionID, event.JobID, targetIdx, updatedItem); err != nil {
			log.Warn().Err(err).Msg("Failed to update enhancement item with feedback")
		}
		log.Info().Str("jobId", event.JobID).Str("feedbackKey", feedbackKey).Dur("duration", time.Since(jobStart)).Msg("Enhancement feedback complete")
	}

	return nil
}
