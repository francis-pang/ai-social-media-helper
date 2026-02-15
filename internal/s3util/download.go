// Package s3util provides shared S3 helper functions used across multiple Lambda handlers.
//
// Replaces duplicated download/upload/presign/thumbnail functions found in
// triage-lambda, selection-lambda, media-process-lambda, enhance-lambda,
// and description-lambda.
package s3util

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/rs/zerolog/log"
)

// DownloadToFile downloads an S3 object to a specific local path.
// Replaces downloadToFile in triage-lambda, selection-lambda, media-process-lambda.
func DownloadToFile(ctx context.Context, client *s3.Client, bucket, key, localPath string) error {
	log.Debug().Str("bucket", bucket).Str("key", key).Str("localPath", localPath).Msg("Downloading from S3")
	result, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &bucket, Key: &key,
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

	buf := make([]byte, 32*1024)
	for {
		n, readErr := result.Body.Read(buf)
		if n > 0 {
			if _, writeErr := f.Write(buf[:n]); writeErr != nil {
				return fmt.Errorf("write: %w", writeErr)
			}
		}
		if readErr != nil {
			if readErr.Error() == "EOF" {
				break
			}
			return fmt.Errorf("download: %w", readErr)
		}
	}
	return nil
}

// DownloadToTempFile downloads an S3 object to a new temporary file and returns
// the file path plus a cleanup function that removes it.
// Replaces downloadFromS3 in enhance-lambda and description-lambda.
func DownloadToTempFile(ctx context.Context, client *s3.Client, bucket, key string) (string, func(), error) {
	tmpFile, err := os.CreateTemp("", "s3dl-*"+filepath.Ext(key))
	if err != nil {
		return "", nil, fmt.Errorf("create temp file: %w", err)
	}

	result, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return "", nil, fmt.Errorf("S3 GetObject: %w", err)
	}
	defer result.Body.Close()

	buf := make([]byte, 32*1024)
	for {
		n, readErr := result.Body.Read(buf)
		if n > 0 {
			if _, writeErr := tmpFile.Write(buf[:n]); writeErr != nil {
				tmpFile.Close()
				os.Remove(tmpFile.Name())
				return "", nil, fmt.Errorf("write: %w", writeErr)
			}
		}
		if readErr != nil {
			if readErr.Error() == "EOF" {
				break
			}
			tmpFile.Close()
			os.Remove(tmpFile.Name())
			return "", nil, fmt.Errorf("read: %w", readErr)
		}
	}
	tmpFile.Close()

	cleanup := func() { os.Remove(tmpFile.Name()) }
	return tmpFile.Name(), cleanup, nil
}
