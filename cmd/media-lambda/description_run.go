package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpang/gemini-media-cli/internal/chat"
	"github.com/fpang/gemini-media-cli/internal/filehandler"
	"github.com/rs/zerolog/log"
)

// --- Description Processing ---

// runDescriptionJob generates the initial caption for a post group.
func runDescriptionJob(job *descriptionJob, keys []string) {
	job.mu.Lock()
	job.status = "processing"
	job.mu.Unlock()

	ctx := context.Background()

	// Initialize Gemini client
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		setDescriptionJobError(job, "API key not configured")
		return
	}

	genaiClient, err := chat.NewGeminiClient(ctx, apiKey)
	if err != nil {
		setDescriptionJobError(job, "failed to initialize AI client")
		return
	}

	// Build media items from S3 keys
	mediaItems, err := buildDescriptionMediaItems(ctx, keys)
	if err != nil {
		setDescriptionJobError(job, "failed to prepare media")
		return
	}

	// Store media items for potential regeneration
	job.mu.Lock()
	job.mediaItems = mediaItems
	job.mu.Unlock()

	// Generate description
	result, rawResponse, err := chat.GenerateDescription(
		ctx, genaiClient,
		job.groupLabel, job.tripContext,
		mediaItems,
	)
	if err != nil {
		log.Error().Err(err).Msg("Description generation failed")
		setDescriptionJobError(job, "caption generation failed")
		return
	}

	job.mu.Lock()
	job.result = result
	job.rawResponse = rawResponse
	job.status = "complete"
	job.mu.Unlock()

	log.Info().
		Str("job", job.id).
		Int("caption_length", len(result.Caption)).
		Msg("Description generation complete")
}

// runDescriptionFeedback regenerates a caption with user feedback.
func runDescriptionFeedback(job *descriptionJob, feedback string) {
	ctx := context.Background()

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		setDescriptionJobError(job, "API key not configured")
		return
	}

	genaiClient, err := chat.NewGeminiClient(ctx, apiKey)
	if err != nil {
		setDescriptionJobError(job, "failed to initialize AI client")
		return
	}

	job.mu.Lock()
	mediaItems := job.mediaItems
	history := make([]chat.DescriptionConversationEntry, len(job.history))
	copy(history, job.history)
	groupLabel := job.groupLabel
	tripContext := job.tripContext
	job.mu.Unlock()

	result, rawResponse, err := chat.RegenerateDescription(
		ctx, genaiClient,
		groupLabel, tripContext,
		mediaItems,
		feedback, history,
	)
	if err != nil {
		log.Error().Err(err).Msg("Description regeneration failed")
		setDescriptionJobError(job, "caption regeneration failed")
		return
	}

	job.mu.Lock()
	job.result = result
	job.rawResponse = rawResponse
	job.status = "complete"
	job.mu.Unlock()

	log.Info().
		Str("job", job.id).
		Int("caption_length", len(result.Caption)).
		Int("feedback_round", len(history)).
		Msg("Description regeneration complete")
}

// buildDescriptionMediaItems downloads thumbnails from S3 and builds
// DescriptionMediaItem structs for the caption generation prompt.
func buildDescriptionMediaItems(ctx context.Context, keys []string) ([]chat.DescriptionMediaItem, error) {
	var items []chat.DescriptionMediaItem

	for _, key := range keys {
		filename := filepath.Base(key)
		ext := strings.ToLower(filepath.Ext(key))

		item := chat.DescriptionMediaItem{
			Filename: filename,
		}

		if filehandler.IsImage(ext) {
			item.Type = "Photo"

			// Try to get the pre-generated thumbnail from S3
			// Thumbnails are at {sessionId}/thumbnails/{filename}.jpg
			parts := strings.SplitN(key, "/", 2)
			thumbKey := fmt.Sprintf("%s/thumbnails/%s.jpg", parts[0], strings.TrimSuffix(filename, ext))

			tmpPath, cleanup, err := downloadFromS3(ctx, thumbKey)
			if err != nil {
				// Fall back: download the original and generate a thumbnail in-memory
				log.Debug().Str("key", thumbKey).Msg("Pre-generated thumbnail not found, downloading original")
				origPath, origCleanup, origErr := downloadFromS3(ctx, key)
				if origErr != nil {
					log.Warn().Err(origErr).Str("key", key).Msg("Failed to download media for description, skipping")
					continue
				}
				defer origCleanup()

				origData, readErr := os.ReadFile(origPath)
				if readErr != nil {
					log.Warn().Err(readErr).Str("key", key).Msg("Failed to read original for description, skipping")
					continue
				}

				mime := "image/jpeg"
				if m, ok := filehandler.SupportedImageExtensions[ext]; ok {
					mime = m
				}

				thumbData, thumbMIME, thumbErr := generateThumbnailFromBytes(origData, mime, filehandler.DefaultThumbnailMaxDimension)
				if thumbErr != nil {
					log.Warn().Err(thumbErr).Str("key", key).Msg("Failed to generate thumbnail for description, skipping")
					continue
				}
				item.ThumbnailData = thumbData
				item.ThumbnailMIMEType = thumbMIME
			} else {
				defer cleanup()
				thumbData, err := os.ReadFile(tmpPath)
				if err != nil {
					log.Warn().Err(err).Str("key", thumbKey).Msg("Failed to read thumbnail")
					continue
				}
				item.ThumbnailData = thumbData
				item.ThumbnailMIMEType = "image/jpeg"
			}
		} else if filehandler.IsVideo(ext) {
			item.Type = "Video"

			// Try to get the pre-generated video thumbnail from S3
			parts := strings.SplitN(key, "/", 2)
			thumbKey := fmt.Sprintf("%s/thumbnails/%s.jpg", parts[0], strings.TrimSuffix(filename, ext))

			tmpPath, cleanup, err := downloadFromS3(ctx, thumbKey)
			if err != nil {
				log.Debug().Str("key", thumbKey).Msg("Video thumbnail not found, skipping video media")
				// Videos without thumbnails: send as photo thumbnail if available
				continue
			}
			defer cleanup()

			// For videos, send the thumbnail frame as a photo (not a full video upload)
			// This keeps the description request fast and within API Gateway timeout.
			thumbData, err := os.ReadFile(tmpPath)
			if err != nil {
				log.Warn().Err(err).Str("key", thumbKey).Msg("Failed to read video thumbnail")
				continue
			}
			item.ThumbnailData = thumbData
			item.ThumbnailMIMEType = "image/jpeg"
		} else {
			continue // skip unknown media types
		}

		items = append(items, item)
	}

	if len(items) == 0 {
		return nil, fmt.Errorf("no media items could be prepared for description")
	}

	return items, nil
}
