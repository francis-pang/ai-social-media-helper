// Package filehandler provides media file handling and metadata extraction.
//
// This package implements the Split-Provider Model (DDR-013) for metadata extraction:
//   - Images (JPEG, PNG, HEIC, etc.): Pure Go using evanoberholster/imagemeta
//   - Videos (MP4, MOV, MKV, etc.): External tool using ffprobe
//
// Both providers implement the common MediaMetadata interface, enabling polymorphic
// handling by consuming code.
package filehandler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// MediaMetadata is the common interface for all media metadata types.
// Both ImageMetadata and VideoMetadata implement this interface.
//
// This interface enables polymorphic handling—the consuming code (prompt builder,
// chat handler) doesn't need to know whether metadata came from imagemeta or ffprobe.
type MediaMetadata interface {
	// FormatMetadataContext returns a formatted string for inclusion in AI prompts.
	FormatMetadataContext() string

	// GetMediaType returns "image" or "video".
	GetMediaType() string

	// HasGPSData returns true if GPS coordinates are available.
	HasGPSData() bool

	// GetGPS returns latitude and longitude (0,0 if not available).
	GetGPS() (latitude, longitude float64)

	// HasDateData returns true if date/time is available.
	HasDateData() bool

	// GetDate returns the date taken/created.
	GetDate() time.Time
}

// MediaFile represents a file that can be uploaded to Gemini.
// Note: Data is no longer populated; we always stream upload via Files API (DDR-012).
type MediaFile struct {
	Path     string
	MIMEType string
	Size     int64
	Metadata MediaMetadata
}

// LoadMediaFile loads a media file from disk and returns a MediaFile struct.
// It automatically detects the file type and routes to the appropriate metadata extractor:
//   - Images → ExtractImageMetadata (Pure Go, imagemeta library)
//   - Videos → ExtractVideoMetadata (External, ffprobe)
//
// Note: File data is not loaded into memory; Files API streams directly from disk (DDR-012).
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

	mediaFile := &MediaFile{
		Path:     filePath,
		MIMEType: mimeType,
		Size:     info.Size(),
	}

	log.Info().
		Str("path", filePath).
		Str("mime_type", mimeType).
		Int64("size_bytes", info.Size()).
		Msg("Media file loaded successfully")

	// Extract metadata based on file type (Split-Provider Model)
	if IsImage(ext) {
		imgMeta, err := ExtractImageMetadata(filePath)
		if err != nil {
			log.Warn().Err(err).Msg("Failed to extract image metadata, continuing without it")
		} else {
			mediaFile.Metadata = imgMeta
		}
	} else if IsVideo(ext) {
		vidMeta, err := ExtractVideoMetadata(filePath)
		if err != nil {
			log.Warn().Err(err).Msg("Failed to extract video metadata, continuing without it")
		} else {
			mediaFile.Metadata = vidMeta
		}
	}

	return mediaFile, nil
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

// CoordinatesToDMS converts decimal degrees to degrees, minutes, seconds format.
func CoordinatesToDMS(lat, lon float64) string {
	latDir := "N"
	if lat < 0 {
		latDir = "S"
		lat = -lat
	}

	lonDir := "E"
	if lon < 0 {
		lonDir = "W"
		lon = -lon
	}

	latDeg := int(lat)
	latMin := int((lat - float64(latDeg)) * 60)
	latSec := ((lat - float64(latDeg)) * 60 - float64(latMin)) * 60

	lonDeg := int(lon)
	lonMin := int((lon - float64(lonDeg)) * 60)
	lonSec := ((lon - float64(lonDeg)) * 60 - float64(lonMin)) * 60

	return fmt.Sprintf("%d°%d'%.2f\"%s, %d°%d'%.2f\"%s",
		latDeg, latMin, latSec, latDir,
		lonDeg, lonMin, lonSec, lonDir)
}

