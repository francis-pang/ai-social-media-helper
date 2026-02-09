package main

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/klauspost/compress/zstd"
	"github.com/rs/zerolog/log"

	"github.com/fpang/gemini-media-cli/internal/filehandler"
)

// zipMethodZstd is the ZIP compression method ID for Zstandard (APPNOTE 6.3.7).
// Registered in init() with zstd level 12 (SpeedBestCompression in klauspost/compress).
// Requires 2+ GB Lambda memory due to zstd encoder window size at high levels.
const zipMethodZstd uint16 = 93

func init() {
	// Register Zstandard (zstd) as a ZIP compressor at level 12 (DDR-034).
	// Level 12 maps to SpeedBestCompression in klauspost/compress — the highest
	// compression the Go library supports. This trades CPU time for smaller ZIPs.
	// Requires Lambda memory ≥ 2 GB due to zstd encoder window size.
	zip.RegisterCompressor(zipMethodZstd, func(w io.Writer) (io.WriteCloser, error) {
		return zstd.NewWriter(w, zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(12)))
	})
}

// maxVideoZipBytes is the maximum size of a single video ZIP bundle.
// Calculated as 30 seconds × 100 Mbps ÷ 8 = 375 MB.
// Based on AT&T Internet Air typical download speed in San Jose (~100 Mbps).
const maxVideoZipBytes int64 = 375 * 1024 * 1024

// runDownloadJob creates ZIP bundles for the given media keys.
// Strategy: 1 ZIP for all images, videos split into bundles ≤ 375 MB each.
func runDownloadJob(job *downloadJob, keys []string, groupLabel string) {
	job.mu.Lock()
	job.status = "processing"
	job.mu.Unlock()

	ctx := context.Background()

	// Step 1: Query file sizes via HeadObject and separate images from videos
	var images []fileWithSize
	var videos []fileWithSize

	for _, key := range keys {
		headResult, err := s3Client.HeadObject(ctx, &s3.HeadObjectInput{
			Bucket: &mediaBucket,
			Key:    &key,
		})
		if err != nil {
			log.Warn().Err(err).Str("key", key).Msg("HeadObject failed, skipping file")
			continue
		}

		size := *headResult.ContentLength
		ext := strings.ToLower(filepath.Ext(key))

		if filehandler.IsVideo(ext) {
			videos = append(videos, fileWithSize{key: key, size: size})
		} else {
			images = append(images, fileWithSize{key: key, size: size})
		}
	}

	if len(images) == 0 && len(videos) == 0 {
		setDownloadJobError(job, "No downloadable files found")
		return
	}

	// Step 2: Plan bundles
	var bundles []downloadBundle

	// All images go into one ZIP
	if len(images) > 0 {
		var totalSize int64
		for _, img := range images {
			totalSize += img.size
		}
		bundles = append(bundles, downloadBundle{
			Type:      "images",
			Name:      sanitizeZipName(groupLabel, "images", 0),
			FileCount: len(images),
			TotalSize: totalSize,
			Status:    "pending",
		})
	}

	// Videos grouped into bundles ≤ maxVideoZipBytes (375 MB)
	if len(videos) > 0 {
		videoGroups := groupVideosBySize(videos, maxVideoZipBytes)
		for i, group := range videoGroups {
			var totalSize int64
			for _, v := range group {
				totalSize += v.size
			}
			bundles = append(bundles, downloadBundle{
				Type:      "videos",
				Name:      sanitizeZipName(groupLabel, "videos", i+1),
				FileCount: len(group),
				TotalSize: totalSize,
				Status:    "pending",
			})
		}
	}

	// Store initial bundle plan
	job.mu.Lock()
	job.bundles = bundles
	job.mu.Unlock()

	log.Info().
		Int("images", len(images)).
		Int("videos", len(videos)).
		Int("bundles", len(bundles)).
		Str("job", job.id).
		Msg("Download bundle plan created")

	// Step 3: Create each ZIP bundle
	// Track which video group index we're on
	videoGroupIdx := 0
	videoGroups := groupVideosBySize(videos, maxVideoZipBytes)

	for i := range bundles {
		job.mu.Lock()
		job.bundles[i].Status = "processing"
		job.mu.Unlock()

		var filesToZip []fileWithSize
		if bundles[i].Type == "images" {
			filesToZip = images
		} else {
			filesToZip = videoGroups[videoGroupIdx]
			videoGroupIdx++
		}

		zipKey := fmt.Sprintf("%s/downloads/%s/%s", job.sessionID, job.id, bundles[i].Name)

		zipSize, err := createZipBundle(ctx, filesToZip, zipKey)
		if err != nil {
			job.mu.Lock()
			job.bundles[i].Status = "error"
			job.bundles[i].Error = err.Error()
			job.mu.Unlock()
			log.Error().Err(err).Str("bundle", bundles[i].Name).Msg("Failed to create ZIP bundle")
			continue
		}

		// Generate presigned download URL (1 hour expiry)
		downloadResult, err := presigner.PresignGetObject(ctx, &s3.GetObjectInput{
			Bucket:                     &mediaBucket,
			Key:                        &zipKey,
			ResponseContentDisposition: aws.String(fmt.Sprintf(`attachment; filename="%s"`, bundles[i].Name)),
		}, s3.WithPresignExpires(1*time.Hour))
		if err != nil {
			job.mu.Lock()
			job.bundles[i].Status = "error"
			job.bundles[i].Error = "failed to generate download URL"
			job.mu.Unlock()
			log.Error().Err(err).Str("key", zipKey).Msg("Failed to generate presigned GET URL for ZIP")
			continue
		}

		job.mu.Lock()
		job.bundles[i].ZipKey = zipKey
		job.bundles[i].ZipSize = zipSize
		job.bundles[i].DownloadURL = downloadResult.URL
		job.bundles[i].Status = "complete"
		job.mu.Unlock()

		log.Info().
			Str("bundle", bundles[i].Name).
			Int64("zipSize", zipSize).
			Int("files", len(filesToZip)).
			Msg("ZIP bundle created")
	}

	// Mark job complete
	job.mu.Lock()
	allComplete := true
	for _, b := range job.bundles {
		if b.Status != "complete" && b.Status != "error" {
			allComplete = false
			break
		}
	}
	if allComplete {
		job.status = "complete"
	}
	job.mu.Unlock()

	log.Info().
		Str("job", job.id).
		Int("bundles", len(bundles)).
		Msg("Download job complete")
}

