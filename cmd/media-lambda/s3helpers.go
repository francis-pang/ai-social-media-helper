package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/rs/zerolog/log"
)

// --- S3 Helpers ---

// downloadFromS3 downloads an S3 object to a temp file and returns its path
// and a cleanup function. Caller must defer cleanup().
func downloadFromS3(ctx context.Context, key string) (string, func(), error) {
	log.Debug().Str("key", key).Msg("Starting S3 download")

	tmpFile, err := os.CreateTemp("", "media-*"+filepath.Ext(key))
	if err != nil {
		return "", nil, fmt.Errorf("create temp file: %w", err)
	}

	result, err := s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &mediaBucket,
		Key:    &key,
	})
	if err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return "", nil, fmt.Errorf("S3 GetObject: %w", err)
	}
	defer result.Body.Close()

	if _, err := io.Copy(tmpFile, result.Body); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return "", nil, fmt.Errorf("download: %w", err)
	}
	tmpFile.Close()

	fileInfo, _ := os.Stat(tmpFile.Name())
	fileSize := int64(0)
	if fileInfo != nil {
		fileSize = fileInfo.Size()
	}
	log.Debug().Str("key", key).Int64("fileSize", fileSize).Msg("S3 download completed")

	cleanup := func() { os.Remove(tmpFile.Name()) }
	return tmpFile.Name(), cleanup, nil
}

// downloadToFile downloads an S3 object to a specific local path.
func downloadToFile(ctx context.Context, key, localPath string) error {
	log.Debug().Str("key", key).Str("localPath", localPath).Msg("Starting S3 download to file")

	result, err := s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &mediaBucket,
		Key:    &key,
	})
	if err != nil {
		return fmt.Errorf("S3 GetObject: %w", err)
	}
	defer result.Body.Close()

	f, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, result.Body); err != nil {
		return fmt.Errorf("download: %w", err)
	}

	log.Debug().Str("key", key).Str("localPath", localPath).Msg("S3 download to file completed")
	return nil
}

// uploadCompressedVideo uploads a compressed video file to S3 at {sessionId}/compressed/{filename}.webm
// Returns the S3 key of the uploaded file.
func uploadCompressedVideo(ctx context.Context, sessionID, originalKey, compressedPath string) (string, error) {
	// Extract filename from original key
	filename := filepath.Base(originalKey)
	// Change extension to .webm
	baseName := strings.TrimSuffix(filename, filepath.Ext(filename))
	compressedFilename := baseName + ".webm"
	
	compressedKey := fmt.Sprintf("%s/compressed/%s", sessionID, compressedFilename)
	
	log.Debug().
		Str("original_key", originalKey).
		Str("compressed_key", compressedKey).
		Str("compressed_path", compressedPath).
		Msg("Uploading compressed video to S3")
	
	// Open the compressed file
	compressedFile, err := os.Open(compressedPath)
	if err != nil {
		return "", fmt.Errorf("failed to open compressed file: %w", err)
	}
	defer compressedFile.Close()
	
	// Upload to S3
	contentType := "video/webm"
	_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      &mediaBucket,
		Key:         &compressedKey,
		Body:        compressedFile,
		ContentType: &contentType,
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload compressed video to S3: %w", err)
	}
	
	log.Info().
		Str("compressed_key", compressedKey).
		Msg("Compressed video uploaded to S3")
	
	return compressedKey, nil
}

// cleanupS3Prefix deletes all objects under {sessionId}/{prefix} in the media bucket.
// Best-effort — errors are logged but do not affect the invalidation response.
// Orphaned files are cleaned by the bucket's 24-hour lifecycle policy (DDR-035).
func cleanupS3Prefix(sessionID, prefix string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	fullPrefix := sessionID + "/" + prefix
	input := &s3.ListObjectsV2Input{
		Bucket: aws.String(mediaBucket),
		Prefix: aws.String(fullPrefix),
	}

	result, err := s3Client.ListObjectsV2(ctx, input)
	if err != nil {
		log.Warn().Err(err).Str("prefix", fullPrefix).Msg("Failed to list S3 objects for cleanup")
		return
	}

	deleted := 0
	for _, obj := range result.Contents {
		log.Debug().Str("key", *obj.Key).Msg("Found S3 object during cleanup listing")
		_, delErr := s3Client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(mediaBucket),
			Key:    obj.Key,
		})
		if delErr != nil {
			log.Warn().Err(delErr).Str("key", *obj.Key).Msg("Failed to delete S3 object during cleanup")
			continue
		}
		deleted++
	}

	if deleted > 0 {
		log.Info().Str("prefix", fullPrefix).Int("deleted", deleted).Msg("S3 cleanup completed")
	}
}
