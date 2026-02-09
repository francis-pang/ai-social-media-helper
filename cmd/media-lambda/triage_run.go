package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/fpang/gemini-media-cli/internal/chat"
	"github.com/fpang/gemini-media-cli/internal/filehandler"
	"github.com/fpang/gemini-media-cli/internal/metrics"
	"github.com/rs/zerolog/log"
)

// --- Triage Processing ---

func runTriageJob(job *triageJob, model string) {
	jobStart := time.Now()
	job.mu.Lock()
	job.status = "processing"
	job.mu.Unlock()

	ctx := context.Background()

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		setJobError(job, "GEMINI_API_KEY not configured")
		return
	}

	client, err := chat.NewGeminiClient(ctx, apiKey)
	if err != nil {
		setJobError(job, fmt.Sprintf("Failed to create Gemini client: %v", err))
		return
	}

	// List objects in the session prefix
	prefix := job.sessionID + "/"
	listResult, err := s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: &mediaBucket,
		Prefix: &prefix,
	})
	if err != nil {
		setJobError(job, fmt.Sprintf("Failed to list S3 objects: %v", err))
		return
	}

	if len(listResult.Contents) == 0 {
		setJobError(job, "No files found for session")
		return
	}

	log.Info().Int("count", len(listResult.Contents)).Str("session", job.sessionID).Msg("Found S3 objects for triage")

	// Download each file and create MediaFile objects
	tmpDir := filepath.Join(os.TempDir(), "triage", job.sessionID)
	os.MkdirAll(tmpDir, 0755)
	defer os.RemoveAll(tmpDir) // Clean up after triage

	var allMediaFiles []*filehandler.MediaFile
	var s3Keys []string // Parallel slice tracking which key maps to which MediaFile

	for _, obj := range listResult.Contents {
		key := *obj.Key
		filename := filepath.Base(key)
		ext := strings.ToLower(filepath.Ext(filename))

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
		setJobError(job, "No supported media files found in the uploaded session")
		return
	}

	log.Info().Int("count", len(allMediaFiles)).Msg("Starting Lambda triage evaluation")

	// Call the existing AskMediaTriage — reusing all Gemini interaction logic
	triageResults, err := chat.AskMediaTriage(ctx, client, allMediaFiles, model)
	if err != nil {
		setJobError(job, fmt.Sprintf("Triage failed: %v", err))
		return
	}

	// Map results to items with S3 keys and thumbnail URLs
	job.mu.Lock()
	for _, tr := range triageResults {
		idx := tr.Media - 1
		if idx < 0 || idx >= len(allMediaFiles) {
			continue
		}
		key := s3Keys[idx]
		item := triageResultItem{
			Media:        tr.Media,
			Filename:     tr.Filename,
			Key:          key,
			Saveable:     tr.Saveable,
			Reason:       tr.Reason,
			ThumbnailURL: fmt.Sprintf("/api/media/thumbnail?key=%s", key),
		}
		if tr.Saveable {
			job.keep = append(job.keep, item)
		} else {
			job.discard = append(job.discard, item)
		}
	}
	job.status = "complete"
	job.mu.Unlock()

	log.Info().
		Int("keep", len(job.keep)).
		Int("discard", len(job.discard)).
		Msg("Lambda triage complete")

	// Emit job-level EMF metrics
	metrics.New("AiSocialMedia").
		Dimension("JobType", "triage").
		Metric("JobDurationMs", float64(time.Since(jobStart).Milliseconds()), metrics.UnitMilliseconds).
		Metric("JobFilesProcessed", float64(len(allMediaFiles)), metrics.UnitCount).
		Count("JobSuccess").
		Property("jobId", job.id).
		Property("sessionId", job.sessionID).
		Flush()
}
