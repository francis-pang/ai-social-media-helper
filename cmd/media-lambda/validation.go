package main

import (
	"fmt"
	"regexp"
	"strings"
)

// --- Input Validation (DDR-028) ---

// uuidRegex matches UUID v4 format: 8-4-4-4-12 lowercase hex with dashes.
var uuidRegex = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// safeFilenameRegex allows alphanumeric, dots, hyphens, underscores, spaces, and parentheses.
var safeFilenameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._ ()-]{0,254}$`)

func validateSessionID(id string) error {
	if !uuidRegex.MatchString(id) {
		return fmt.Errorf("invalid sessionId: must be a UUID (e.g., a1b2c3d4-e5f6-7890-abcd-ef1234567890)")
	}
	return nil
}

func validateFilename(name string) error {
	if name == "" {
		return fmt.Errorf("filename is required")
	}
	if strings.Contains(name, "..") || strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return fmt.Errorf("filename contains invalid characters")
	}
	if !safeFilenameRegex.MatchString(name) {
		return fmt.Errorf("filename contains invalid characters; only alphanumeric, dots, hyphens, underscores, spaces, and parentheses allowed")
	}
	return nil
}

func validateS3Key(key string) error {
	if strings.Contains(key, "..") || strings.HasPrefix(key, "/") || strings.Contains(key, "\\") {
		return fmt.Errorf("invalid key")
	}
	parts := strings.SplitN(key, "/", 2)
	if len(parts) != 2 || !uuidRegex.MatchString(parts[0]) || parts[1] == "" {
		return fmt.Errorf("invalid key format: expected <uuid>/<filename>")
	}
	return nil
}

// --- Upload Validation (DDR-028) ---

// allowedContentTypes is the content-type allowlist for uploads.
var allowedContentTypes = map[string]bool{
	// Photos
	"image/jpeg":    true,
	"image/png":     true,
	"image/gif":     true,
	"image/webp":    true,
	"image/heic":    true,
	"image/heif":    true,
	"image/tiff":    true,
	"image/bmp":     true,
	"image/svg+xml": true,
	// RAW camera formats
	"image/x-adobe-dng":     true,
	"image/x-canon-cr2":     true,
	"image/x-canon-cr3":     true,
	"image/x-nikon-nef":     true,
	"image/x-sony-arw":      true,
	"image/x-fuji-raf":      true,
	"image/x-olympus-orf":   true,
	"image/x-panasonic-rw2": true,
	"image/x-samsung-srw":   true,
	// Videos
	"video/mp4":        true,
	"video/quicktime":  true,
	"video/webm":       true,
	"video/x-msvideo":  true,
	"video/x-matroska": true,
	"video/3gpp":       true,
	"video/MP2T":       true,
}

const maxPhotoSize int64 = 50 * 1024 * 1024       // 50 MB
const maxVideoSize int64 = 5 * 1024 * 1024 * 1024 // 5 GB

func isVideoContentType(ct string) bool {
	return strings.HasPrefix(ct, "video/")
}
