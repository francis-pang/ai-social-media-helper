package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/fpang/gemini-media-cli/internal/filehandler"
	"github.com/rs/zerolog/log"
)

// --- Media Endpoints ---

// GET /api/media/thumbnail?key=sessionId/filename.jpg
func handleThumbnail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	key := r.URL.Query().Get("key")
	if key == "" {
		httpError(w, http.StatusBadRequest, "key is required")
		return
	}

	// Validate S3 key format (DDR-028 Problem 5)
	if err := validateS3Key(key); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Check for pre-generated thumbnail (DDR-030): keys under /thumbnails/ are
	// already JPEG thumbnails — serve directly from S3 without regeneration.
	parts := strings.SplitN(key, "/", 2)
	if len(parts) == 2 && strings.HasPrefix(parts[1], "thumbnails/") {
		result, err := s3Client.GetObject(context.Background(), &s3.GetObjectInput{
			Bucket: &mediaBucket,
			Key:    &key,
		})
		if err != nil {
			log.Warn().Err(err).Str("key", key).Msg("Pre-generated thumbnail not found")
			httpError(w, http.StatusNotFound, "thumbnail not found")
			return
		}
		defer result.Body.Close()

		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		io.Copy(w, result.Body)
		return
	}

	ext := strings.ToLower(filepath.Ext(key))

	// For images, download from S3, generate thumbnail, return bytes.
	if mime, ok := filehandler.SupportedImageExtensions[ext]; ok {
		tmpPath, cleanup, err := downloadFromS3(context.Background(), key)
		if err != nil {
			log.Warn().Err(err).Str("key", key).Msg("Failed to download for thumbnail")
			httpError(w, http.StatusNotFound, "file not found")
			return
		}
		defer cleanup()

		info, _ := os.Stat(tmpPath)
		mf := &filehandler.MediaFile{
			Path:     tmpPath,
			MIMEType: mime,
			Size:     info.Size(),
		}

		thumbData, thumbMIME, err := filehandler.GenerateThumbnail(mf, 400)
		if err != nil {
			log.Warn().Err(err).Str("key", key).Msg("Failed to generate thumbnail")
			httpError(w, http.StatusInternalServerError, "thumbnail generation failed")
			return
		}
		w.Header().Set("Content-Type", thumbMIME)
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Write(thumbData)
		return
	}

	// For videos, return a placeholder SVG (pre-generated thumbnails are preferred; DDR-030).
	if _, ok := filehandler.SupportedVideoExtensions[ext]; ok {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		fmt.Fprintf(w, `<svg xmlns="http://www.w3.org/2000/svg" width="400" height="400" viewBox="0 0 400 400">
			<rect width="400" height="400" fill="#1a1d27"/>
			<polygon points="160,120 160,280 290,200" fill="#8b8fa8"/>
			<text x="200" y="340" text-anchor="middle" fill="#8b8fa8" font-size="16" font-family="sans-serif">%s</text>
		</svg>`, filepath.Base(key))
		return
	}

	httpError(w, http.StatusBadRequest, "unsupported file type")
}

// GET /api/media/full?key=sessionId/filename.jpg
// Returns a presigned GET URL for the full-resolution image.
func handleFullImage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	key := r.URL.Query().Get("key")
	if key == "" {
		httpError(w, http.StatusBadRequest, "key is required")
		return
	}

	// Validate S3 key format (DDR-028 Problem 5)
	if err := validateS3Key(key); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}

	result, err := presigner.PresignGetObject(context.Background(), &s3.GetObjectInput{
		Bucket: &mediaBucket,
		Key:    &key,
	}, s3.WithPresignExpires(1*time.Hour))
	if err != nil {
		httpError(w, http.StatusInternalServerError, "failed to generate download URL")
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{
		"url": result.URL,
	})
}

// generateThumbnailFromBytes creates a thumbnail from raw image bytes.
func generateThumbnailFromBytes(imageData []byte, mimeType string, maxDimension int) ([]byte, string, error) {
	// Write to temp file, generate thumbnail, clean up
	tmpFile, err := os.CreateTemp("", "enhance-thumb-*")
	if err != nil {
		return nil, "", err
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.Write(imageData); err != nil {
		tmpFile.Close()
		return nil, "", err
	}
	tmpFile.Close()

	info, _ := os.Stat(tmpPath)
	mf := &filehandler.MediaFile{
		Path:     tmpPath,
		MIMEType: mimeType,
		Size:     info.Size(),
	}

	return filehandler.GenerateThumbnail(mf, maxDimension)
}
