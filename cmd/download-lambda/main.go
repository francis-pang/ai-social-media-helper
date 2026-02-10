// Package main provides a Lambda entry point for download bundle creation (DDR-053).
//
// This Lambda creates ZIP bundles of selected media for download.
// It separates images and videos, creates zstd-compressed ZIPs,
// uploads them to S3, and returns presigned download URLs.
//
// This is the leanest Lambda: no Gemini API, no Instagram, no chat package.
//
// Invoked asynchronously by the API Lambda via lambda:Invoke (Event type).
//
// Container: Light (Dockerfile.light — no ffmpeg needed)
// Memory: 2 GB
// Timeout: 10 minutes
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

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/klauspost/compress/zstd"
	"github.com/rs/zerolog/log"

	"github.com/fpang/gemini-media-cli/internal/lambdaboot"
	"github.com/fpang/gemini-media-cli/internal/logging"
	"github.com/fpang/gemini-media-cli/internal/store"
)

var coldStart = true

var (
	s3Client     *s3.Client
	presigner    *s3.PresignClient
	mediaBucket  string
	sessionStore *store.DynamoStore
)

// zipMethodZstd is the ZIP compression method ID for Zstandard.
const zipMethodZstd uint16 = 93

// maxVideoZipBytes is the maximum size of a single video ZIP bundle (375 MB).
const maxVideoZipBytes int64 = 375 * 1024 * 1024

func init() {
	initStart := time.Now()
	logging.Init()

	awsClients := lambdaboot.InitAWS()
	s3s := lambdaboot.InitS3(awsClients.Config, "MEDIA_BUCKET_NAME")
	s3Client = s3s.Client
	presigner = s3s.Presigner
	mediaBucket = s3s.Bucket
	sessionStore = lambdaboot.InitDynamo(awsClients.Config, "DYNAMO_TABLE_NAME")

	// Register Zstandard compressor for ZIP bundles (DDR-034).
	zip.RegisterCompressor(zipMethodZstd, func(w io.Writer) (io.WriteCloser, error) {
		return zstd.NewWriter(w, zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(12)))
	})

	lambdaboot.StartupLog("download-lambda", initStart).
		S3Bucket("mediaBucket", mediaBucket).
		DynamoTable("sessions", os.Getenv("DYNAMO_TABLE_NAME")).
		Log()
}

func main() {
	lambda.Start(handler)
}

// DownloadEvent is the input from the API Lambda.
type DownloadEvent struct {
	Type       string   `json:"type"`
	SessionID  string   `json:"sessionId"`
	JobID      string   `json:"jobId"`
	Keys       []string `json:"keys"`
	GroupLabel string   `json:"groupLabel,omitempty"`
}

func handler(ctx context.Context, event DownloadEvent) error {
	if coldStart {
		coldStart = false
		log.Info().Str("function", "download-lambda").Msg("Cold start — first invocation")
	}
	log.Info().
		Str("sessionId", event.SessionID).
		Str("jobId", event.JobID).
		Int("keyCount", len(event.Keys)).
		Msg("Download Lambda invoked")

	return handleDownload(ctx, event)
}

func handleDownload(ctx context.Context, event DownloadEvent) error {
	jobStart := time.Now()
	sessionStore.PutDownloadJob(ctx, event.SessionID, &store.DownloadJob{
		ID: event.JobID, Status: "processing",
	})

	// Step 1: Query file sizes and separate images from videos.
	var images, videos []dlFile

	for _, key := range event.Keys {
		headResult, err := s3Client.HeadObject(ctx, &s3.HeadObjectInput{
			Bucket: &mediaBucket, Key: &key,
		})
		if err != nil {
			log.Warn().Err(err).Str("key", key).Msg("HeadObject failed, skipping")
			continue
		}
		size := *headResult.ContentLength
		ext := strings.ToLower(filepath.Ext(key))
		if isVideoExt(ext) {
			videos = append(videos, dlFile{key: key, size: size})
		} else {
			images = append(images, dlFile{key: key, size: size})
		}
	}

	if len(images) == 0 && len(videos) == 0 {
		return setDownloadError(ctx, event, "No downloadable files found")
	}

	log.Debug().Int("images", len(images)).Int("videos", len(videos)).Str("jobId", event.JobID).Msg("Bundle planning")

	// Step 2: Plan bundles.
	var bundles []store.DownloadBundle

	if len(images) > 0 {
		var totalSize int64
		for _, img := range images {
			totalSize += img.size
		}
		bundles = append(bundles, store.DownloadBundle{
			Type: "images", Name: sanitizeZipName(event.GroupLabel, "images", 0),
			FileCount: len(images), TotalSize: totalSize, Status: "pending",
		})
	}

	if len(videos) > 0 {
		videoGroups := dlGroupBySize(videos, maxVideoZipBytes)
		for i, group := range videoGroups {
			var totalSize int64
			for _, v := range group {
				totalSize += v.size
			}
			bundles = append(bundles, store.DownloadBundle{
				Type: "videos", Name: sanitizeZipName(event.GroupLabel, "videos", i+1),
				FileCount: len(group), TotalSize: totalSize, Status: "pending",
			})
		}
	}

	// Step 3: Create each ZIP bundle.
	videoGroupIdx := 0
	videoGroups := dlGroupBySize(videos, maxVideoZipBytes)

	for i := range bundles {
		bundles[i].Status = "processing"

		var filesToZip []dlFile
		if bundles[i].Type == "images" {
			filesToZip = images
		} else {
			filesToZip = videoGroups[videoGroupIdx]
			videoGroupIdx++
		}

		zipKey := fmt.Sprintf("%s/downloads/%s/%s", event.SessionID, event.JobID, bundles[i].Name)
		zipSize, err := dlCreateZip(ctx, filesToZip, zipKey)
		if err != nil {
			bundles[i].Status = "error"
			bundles[i].Error = err.Error()
			continue
		}

		downloadResult, err := presigner.PresignGetObject(ctx, &s3.GetObjectInput{
			Bucket:                     &mediaBucket,
			Key:                        &zipKey,
			ResponseContentDisposition: aws.String(fmt.Sprintf(`attachment; filename="%s"`, bundles[i].Name)),
		}, s3.WithPresignExpires(1*time.Hour))
		if err != nil {
			bundles[i].Status = "error"
			bundles[i].Error = "failed to generate download URL"
			continue
		}

		bundles[i].ZipKey = zipKey
		bundles[i].ZipSize = zipSize
		bundles[i].DownloadURL = downloadResult.URL
		bundles[i].Status = "complete"
	}

	sessionStore.PutDownloadJob(ctx, event.SessionID, &store.DownloadJob{
		ID: event.JobID, Status: "complete", Bundles: bundles,
	})

	log.Info().Str("job", event.JobID).Int("bundles", len(bundles)).Dur("duration", time.Since(jobStart)).Msg("Download job complete")
	return nil
}

