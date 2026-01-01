package filehandler

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rs/zerolog/log"
	"golang.org/x/image/draw"
)

// DefaultThumbnailMaxDimension is the maximum dimension (width or height) for thumbnails.
const DefaultThumbnailMaxDimension = 1024

// ScanOptions configures directory scanning behavior.
type ScanOptions struct {
	// MaxDepth limits recursion depth. 0 = unlimited, 1 = top-level only.
	MaxDepth int

	// Limit caps the number of images returned. 0 = unlimited.
	Limit int
}

// ScanDirectory scans a directory for supported image files and returns them as MediaFiles.
// Only images are scanned (videos are excluded for Iteration 8).
// Files are sorted alphabetically by filename.
// This is a convenience wrapper that calls ScanDirectoryWithOptions with default options.
func ScanDirectory(dirPath string) ([]*MediaFile, error) {
	return ScanDirectoryWithOptions(dirPath, ScanOptions{})
}

// ScanDirectoryWithOptions scans a directory for supported image files with configurable options.
// Recursive scanning is enabled by default (MaxDepth=0 means unlimited).
// Symlinks to files are followed; symlinks to directories are skipped to prevent infinite loops.
// Files are sorted alphabetically by path for consistent ordering.
func ScanDirectoryWithOptions(dirPath string, opts ScanOptions) ([]*MediaFile, error) {
	log.Info().
		Str("path", dirPath).
		Int("max_depth", opts.MaxDepth).
		Int("limit", opts.Limit).
		Msg("Scanning directory for images")

	// Check if directory exists
	info, err := os.Stat(dirPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("directory not found: %s", dirPath)
		}
		return nil, fmt.Errorf("failed to stat directory: %w", err)
	}

	if !info.IsDir() {
		return nil, fmt.Errorf("path is not a directory: %s", dirPath)
	}

	// Convert to absolute path for consistent depth calculation
	absPath, err := filepath.Abs(dirPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}
	baseDepth := strings.Count(absPath, string(os.PathSeparator))

	var mediaFiles []*MediaFile
	limitReached := false

	// Walk the directory tree
	err = filepath.WalkDir(absPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			log.Warn().Err(err).Str("path", path).Msg("Error accessing path, skipping")
			return nil // Continue walking despite errors
		}

		// Check depth limit
		if opts.MaxDepth > 0 {
			currentDepth := strings.Count(path, string(os.PathSeparator)) - baseDepth
			if d.IsDir() && currentDepth >= opts.MaxDepth {
				return fs.SkipDir
			}
		}

		// Skip directories (but continue into them)
		if d.IsDir() {
			return nil
		}

		// Handle symlinks: follow file symlinks, skip directory symlinks
		if d.Type()&fs.ModeSymlink != 0 {
			linkTarget, err := filepath.EvalSymlinks(path)
			if err != nil {
				log.Warn().Err(err).Str("path", path).Msg("Failed to resolve symlink, skipping")
				return nil
			}

			targetInfo, err := os.Stat(linkTarget)
			if err != nil {
				log.Warn().Err(err).Str("path", path).Msg("Failed to stat symlink target, skipping")
				return nil
			}

			if targetInfo.IsDir() {
				log.Debug().Str("path", path).Msg("Skipping symlink to directory")
				return nil
			}
			// Continue processing file symlinks
		}

		// Check if limit reached
		if opts.Limit > 0 && len(mediaFiles) >= opts.Limit {
			limitReached = true
			return fs.SkipAll
		}

		ext := strings.ToLower(filepath.Ext(d.Name()))

		// Only process images
		if !IsImage(ext) {
			return nil
		}

		mediaFile, err := LoadMediaFile(path)
		if err != nil {
			log.Warn().Err(err).Str("file", d.Name()).Msg("Failed to load media file, skipping")
			return nil
		}

		mediaFiles = append(mediaFiles, mediaFile)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to walk directory: %w", err)
	}

	// Sort by path for consistent ordering
	sort.Slice(mediaFiles, func(i, j int) bool {
		return mediaFiles[i].Path < mediaFiles[j].Path
	})

	logEvent := log.Info().
		Int("total_images", len(mediaFiles)).
		Str("directory", dirPath)

	if limitReached {
		logEvent.Bool("limit_reached", true)
	}

	logEvent.Msg("Directory scan complete")

	return mediaFiles, nil
}

