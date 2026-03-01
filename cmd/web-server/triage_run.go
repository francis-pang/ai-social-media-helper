package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/fpang/ai-social-media-helper/internal/auth"
	"github.com/fpang/ai-social-media-helper/internal/ai"
	"github.com/fpang/ai-social-media-helper/internal/media"
	"github.com/rs/zerolog/log"
)

// runTriageJob uses the existing AskMediaTriage function from the chat package,
// matching the same pattern as the media-triage CLI.
func runTriageJob(job *triageJob, model string) {
	job.mu.Lock()
	job.status = "processing"
	job.mu.Unlock()

	ctx := context.Background()

	if err := ai.LoadGCPServiceAccount(); err != nil {
		setJobError(job, fmt.Sprintf("GCP service account error: %v", err))
		return
	}

	apiKey, err := auth.GetAPIKey()
	if err != nil {
		setJobError(job, fmt.Sprintf("API key error: %v", err))
		return
	}
	// Ensure key is in env for NewAIClient (e.g. when loaded from GPG)
	if apiKey != "" && os.Getenv("GEMINI_API_KEY") == "" {
		os.Setenv("GEMINI_API_KEY", apiKey)
	}

	client, err := ai.NewAIClient(ctx)
	if err != nil {
		setJobError(job, fmt.Sprintf("Failed to create AI client: %v", err))
		return
	}

	// Collect all media files from the provided paths
	var allMediaFiles []*media.MediaFile
	for _, p := range job.paths {
		info, err := os.Stat(p)
		if err != nil {
			log.Warn().Err(err).Str("path", p).Msg("Skipping inaccessible path")
			continue
		}
		if info.IsDir() {
			files, err := media.ScanDirectoryMediaWithOptions(p, media.ScanOptions{})
			if err != nil {
				log.Warn().Err(err).Str("path", p).Msg("Failed to scan directory")
				continue
			}
			allMediaFiles = append(allMediaFiles, files...)
		} else {
			mf, err := media.LoadMediaFile(p)
			if err != nil {
				log.Warn().Err(err).Str("path", p).Msg("Failed to load media file")
				continue
			}
			allMediaFiles = append(allMediaFiles, mf)
		}
	}

	if len(allMediaFiles) == 0 {
		setJobError(job, "No media files found in the provided paths")
		return
	}

	log.Info().Int("count", len(allMediaFiles)).Msg("Starting web triage evaluation")

	// Pre-filter short videos (same logic as media-triage CLI)
	var mediaForAI []*media.MediaFile
	for _, mf := range allMediaFiles {
		if mf.Metadata != nil && mf.Metadata.GetMediaType() == "video" {
			if vm, ok := mf.Metadata.(*media.VideoMetadata); ok && vm.Duration < 2.0 {
				job.mu.Lock()
				job.discard = append(job.discard, triageResultItem{
					Media:        0,
					Filename:     filepath.Base(mf.Path),
					Path:         mf.Path,
					Saveable:     false,
					Reason:       "Video too short — likely accidental recording",
					ThumbnailURL: "/api/media/thumbnail?path=" + url.QueryEscape(mf.Path),
				})
				job.mu.Unlock()
				continue
			}
		}
		mediaForAI = append(mediaForAI, mf)
	}

	if len(mediaForAI) == 0 {
		// All files were pre-filtered
		job.mu.Lock()
		job.status = "complete"
		job.mu.Unlock()
		return
	}

	// Use the existing AskMediaTriage function from the chat package
	// Local mode: no sessionID, no S3 storage
	output, err := ai.AskMediaTriage(ctx, client, mediaForAI, model, "", nil, nil, nil, "", false)
	if err != nil {
		setJobError(job, fmt.Sprintf("Triage failed: %v", err))
		return
	}
	triageResults := output.Results

	// Map results to items with paths and thumbnail URLs
	job.mu.Lock()
	seen := make(map[int]bool) // track which media indices got a verdict
	for _, tr := range triageResults {
		idx := tr.Media - 1
		if idx < 0 || idx >= len(mediaForAI) {
			continue
		}
		seen[idx] = true
		mf := mediaForAI[idx]
		item := triageResultItem{
			Media:        tr.Media,
			Filename:     tr.Filename,
			Path:         mf.Path,
			Saveable:     tr.Saveable,
			Reason:       tr.Reason,
			ThumbnailURL: "/api/media/thumbnail?path=" + url.QueryEscape(mf.Path),
		}
		if tr.Saveable {
			job.keep = append(job.keep, item)
		} else {
			job.discard = append(job.discard, item)
		}
	}

	// Safety net: any media items missing from the AI response default to "keep".
	for i, mf := range mediaForAI {
		if !seen[i] {
			log.Warn().
				Int("media", i+1).
				Str("filename", filepath.Base(mf.Path)).
				Msg("Media item missing from AI triage results — defaulting to keep")
			job.keep = append(job.keep, triageResultItem{
				Media:        i + 1,
				Filename:     filepath.Base(mf.Path),
				Path:         mf.Path,
				Saveable:     true,
				Reason:       "Not evaluated by AI — kept by default",
				ThumbnailURL: "/api/media/thumbnail?path=" + url.QueryEscape(mf.Path),
			})
		}
	}

	job.status = "complete"
	job.mu.Unlock()

	log.Info().
		Int("keep", len(job.keep)).
		Int("discard", len(job.discard)).
		Msg("Web triage complete")
}

func setJobError(job *triageJob, msg string) {
	job.mu.Lock()
	defer job.mu.Unlock()
	job.status = "error"
	job.errMsg = msg
	log.Error().Str("job", job.id).Str("error", msg).Msg("Triage job failed")
}

func isValidDeletePath(job *triageJob, path string) bool {
	job.mu.Lock()
	defer job.mu.Unlock()
	for _, item := range job.discard {
		if item.Path == path {
			return true
		}
	}
	return false
}
