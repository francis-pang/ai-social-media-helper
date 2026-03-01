package main

import (
	"context"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/rs/zerolog/log"
)

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