// GenerateThumbnail creates a low-resolution thumbnail of the image.
// Returns the thumbnail bytes, MIME type, and any error.
//
// Strategy:
//   - JPEG/PNG: Resize using pure Go (golang.org/x/image/draw)
//   - HEIC/HEIF: Use macOS sips tool to convert to JPEG thumbnail
//   - GIF/WebP: Return original file (typically small)
func GenerateThumbnail(mediaFile *MediaFile, maxDimension int) ([]byte, string, error) {
	ext := strings.ToLower(filepath.Ext(mediaFile.Path))

	log.Debug().
		Str("path", mediaFile.Path).
		Str("ext", ext).
		Int("max_dim", maxDimension).
		Msg("Generating thumbnail")

	switch ext {
	case ".jpg", ".jpeg", ".png":
		return generateThumbnailPureGo(mediaFile.Path, ext, maxDimension)

	case ".heic", ".heif":
		return generateThumbnailSips(mediaFile.Path, maxDimension)

	case ".gif", ".webp":
		// Return original file for small formats
		data, err := os.ReadFile(mediaFile.Path)
		if err != nil {
			return nil, "", fmt.Errorf("failed to read file: %w", err)
		}
		return data, mediaFile.MIMEType, nil

	default:
		return nil, "", fmt.Errorf("unsupported format for thumbnail: %s", ext)
	}
}

// generateThumbnailPureGo resizes JPEG/PNG images using pure Go.
func generateThumbnailPureGo(filePath, ext string, maxDimension int) ([]byte, string, error) {
	// Open the file
	f, err := os.Open(filePath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	// Decode the image
	var img image.Image
	switch ext {
	case ".jpg", ".jpeg":
		img, err = jpeg.Decode(f)
	case ".png":
		img, err = png.Decode(f)
	default:
		return nil, "", fmt.Errorf("unsupported format: %s", ext)
	}

	if err != nil {
		return nil, "", fmt.Errorf("failed to decode image: %w", err)
	}

	// Calculate new dimensions maintaining aspect ratio
	bounds := img.Bounds()
	origWidth := bounds.Dx()
	origHeight := bounds.Dy()

	newWidth, newHeight := calculateThumbnailDimensions(origWidth, origHeight, maxDimension)

	// Skip resize if already smaller
	if origWidth <= maxDimension && origHeight <= maxDimension {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, "", fmt.Errorf("failed to read file: %w", err)
		}
		mimeType := "image/jpeg"
		if ext == ".png" {
			mimeType = "image/png"
		}
		return data, mimeType, nil
	}

	// Create resized image
	resized := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))
	draw.CatmullRom.Scale(resized, resized.Bounds(), img, bounds, draw.Over, nil)

	// Encode to JPEG (smaller than PNG for thumbnails)
	var buf bytes.Buffer
	err = jpeg.Encode(&buf, resized, &jpeg.Options{Quality: 80})
	if err != nil {
		return nil, "", fmt.Errorf("failed to encode thumbnail: %w", err)
	}

	log.Debug().
		Int("orig_width", origWidth).
		Int("orig_height", origHeight).
		Int("new_width", newWidth).
		Int("new_height", newHeight).
		Int("thumb_size", buf.Len()).
		Msg("Thumbnail generated (pure Go)")

	return buf.Bytes(), "image/jpeg", nil
}

// generateThumbnailSips uses macOS sips tool to convert HEIC to JPEG thumbnail.
func generateThumbnailSips(filePath string, maxDimension int) ([]byte, string, error) {
	// Create temp file for output
	tmpFile, err := os.CreateTemp("", "thumb-*.jpg")
	if err != nil {
		return nil, "", fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	// Run sips to convert and resize
	// sips -s format jpeg -Z 1024 input.heic --out output.jpg
	cmd := exec.Command("sips",
		"-s", "format", "jpeg",
		"-Z", fmt.Sprintf("%d", maxDimension),
		filePath,
		"--out", tmpPath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Warn().
			Err(err).
			Str("output", string(output)).
			Str("file", filePath).
			Msg("sips conversion failed, falling back to full file")

		// Fallback: return original HEIC file
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, "", fmt.Errorf("failed to read original file: %w", err)
		}
		return data, "image/heic", nil
	}

	// Read the generated thumbnail
	data, err := os.ReadFile(tmpPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read thumbnail: %w", err)
	}

	log.Debug().
		Str("file", filepath.Base(filePath)).
		Int("thumb_size", len(data)).
		Msg("Thumbnail generated (sips)")

	return data, "image/jpeg", nil
}

// calculateThumbnailDimensions calculates new dimensions maintaining aspect ratio.
func calculateThumbnailDimensions(width, height, maxDimension int) (int, int) {
	if width <= maxDimension && height <= maxDimension {
		return width, height
	}

	if width > height {
		newWidth := maxDimension
		newHeight := int(float64(height) * float64(maxDimension) / float64(width))
		return newWidth, newHeight
	}

	newHeight := maxDimension
	newWidth := int(float64(width) * float64(maxDimension) / float64(height))
	return newWidth, newHeight
}