func setDownloadError(ctx context.Context, event DownloadEvent, msg string) error {
	log.Error().Str("job", event.JobID).Str("error", msg).Msg("Download job failed")
	sessionStore.PutDownloadJob(ctx, event.SessionID, &store.DownloadJob{
		ID: event.JobID, Status: "error", Error: msg,
	})
	return nil
}

// isVideoExt checks if a file extension is a video format.
// Inlined to avoid importing filehandler (which pulls in genai via LoadMediaFile).
func isVideoExt(ext string) bool {
	switch ext {
	case ".mp4", ".mov", ".avi", ".webm", ".mkv", ".m4v", ".3gp":
		return true
	}
	return false
}

// --- ZIP Helpers ---

type dlFile struct {
	key  string
	size int64
}

func dlGroupBySize(files []dlFile, maxBytes int64) [][]dlFile {
	if len(files) == 0 {
		return nil
	}

	sorted := make([]dlFile, len(files))
	copy(sorted, files)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].size > sorted[j].size
	})

	var groups [][]dlFile
	groupSizes := []int64{}

	for _, file := range sorted {
		if file.size > maxBytes {
			groups = append(groups, []dlFile{file})
			groupSizes = append(groupSizes, file.size)
			continue
		}
		placed := false
		for i, currentSize := range groupSizes {
			if currentSize+file.size <= maxBytes {
				groups[i] = append(groups[i], file)
				groupSizes[i] += file.size
				placed = true
				break
			}
		}
		if !placed {
			groups = append(groups, []dlFile{file})
			groupSizes = append(groupSizes, file.size)
		}
	}
	return groups
}

func dlCreateZip(ctx context.Context, files []dlFile, zipKey string) (int64, error) {
	tmpFile, err := os.CreateTemp("", "download-*.zip")
	if err != nil {
		return 0, fmt.Errorf("create temp ZIP: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	zipWriter := zip.NewWriter(tmpFile)

	for _, file := range files {
		filename := filepath.Base(file.key)
		getResult, err := s3Client.GetObject(ctx, &s3.GetObjectInput{
			Bucket: &mediaBucket, Key: &file.key,
		})
		if err != nil {
			log.Warn().Err(err).Str("key", file.key).Msg("Failed to download for ZIP, skipping")
			continue
		}

		header := &zip.FileHeader{
			Name:   filename,
			Method: zipMethodZstd,
		}
		header.SetModTime(time.Now())

		writer, err := zipWriter.CreateHeader(header)
		if err != nil {
			getResult.Body.Close()
			return 0, fmt.Errorf("create ZIP entry for %s: %w", filename, err)
		}
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

	info, err := os.Stat(tmpPath)
	if err != nil {
		return 0, fmt.Errorf("stat ZIP file: %w", err)
	}
	zipSize := info.Size()

	zipFile, err := os.Open(tmpPath)
	if err != nil {
		return 0, fmt.Errorf("open ZIP for upload: %w", err)
	}
	defer zipFile.Close()

	contentType := "application/zip"
	_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: &mediaBucket, Key: &zipKey,
		Body: zipFile, ContentType: &contentType,
	})
	if err != nil {
		return 0, fmt.Errorf("upload ZIP to S3: %w", err)
	}

	return zipSize, nil
}

func sanitizeZipName(groupLabel, bundleType string, index int) string {
	name := groupLabel
	if name == "" {
		name = "media"
	}
	name = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == ' ' {
			return r
		}
		return '-'
	}, name)
	name = strings.TrimSpace(name)
	if len(name) > 50 {
		name = name[:50]
	}
	if bundleType == "images" {
		return fmt.Sprintf("%s-images.zip", name)
	}
	return fmt.Sprintf("%s-videos-%d.zip", name, index)
}
