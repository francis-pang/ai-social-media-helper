package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/fpang/gemini-media-cli/internal/chat"
	"github.com/fpang/gemini-media-cli/internal/filehandler"
	"github.com/rs/zerolog/log"
)

// --- Selection Processing ---

func runSelectionJob(job *selectionJob, tripContext string, model string) {
	job.mu.Lock()
	job.status = "processing"
	job.mu.Unlock()

	ctx := context.Background()

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		setSelectionJobError(job, "GEMINI_API_KEY not configured")
		return
	}

	client, err := chat.NewGeminiClient(ctx, apiKey)
	if err != nil {
		setSelectionJobError(job, fmt.Sprintf("Failed to create Gemini client: %v", err))
		return
	}

	// List objects in the session prefix
	prefix := job.sessionID + "/"
	listResult, err := s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: &mediaBucket,
		Prefix: &prefix,
	})
	if err != nil {
		setSelectionJobError(job, fmt.Sprintf("Failed to list S3 objects: %v", err))
		return
	}

	if len(listResult.Contents) == 0 {
		setSelectionJobError(job, "No files found for session")
		return
	}

	log.Info().Int("count", len(listResult.Contents)).Str("session", job.sessionID).Msg("Found S3 objects for selection")

	// Download each file and create MediaFile objects
	tmpDir := filepath.Join(os.TempDir(), "selection", job.sessionID)
	os.MkdirAll(tmpDir, 0755)
	defer os.RemoveAll(tmpDir)

	var allMediaFiles []*filehandler.MediaFile
	var s3Keys []string

	// Filter to only media files under the session root (exclude thumbnails/ subfolder)
	for _, obj := range listResult.Contents {
		key := *obj.Key
		filename := filepath.Base(key)
		ext := strings.ToLower(filepath.Ext(filename))

		// Skip files in thumbnails/ subfolder
		relPath := strings.TrimPrefix(key, prefix)
		if strings.Contains(relPath, "/") {
			log.Debug().Str("key", key).Msg("Skipping non-root-level file")
			continue
		}

		if !filehandler.IsSupported(ext) {
			log.Debug().Str("key", key).Msg("Skipping unsupported file type")
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
	}

	if len(allMediaFiles) == 0 {
		setSelectionJobError(job, "No supported media files found in the uploaded session")
		return
	}

	log.Info().Int("count", len(allMediaFiles)).Msg("Starting thumbnail pre-generation and selection")

	// Pre-generate and cache thumbnails in S3 (DDR-030)
	preGenerateThumbnails(ctx, job.sessionID, allMediaFiles, s3Keys)

	// Call Gemini for structured JSON selection (DDR-030)
	selResult, err := chat.AskMediaSelectionJSON(ctx, client, allMediaFiles, tripContext, model)
	if err != nil {
		setSelectionJobError(job, fmt.Sprintf("Selection failed: %v", err))
		return
	}

	// Map results to items with S3 keys and thumbnail URLs
	job.mu.Lock()
	for _, sel := range selResult.Selected {
		idx := sel.Media - 1
		if idx < 0 || idx >= len(allMediaFiles) {
			continue
		}
		key := s3Keys[idx]
		thumbKey := fmt.Sprintf("%s/thumbnails/%s.jpg", job.sessionID, strings.TrimSuffix(filepath.Base(key), filepath.Ext(key)))
		job.selected = append(job.selected, selectionResultItem{
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
			continue
		}
		key := s3Keys[idx]
		thumbKey := fmt.Sprintf("%s/thumbnails/%s.jpg", job.sessionID, strings.TrimSuffix(filepath.Base(key), filepath.Ext(key)))
		job.excluded = append(job.excluded, selectionExcludedItem{
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
		group := selectionSceneGroup{
			Name:      sg.Name,
			GPS:       sg.GPS,
			TimeRange: sg.TimeRange,
		}
		for _, item := range sg.Items {
			idx := item.Media - 1
			if idx < 0 || idx >= len(allMediaFiles) {
				continue
			}
			key := s3Keys[idx]
			thumbKey := fmt.Sprintf("%s/thumbnails/%s.jpg", job.sessionID, strings.TrimSuffix(filepath.Base(key), filepath.Ext(key)))
			group.Items = append(group.Items, selectionSceneGroupItem{
				Media:        item.Media,
				Filename:     item.Filename,
				Key:          key,
				Type:         item.Type,
				Selected:     item.Selected,
				Description:  item.Description,
				ThumbnailURL: fmt.Sprintf("/api/media/thumbnail?key=%s", thumbKey),
			})
		}
		job.sceneGroups = append(job.sceneGroups, group)
	}
	job.status = "complete"
	job.mu.Unlock()

	log.Info().
		Int("selected", len(job.selected)).
		Int("excluded", len(job.excluded)).
		Int("scenes", len(job.sceneGroups)).
		Msg("Selection job complete")
}

// preGenerateThumbnails generates thumbnails for all media files and uploads them to S3.
// Thumbnails are stored at {sessionId}/thumbnails/{filename}.jpg for fast serving.
// Uses goroutines for parallel generation. See DDR-030.
func preGenerateThumbnails(ctx context.Context, sessionID string, files []*filehandler.MediaFile, s3Keys []string) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 10) // Max 10 concurrent thumbnail uploads

	for i, mf := range files {
		wg.Add(1)
		go func(idx int, mediaFile *filehandler.MediaFile, key string) {
			defer wg.Done()
			sem <- struct{}{}        // Acquire semaphore
			defer func() { <-sem }() // Release semaphore

			filename := filepath.Base(key)
			baseName := strings.TrimSuffix(filename, filepath.Ext(filename))
			thumbKey := fmt.Sprintf("%s/thumbnails/%s.jpg", sessionID, baseName)

			// Generate thumbnail (400px for caching — frontend display size)
			thumbData, _, err := filehandler.GenerateThumbnail(mediaFile, 400)
			if err != nil {
				log.Warn().Err(err).Str("file", filename).Msg("Failed to generate thumbnail for S3 cache")
				return
			}

			// Upload to S3
			contentType := "image/jpeg"
			_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
				Bucket:      &mediaBucket,
				Key:         &thumbKey,
				Body:        bytes.NewReader(thumbData),
				ContentType: &contentType,
			})
			if err != nil {
				log.Warn().Err(err).Str("key", thumbKey).Msg("Failed to upload thumbnail to S3")
				return
			}

			log.Debug().
				Str("file", filename).
				Str("thumbKey", thumbKey).
				Int("size", len(thumbData)).
				Msg("Thumbnail cached in S3")
		}(i, mf, s3Keys[i])
	}

	wg.Wait()
	log.Info().Int("count", len(files)).Msg("Thumbnail pre-generation complete")
}
