package s3util

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/rs/zerolog/log"
)

// UploadCompressedVideo uploads a locally compressed video file to S3 under a
// "compressed/" prefix, changing the extension to .webm.
// Replaces uploadCompressedVideo in triage-lambda and selection-lambda.
func UploadCompressedVideo(ctx context.Context, client *s3.Client, bucket, sessionID, originalKey, compressedPath string) (string, error) {
	filename := filepath.Base(originalKey)
	baseName := strings.TrimSuffix(filename, filepath.Ext(filename))
	compressedFilename := baseName + ".webm"
	compressedKey := fmt.Sprintf("%s/compressed/%s", sessionID, compressedFilename)

	log.Debug().
		Str("original_key", originalKey).
		Str("compressed_key", compressedKey).
		Str("compressed_path", compressedPath).
		Msg("Uploading compressed video to S3")

	compressedFile, err := os.Open(compressedPath)
	if err != nil {
		return "", fmt.Errorf("failed to open compressed file: %w", err)
	}
	defer compressedFile.Close()

	contentType := "video/webm"
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      &bucket,
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

// GeneratePresignedURL creates a pre-signed GET URL for an S3 object.
// Replaces generatePresignedURL in triage-lambda and selection-lambda.
func GeneratePresignedURL(ctx context.Context, presignClient *s3.PresignClient, bucket, key string, expiry time.Duration) (string, error) {
	result, err := presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: &bucket, Key: &key,
	}, func(opts *s3.PresignOptions) {
		opts.Expires = expiry
	})
	if err != nil {
		return "", fmt.Errorf("presign GetObject: %w", err)
	}
	return result.URL, nil
}
