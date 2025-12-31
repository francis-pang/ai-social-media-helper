package filehandler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog/log"
)

// SupportedImageExtensions defines the file extensions that are supported for image upload.
var SupportedImageExtensions = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".gif":  "image/gif",
	".webp": "image/webp",
	".heic": "image/heic",
	".heif": "image/heif",
}

// SupportedVideoExtensions defines the file extensions that are supported for video upload.
var SupportedVideoExtensions = map[string]string{
	".mp4":  "video/mp4",
	".mov":  "video/quicktime",
	".avi":  "video/x-msvideo",
	".webm": "video/webm",
	".mkv":  "video/x-matroska",
}

// MediaFile represents a file that can be uploaded to Gemini.
type MediaFile struct {
	Path     string
	MIMEType string
	Data     []byte
	Size     int64
}

// LoadMediaFile loads a media file from disk and returns a MediaFile struct.
func LoadMediaFile(filePath string) (*MediaFile, error) {
	log.Debug().Str("path", filePath).Msg("Loading media file")

	// Check if file exists
	info, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("file not found: %s", filePath)
		}
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	if info.IsDir() {
		return nil, fmt.Errorf("path is a directory, not a file: %s", filePath)
	}

	// Determine MIME type from extension
	ext := strings.ToLower(filepath.Ext(filePath))
	mimeType, err := GetMIMEType(ext)
	if err != nil {
		return nil, err
	}

	// Read file data
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	log.Info().
		Str("path", filePath).
		Str("mime_type", mimeType).
		Int64("size_bytes", info.Size()).
		Msg("Media file loaded successfully")

	return &MediaFile{
		Path:     filePath,
		MIMEType: mimeType,
		Data:     data,
		Size:     info.Size(),
	}, nil
}

// GetMIMEType returns the MIME type for a given file extension.
func GetMIMEType(ext string) (string, error) {
	ext = strings.ToLower(ext)

	if mimeType, ok := SupportedImageExtensions[ext]; ok {
		return mimeType, nil
	}

	if mimeType, ok := SupportedVideoExtensions[ext]; ok {
		return mimeType, nil
	}

	return "", fmt.Errorf("unsupported file extension: %s", ext)
}

// IsImage returns true if the file extension corresponds to an image.
func IsImage(ext string) bool {
	_, ok := SupportedImageExtensions[strings.ToLower(ext)]
	return ok
}

// IsVideo returns true if the file extension corresponds to a video.
func IsVideo(ext string) bool {
	_, ok := SupportedVideoExtensions[strings.ToLower(ext)]
	return ok
}

// IsSupported returns true if the file extension is supported (image or video).
func IsSupported(ext string) bool {
	return IsImage(ext) || IsVideo(ext)
}

