package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/fpang/gemini-media-cli/internal/chat"
	"github.com/fpang/gemini-media-cli/internal/filehandler"
	"github.com/fpang/gemini-media-cli/internal/s3util"
	"github.com/fpang/gemini-media-cli/internal/store"
)

func handler(ctx context.Context, event SelectionEvent) (SelectionResult, error) {
	handlerStart := time.Now()
	if coldStart {
		coldStart = false
		log.Info().Str("function", "selection-lambda").Msg("Cold start — first invocation")
	}
	bucket := mediaBucket
	if event.Bucket != "" {
		bucket = event.Bucket
	}

	logger := log.With().
		Str("sessionId", event.SessionID).
		Str("jobId", event.JobID).
		Int("mediaCount", len(event.MediaKeys)).
		Logger()

	logger.Info().
		Str("tripContext", event.TripContext).
		Str("model", event.Model).
		Int("thumbnailCount", len(event.ThumbnailKeys)).
		Str("bucket", bucket).
		Msg("Starting AI media selection")

	// Validate input.
	logger.Debug().
		Bool("hasSessionID", event.SessionID != "").
		Bool("hasJobID", event.JobID != "").
		Bool("hasMediaKeys", len(event.MediaKeys) > 0).
		Msg("Validating event fields")
	if event.SessionID == "" || event.JobID == "" {
		return SelectionResult{Error: "sessionId and jobId are required"},
			fmt.Errorf("sessionId and jobId are required")
	}
	if len(event.MediaKeys) == 0 {
		return SelectionResult{Error: "no media keys provided"},
			fmt.Errorf("no media keys provided")
	}

	model := chat.DefaultModelName
	if event.Model != "" {
		model = event.Model
	}

	// Update job status to "processing" in DynamoDB.
	selJob := &store.SelectionJob{
		ID:     event.JobID,
		Status: "processing",
	}
	logger.Debug().Str("status", "processing").Msg("Updating DynamoDB job status")
	if err := sessionStore.PutSelectionJob(ctx, event.SessionID, selJob); err != nil {
		logger.Error().Err(err).Msg("Failed to update job status")
		// Non-fatal — continue processing even if status update fails.
	}

	// Download media files and create MediaFile objects.
	tmpDir := filepath.Join(os.TempDir(), "selection", event.SessionID)
	os.MkdirAll(tmpDir, 0755)
	defer os.RemoveAll(tmpDir)

	var allMediaFiles []*filehandler.MediaFile
	var s3Keys []string
	pathToKeyMap := make(map[string]string) // Map local path -> S3 key

	for _, key := range event.MediaKeys {
		filename := filepath.Base(key)
		ext := strings.ToLower(filepath.Ext(filename))

		if !filehandler.IsSupported(ext) {
			logger.Debug().Str("key", key).Msg("Skipping unsupported file type")
			continue
		}

		localPath := filepath.Join(tmpDir, filename)
		if err := s3util.DownloadToFile(ctx, s3Client, bucket, key, localPath); err != nil {
			logger.Warn().Err(err).Str("key", key).Msg("Failed to download file, skipping")
			continue
		}

		mf, err := filehandler.LoadMediaFile(localPath)
		if err != nil {
			logger.Warn().Err(err).Str("key", key).Msg("Failed to load media file, skipping")
			continue
		}

		// For videos, generate presigned URL so Gemini fetches directly from S3 (DDR-060).
		if filehandler.IsVideo(ext) {
			url, err := s3util.GeneratePresignedURL(ctx, presignClient, bucket, key, 15*time.Minute)
			if err != nil {
				logger.Warn().Err(err).Str("key", key).Msg("Failed to generate presigned URL for video")
				// Continue without presigned URL — buildMediaParts will fall back to compress+upload
			} else {
				mf.PresignedURL = url
				logger.Debug().Str("key", key).Msg("Presigned URL generated for video (DDR-060)")
			}
		}

		allMediaFiles = append(allMediaFiles, mf)
		s3Keys = append(s3Keys, key)
		pathToKeyMap[localPath] = key
	}

	if len(allMediaFiles) == 0 {
		errMsg := "no supported media files found"
		selJob.Status = "error"
		selJob.Error = errMsg
		sessionStore.PutSelectionJob(ctx, event.SessionID, selJob)
		return SelectionResult{JobID: event.JobID, Error: errMsg},
			fmt.Errorf("%s", errMsg)
	}

	logger.Info().Int("count", len(allMediaFiles)).Msg("Loaded media files, calling Gemini")

	// Initialize Gemini client and run selection.
	apiKey := os.Getenv("GEMINI_API_KEY")
	logger.Debug().Str("model", model).Msg("Calling Gemini API for media selection")
	client, err := chat.NewGeminiClient(ctx, apiKey)
	if err != nil {
		errMsg := fmt.Sprintf("failed to create Gemini client: %v", err)
		selJob.Status = "error"
		selJob.Error = errMsg
		sessionStore.PutSelectionJob(ctx, event.SessionID, selJob)
		return SelectionResult{JobID: event.JobID, Error: errMsg}, err
	}

	// Create key mapper function
	keyMapper := func(localPath string) string {
		return pathToKeyMap[localPath]
	}

	// Create compressed video store callback
	storeCompressed := func(ctx context.Context, sessionID, originalKey, compressedPath string) (string, error) {
		return s3util.UploadCompressedVideo(ctx, s3Client, mediaBucket, sessionID, originalKey, compressedPath)
	}

	selResult, err := chat.AskMediaSelectionJSON(ctx, client, allMediaFiles, event.TripContext, model, event.SessionID, storeCompressed, keyMapper)
	if err != nil {
		errMsg := fmt.Sprintf("selection failed: %v", err)
		selJob.Status = "error"
		selJob.Error = errMsg
		sessionStore.PutSelectionJob(ctx, event.SessionID, selJob)
		return SelectionResult{JobID: event.JobID, Error: errMsg}, err
	}

	// Map results to items with S3 keys and thumbnail URLs.
	for _, sel := range selResult.Selected {
		idx := sel.Media - 1
		if idx < 0 || idx >= len(allMediaFiles) {
			logger.Warn().Int("mediaIndex", idx+1).Int("maxIndex", len(allMediaFiles)).Msg("Skipping result with out-of-bounds media index")
			continue
		}
		key := s3Keys[idx]
		thumbKey := fmt.Sprintf("%s/thumbnails/%s.jpg", event.SessionID,
			strings.TrimSuffix(filepath.Base(key), filepath.Ext(key)))
		selJob.Selected = append(selJob.Selected, store.SelectedItem{
			Rank:           sel.Rank,
			Media:          sel.Media,
			Filename:       sel.Filename,
			Key:            key,
			Type:           sel.Type,
			Scene:          sel.Scene,
			Justification:  sel.Justification,
			ComparisonNote: sel.ComparisonNote,
			ThumbnailURL:   fmt.Sprintf("/api/media/thumbnail?key=%s", thumbKey),
		})
	}

	for _, exc := range selResult.Excluded {
		idx := exc.Media - 1
		if idx < 0 || idx >= len(allMediaFiles) {
			logger.Warn().Int("mediaIndex", idx+1).Int("maxIndex", len(allMediaFiles)).Msg("Skipping result with out-of-bounds media index")
			continue
		}
		key := s3Keys[idx]
		thumbKey := fmt.Sprintf("%s/thumbnails/%s.jpg", event.SessionID,
			strings.TrimSuffix(filepath.Base(key), filepath.Ext(key)))
		selJob.Excluded = append(selJob.Excluded, store.ExcludedItem{
			Media:        exc.Media,
			Filename:     exc.Filename,
			Key:          key,
			Reason:       exc.Reason,
			Category:     exc.Category,
			DuplicateOf:  exc.DuplicateOf,
			ThumbnailURL: fmt.Sprintf("/api/media/thumbnail?key=%s", thumbKey),
		})
	}

	for _, sg := range selResult.SceneGroups {
		group := store.SceneGroup{
			Name:      sg.Name,
			GPS:       sg.GPS,
			TimeRange: sg.TimeRange,
		}
		for _, item := range sg.Items {
			idx := item.Media - 1
			if idx < 0 || idx >= len(allMediaFiles) {
				logger.Warn().Int("mediaIndex", idx+1).Int("maxIndex", len(allMediaFiles)).Msg("Skipping result with out-of-bounds media index")
				continue
			}
			key := s3Keys[idx]
			thumbKey := fmt.Sprintf("%s/thumbnails/%s.jpg", event.SessionID,
				strings.TrimSuffix(filepath.Base(key), filepath.Ext(key)))
			group.Items = append(group.Items, store.SceneGroupItem{
				Media:        item.Media,
				Filename:     item.Filename,
				Key:          key,
				Type:         item.Type,
				Selected:     item.Selected,
				Description:  item.Description,
				ThumbnailURL: fmt.Sprintf("/api/media/thumbnail?key=%s", thumbKey),
			})
		}
		selJob.SceneGroups = append(selJob.SceneGroups, group)
	}

	// Write completed results to DynamoDB.
	selJob.Status = "complete"
	if err := sessionStore.PutSelectionJob(ctx, event.SessionID, selJob); err != nil {
		errMsg := fmt.Sprintf("failed to write results to DynamoDB: %v", err)
		logger.Error().Err(err).Msg(errMsg)
		return SelectionResult{JobID: event.JobID, Error: errMsg}, err
	}

	logger.Info().
		Int("selected", len(selJob.Selected)).
		Int("excluded", len(selJob.Excluded)).
		Int("scenes", len(selJob.SceneGroups)).
		Dur("duration", time.Since(handlerStart)).
		Msg("Selection complete, results written to DynamoDB")

	return SelectionResult{
		JobID:           event.JobID,
		SelectedCount:   len(selJob.Selected),
		ExcludedCount:   len(selJob.Excluded),
		SceneGroupCount: len(selJob.SceneGroups),
	}, nil
}