// groupVideosBySize groups videos into bundles where each bundle's total size ≤ maxBytes.
// Videos larger than maxBytes get their own bundle.
// Uses a first-fit-decreasing bin packing heuristic for better packing.
func groupVideosBySize(videos []fileWithSize, maxBytes int64) [][]fileWithSize {
	if len(videos) == 0 {
		return nil
	}

	// Sort videos by size descending (first-fit-decreasing)
	sorted := make([]fileWithSize, len(videos))
	copy(sorted, videos)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].size > sorted[j].size
	})

	var groups [][]fileWithSize
	groupSizes := []int64{}

	for _, video := range sorted {
		placed := false

		// If the video itself exceeds maxBytes, it gets its own group
		if video.size > maxBytes {
			groups = append(groups, []fileWithSize{video})
			groupSizes = append(groupSizes, video.size)
			continue
		}

		// Try to fit into an existing group
		for i, currentSize := range groupSizes {
			if currentSize+video.size <= maxBytes {
				groups[i] = append(groups[i], video)
				groupSizes[i] += video.size
				placed = true
				break
			}
		}

		// If it doesn't fit anywhere, create a new group
		if !placed {
			groups = append(groups, []fileWithSize{video})
			groupSizes = append(groupSizes, video.size)
		}
	}

	return groups
}

// createZipBundle downloads files from S3, creates a zstd-compressed ZIP in /tmp,
// and uploads it to S3. Uses Zstandard level 12 compression (DDR-034).
// Returns the size of the created ZIP file.
func createZipBundle(ctx context.Context, files []fileWithSize, zipKey string) (int64, error) {
	// Create temp file for the ZIP
	tmpFile, err := os.CreateTemp("", "download-*.zip")
	if err != nil {
		return 0, fmt.Errorf("create temp ZIP: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	// Create ZIP writer
	zipWriter := zip.NewWriter(tmpFile)

	for _, file := range files {
		filename := filepath.Base(file.key)

		// Download file from S3
		getResult, err := s3Client.GetObject(ctx, &s3.GetObjectInput{
			Bucket: &mediaBucket,
			Key:    &file.key,
		})
		if err != nil {
			log.Warn().Err(err).Str("key", file.key).Msg("Failed to download file for ZIP, skipping")
			continue
		}

		// Create ZIP entry
		header := &zip.FileHeader{
			Name:   filename,
			Method: zipMethodZstd, // Zstandard level 12 compression (DDR-034)
		}
		header.SetModTime(time.Now())

		writer, err := zipWriter.CreateHeader(header)
		if err != nil {
			getResult.Body.Close()
			return 0, fmt.Errorf("create ZIP entry for %s: %w", filename, err)
		}

		// Stream from S3 response directly into ZIP
		if _, err := io.Copy(writer, getResult.Body); err != nil {
			getResult.Body.Close()
			return 0, fmt.Errorf("write to ZIP for %s: %w", filename, err)
		}
		getResult.Body.Close()
	}

	if err := zipWriter.Close(); err != nil {
		tmpFile.Close()
		return 0, fmt.Errorf("close ZIP writer: %w", err)
	}
	tmpFile.Close()

	// Get ZIP file size
	info, err := os.Stat(tmpPath)
	if err != nil {
		return 0, fmt.Errorf("stat ZIP file: %w", err)
	}
	zipSize := info.Size()

	// Upload ZIP to S3
	zipFile, err := os.Open(tmpPath)
	if err != nil {
		return 0, fmt.Errorf("open ZIP for upload: %w", err)
	}
	defer zipFile.Close()

	contentType := "application/zip"
	_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      &mediaBucket,
		Key:         &zipKey,
		Body:        zipFile,
		ContentType: &contentType,
	})
	if err != nil {
		return 0, fmt.Errorf("upload ZIP to S3: %w", err)
	}

	return zipSize, nil
}

// sanitizeZipName creates a ZIP filename from the group label and bundle type.
func sanitizeZipName(groupLabel, bundleType string, index int) string {
	// Clean the group label for use in filenames
	name := groupLabel
	if name == "" {
		name = "media"
	}

	// Replace unsafe characters
	name = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == ' ' {
			return r
		}
		return '-'
	}, name)
	name = strings.TrimSpace(name)

	// Truncate to reasonable length
	if len(name) > 50 {
		name = name[:50]
	}

	if bundleType == "images" {
		return fmt.Sprintf("%s-images.zip", name)
	}
	return fmt.Sprintf("%s-videos-%d.zip", name, index)
}
