package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/rs/zerolog/log"
	"google.golang.org/genai"

	"github.com/fpang/gemini-media-cli/internal/filehandler"
)

// uploadVideoToGemini uploads a local video file to the Gemini Files API and
// waits for it to finish processing. Returns the File object whose URI can be
// used in FileData.FileURI for GenerateContent calls. The caller is responsible
// for deleting the uploaded file after use via client.Files.Delete.
func uploadVideoToGemini(ctx context.Context, client *genai.Client, localPath, mimeType string) (*genai.File, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat file: %w", err)
	}

	log.Debug().
		Str("path", localPath).
		Int64("size_bytes", info.Size()).
		Str("mime_type", mimeType).
		Msg("Starting Gemini Files API upload for large video")

	uploadStart := time.Now()
	file, err := client.Files.Upload(ctx, f, &genai.UploadFileConfig{
		MIMEType: mimeType,
	})
	if err != nil {
		return nil, fmt.Errorf("upload file: %w", err)
	}

	log.Debug().
		Str("name", file.Name).
		Str("uri", file.URI).
		Dur("upload_duration", time.Since(uploadStart)).
		Msg("Video uploaded to Gemini, waiting for processing...")

	// Poll until the file is ACTIVE (processed) or FAILED.
	const pollInterval = 5 * time.Second
	const pollTimeout = 5 * time.Minute
	deadline := time.Now().Add(pollTimeout)

	for file.State == genai.FileStateProcessing {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout waiting for Gemini file processing after %v", pollTimeout)
		}
		time.Sleep(pollInterval)
		file, err = client.Files.Get(ctx, file.Name, nil)
		if err != nil {
			return nil, fmt.Errorf("get file state: %w", err)
		}
	}

	if file.State == genai.FileStateFailed {
		return nil, fmt.Errorf("Gemini file processing failed: %s", file.Name)
	}

	log.Info().
		Str("name", file.Name).
		Str("uri", file.URI).
		Str("state", string(file.State)).
		Dur("total_duration", time.Since(uploadStart)).
		Msg("Gemini Files API upload complete")

	return file, nil
}

// interleaveMedia distributes videos evenly among images so that triage
// batches receive a balanced mix of media types. Given 20 videos and 49
// images, interleaving produces a sequence like [V, I, I, V, I, I, V, I, I, ...]
// ensuring each batch of 20 items contains roughly 6 videos and 14 images
// instead of one all-video batch that overwhelms the Gemini API.
//
// Both slices must have the same length; pathToKeyMap is keyed by local path
// and does not need reordering.
func interleaveMedia(files []*filehandler.MediaFile, keys []string) ([]*filehandler.MediaFile, []string) {
	// Separate into videos and images, preserving their paired keys.
	type entry struct {
		file *filehandler.MediaFile
		key  string
	}
	var videos, images []entry
	for i, mf := range files {
		ext := strings.ToLower(filepath.Ext(mf.Path))
		if filehandler.IsVideo(ext) {
			videos = append(videos, entry{mf, keys[i]})
		} else {
			images = append(images, entry{mf, keys[i]})
		}
	}

	if len(videos) == 0 || len(images) == 0 {
		return files, keys // Nothing to interleave.
	}

	// Distribute: for each video, emit a proportional number of images.
	imagesPerSlot := max(len(images)/len(videos), 1)

	result := make([]entry, 0, len(files))
	vi, ii := 0, 0
	for vi < len(videos) || ii < len(images) {
		if vi < len(videos) {
			result = append(result, videos[vi])
			vi++
		}
		for j := 0; j < imagesPerSlot && ii < len(images); j++ {
			result = append(result, images[ii])
			ii++
		}
	}
	// Append any remaining images.
	for ii < len(images) {
		result = append(result, images[ii])
		ii++
	}

	// Rebuild the parallel slices.
	newFiles := make([]*filehandler.MediaFile, len(result))
	newKeys := make([]string, len(result))
	for i, e := range result {
		newFiles[i] = e.file
		newKeys[i] = e.key
	}

	log.Info().
		Int("videos", len(videos)).
		Int("images", len(images)).
		Int("imagesPerSlot", imagesPerSlot).
		Msg("Media files interleaved to distribute videos across triage batches")

	return newFiles, newKeys
}

// deleteOriginals deletes the original media files from S3 after thumbnails
// have been stored. Best-effort — errors are logged but do not fail the job.
// The 1-day S3 lifecycle policy acts as a safety net (DDR-059).
func deleteOriginals(ctx context.Context, sessionID string, originalKeys []string) {
	deleted := 0
	for _, key := range originalKeys {
		// Skip keys under thumbnails/, compressed/, or processed/ — those are generated artifacts.
		parts := strings.SplitN(key, "/", 2)
		if len(parts) == 2 {
			suffix := parts[1]
			if strings.HasPrefix(suffix, "thumbnails/") || strings.HasPrefix(suffix, "compressed/") || strings.HasPrefix(suffix, "processed/") {
				continue
			}
		}

		_, err := s3Client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(mediaBucket),
			Key:    aws.String(key),
		})
		if err != nil {
			log.Warn().Err(err).Str("key", key).Msg("Failed to delete original file from S3")
			continue
		}
		deleted++
	}

	log.Info().
		Int("deleted", deleted).
		Int("total", len(originalKeys)).
		Str("sessionId", sessionID).
		Msg("Original files deleted from S3 (DDR-059)")
}
