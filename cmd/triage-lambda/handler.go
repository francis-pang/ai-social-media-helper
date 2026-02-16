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
	"github.com/fpang/gemini-media-cli/internal/jobutil"
	"github.com/fpang/gemini-media-cli/internal/metrics"
	"github.com/fpang/gemini-media-cli/internal/s3util"
	"github.com/fpang/gemini-media-cli/internal/store"
)

// handleTriageRun reads the pre-processed file manifest from the file-processing
// table, generates presigned URLs, calls Gemini for AI triage, and writes results.
// Simplified from the original that downloaded/processed files (DDR-061).
func handleTriageRun(ctx context.Context, event TriageEvent) error {
	jobStart := time.Now()

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return jobutil.SetJobError(ctx, event.SessionID, event.JobID, "GEMINI_API_KEY not configured", func(ctx context.Context, sessionID, jobID, errMsg string) error {
			sessionStore.PutTriageJob(ctx, sessionID, &store.TriageJob{ID: jobID, Status: "error", Error: errMsg})
			return nil
		})
	}

	client, err := chat.NewGeminiClient(ctx, apiKey)
	if err != nil {
		return jobutil.SetJobError(ctx, event.SessionID, event.JobID, fmt.Sprintf("Failed to create Gemini client: %v", err), func(ctx context.Context, sessionID, jobID, errMsg string) error {
			sessionStore.PutTriageJob(ctx, sessionID, &store.TriageJob{ID: jobID, Status: "error", Error: errMsg})
			return nil
		})
	}

	// Read processed file manifest from file-processing table (DDR-061)
	if fileProcessStore == nil {
		return jobutil.SetJobError(ctx, event.SessionID, event.JobID, "File processing store not configured", func(ctx context.Context, sessionID, jobID, errMsg string) error {
			sessionStore.PutTriageJob(ctx, sessionID, &store.TriageJob{ID: jobID, Status: "error", Error: errMsg})
			return nil
		})
	}

	fileResults, err := fileProcessStore.GetFileResults(ctx, event.SessionID, event.JobID)
	if err != nil {
		return jobutil.SetJobError(ctx, event.SessionID, event.JobID, fmt.Sprintf("Failed to read file results: %v", err), func(ctx context.Context, sessionID, jobID, errMsg string) error {
			sessionStore.PutTriageJob(ctx, sessionID, &store.TriageJob{ID: jobID, Status: "error", Error: errMsg})
			return nil
		})
	}

	// Filter to valid files only
	var validFiles []store.FileResult
	for _, fr := range fileResults {
		if fr.Status == "valid" {
			validFiles = append(validFiles, fr)
		}
	}

	if len(validFiles) == 0 {
		return jobutil.SetJobError(ctx, event.SessionID, event.JobID, "No valid media files found after processing", func(ctx context.Context, sessionID, jobID, errMsg string) error {
			sessionStore.PutTriageJob(ctx, sessionID, &store.TriageJob{ID: jobID, Status: "error", Error: errMsg})
			return nil
		})
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

		mimeType := fr.MimeType
		if mimeType == "" {
			mimeType, _ = filehandler.GetMIMEType(strings.ToLower(filepath.Ext(fr.Filename)))
		}

		// For images, prefer the pre-generated thumbnail to keep the Gemini
		// payload small. Thumbnails are created by the MediaProcess Lambda.
		ext := strings.ToLower(filepath.Ext(fr.Filename))
		if filehandler.IsImage(ext) && fr.ThumbnailKey != "" {
			useKey = fr.ThumbnailKey
			if m, _ := filehandler.GetMIMEType(strings.ToLower(filepath.Ext(fr.ThumbnailKey))); m != "" {
				mimeType = m
			}
		}

		// Generate presigned URL for the file
		url, err := s3util.GeneratePresignedURL(ctx, presignClient, mediaBucket, useKey, 15*time.Minute)
		if err != nil {
			log.Warn().Err(err).Str("key", useKey).Msg("Failed to generate presigned URL")
			continue
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
		return jobutil.SetJobError(ctx, event.SessionID, event.JobID, "No media files with valid presigned URLs", func(ctx context.Context, sessionID, jobID, errMsg string) error {
			sessionStore.PutTriageJob(ctx, sessionID, &store.TriageJob{ID: jobID, Status: "error", Error: errMsg})
			return nil
		})
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
		return s3util.UploadCompressedVideo(ctx, s3Client, mediaBucket, sessionID, originalKey, compressedPath)
	}

	sessionStore.PutTriageJob(ctx, event.SessionID, &store.TriageJob{
		ID: event.JobID, Status: "processing", Phase: "analyzing",
		TotalFiles: len(allMediaFiles),
	})

	log.Debug().Int("fileCount", len(allMediaFiles)).Str("model", model).Msg("Calling AskMediaTriage (DDR-061: presigned URLs from manifest)")
	triageResults, err := chat.AskMediaTriage(ctx, client, allMediaFiles, model, event.SessionID, storeCompressed, keyMapper)
	if err != nil {
		return jobutil.SetJobError(ctx, event.SessionID, event.JobID, fmt.Sprintf("Triage failed: %v", err), func(ctx context.Context, sessionID, jobID, errMsg string) error {
			sessionStore.PutTriageJob(ctx, sessionID, &store.TriageJob{ID: jobID, Status: "error", Error: errMsg})
			return nil
		})
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
