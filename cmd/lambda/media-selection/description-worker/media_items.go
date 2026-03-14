package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/fpang/ai-social-media-helper/internal/ai"
	"github.com/fpang/ai-social-media-helper/internal/media"
	"github.com/fpang/ai-social-media-helper/internal/s3util"
)

func buildDescriptionMediaItems(ctx context.Context, keys []string) ([]ai.DescriptionMediaItem, error) {
	log.Debug().Int("keyCount", len(keys)).Msg("Building description media items")
	var items []ai.DescriptionMediaItem

	for _, key := range keys {
		filename := filepath.Base(key)
		ext := strings.ToLower(filepath.Ext(key))

		item := ai.DescriptionMediaItem{Filename: filename}

		if media.IsImage(ext) {
			item.Type = "Photo"
			parts := strings.SplitN(key, "/", 2)
			thumbKey := fmt.Sprintf("%s/thumbnails/%s.jpg", parts[0], strings.TrimSuffix(filename, ext))

			tmpPath, cleanup, err := s3util.DownloadToTempFile(ctx, s3Client, mediaBucket, thumbKey)
			if err != nil {
				origPath, origCleanup, origErr := s3util.DownloadToTempFile(ctx, s3Client, mediaBucket, key)
				defer origCleanup()
				if origErr != nil {
					log.Warn().Str("key", key).Err(origErr).Msg("Skipping: failed to download original")
					continue
				}

				origData, readErr := os.ReadFile(origPath)
				if readErr != nil {
					log.Warn().Str("key", key).Err(readErr).Msg("Skipping: failed to read original")
					continue
				}

				mime := "image/jpeg"
				if m, ok := media.SupportedImageExtensions[ext]; ok {
					mime = m
				}

				thumbData, thumbMIME, thumbErr := s3util.GenerateThumbnailFromBytes(origData, mime, media.DefaultThumbnailMaxDimension)
				if thumbErr != nil {
					log.Warn().Str("key", key).Err(thumbErr).Msg("Skipping: failed to generate thumbnail")
					continue
				}
				item.ThumbnailData = thumbData
				item.ThumbnailMIMEType = thumbMIME
			} else {
				defer cleanup()
				thumbData, err := os.ReadFile(tmpPath)
				if err != nil {
					log.Warn().Str("key", key).Err(err).Msg("Skipping: failed to read thumbnail")
					continue
				}
				item.ThumbnailData = thumbData
				item.ThumbnailMIMEType = "image/jpeg"
			}
		} else if media.IsVideo(ext) {
			item.Type = "Video"
			parts := strings.SplitN(key, "/", 2)
			thumbKey := fmt.Sprintf("%s/thumbnails/%s.jpg", parts[0], strings.TrimSuffix(filename, ext))

			tmpPath, cleanup, err := s3util.DownloadToTempFile(ctx, s3Client, mediaBucket, thumbKey)
			if err != nil {
				log.Warn().Str("key", key).Err(err).Msg("Skipping: failed to download video thumbnail")
				continue
			}
			defer cleanup()

			thumbData, err := os.ReadFile(tmpPath)
			if err != nil {
				log.Warn().Str("key", key).Err(err).Msg("Skipping: failed to read video thumbnail")
				continue
			}
			item.ThumbnailData = thumbData
			item.ThumbnailMIMEType = "image/jpeg"
		} else {
			log.Warn().Str("key", key).Str("ext", ext).Msg("Skipping: unsupported file type")
			continue
		}

		items = append(items, item)
	}

	if len(items) == 0 {
		return nil, fmt.Errorf("no media items could be prepared for description")
	}
	return items, nil
}
