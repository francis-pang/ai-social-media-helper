package media

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog/log"
	"golang.org/x/image/draw"
)

// ResizeImageForGemini downscales large photos for Gemini API optimization.
//
// When ffmpeg is available (Lambda), outputs WebP for ~30-40% smaller files
// than JPEG at equivalent quality. Falls back to pure-Go JPEG when ffmpeg
// is unavailable (CLI).
//
// Returns the resized image bytes, MIME type, and error.
// Returns nil, "", nil when no resize is needed (image already small enough,
// format not supported for resize, or ffmpeg unavailable for HEIC).
// The caller checks for nil bytes and falls back to the original file.
func ResizeImageForGemini(mediaFile *MediaFile, maxDimension int, quality int) ([]byte, string, error) {
	ext := strings.ToLower(filepath.Ext(mediaFile.Path))

	switch ext {
	case ".jpg", ".jpeg", ".png":
		if IsFFmpegAvailable() {
			return resizeWithFFmpegWebP(mediaFile.Path, ext, maxDimension, quality)
		}
		return resizeJPEGPNG(mediaFile.Path, ext, maxDimension, quality)

	case ".heic", ".heif":
		if IsFFmpegAvailable() {
			return resizeWithFFmpegWebP(mediaFile.Path, ext, maxDimension, quality)
		}
		log.Debug().Str("path", mediaFile.Path).Msg("ffmpeg not available, skipping HEIC resize")
		return nil, "", nil

	case ".gif", ".webp":
		return nil, "", nil

	default:
		return nil, "", nil
	}
}

// resizeWithFFmpegWebP uses ffmpeg to resize and convert any supported image
// to WebP. Handles JPEG, PNG, and HEIC/HEIF input uniformly.
// WebP is ~30-40% smaller than JPEG at equivalent quality and encodes in ~300ms.
func resizeWithFFmpegWebP(filePath, ext string, maxDimension, quality int) ([]byte, string, error) {
	// For JPEG/PNG, check dimensions first to skip resize for small images.
	// HEIC skips this check (we always convert HEIC → WebP regardless of size).
	if ext == ".jpg" || ext == ".jpeg" || ext == ".png" {
		needsResize, err := imageExceedsDimension(filePath, ext, maxDimension)
		if err != nil {
			return nil, "", fmt.Errorf("failed to check image dimensions: %w", err)
		}
		if !needsResize {
			return nil, "", nil
		}
	}

	tmpFile, err := os.CreateTemp("", "gemini-resize-*.webp")
	if err != nil {
		return nil, "", fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	vf := fmt.Sprintf("scale='min(%d,iw)':-2", maxDimension)
	cmd := exec.Command("ffmpeg",
		"-i", filePath,
		"-vf", vf,
		"-frames:v", "1",
		"-c:v", "libwebp",
		"-quality", fmt.Sprintf("%d", quality),
		"-y", tmpPath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Warn().Err(err).Str("output", string(output)).Str("path", filePath).
			Msg("ffmpeg WebP resize failed, falling back to pure Go JPEG")
		if ext == ".jpg" || ext == ".jpeg" || ext == ".png" {
			return resizeJPEGPNG(filePath, ext, maxDimension, quality)
		}
		return nil, "", fmt.Errorf("ffmpeg WebP resize failed: %w: %s", err, string(output))
	}

	data, err := os.ReadFile(tmpPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read resized output: %w", err)
	}

	log.Debug().
		Str("path", filePath).
		Str("input_format", ext).
		Int("output_size", len(data)).
		Msg("Image resized to WebP for Gemini via ffmpeg")

	return data, "image/webp", nil
}

// imageExceedsDimension checks if a JPEG/PNG image has any dimension > maxDimension.
func imageExceedsDimension(filePath, ext string, maxDimension int) (bool, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return false, err
	}
	defer f.Close()

	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return false, err
	}
	return cfg.Width > maxDimension || cfg.Height > maxDimension, nil
}

// resizeJPEGPNG is the pure-Go fallback when ffmpeg is unavailable.
// Outputs JPEG since WebP encoding requires CGO (DDR-027).
func resizeJPEGPNG(filePath, ext string, maxDimension, jpegQuality int) ([]byte, string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to open file for resize: %w", err)
	}
	defer f.Close()

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
		return nil, "", fmt.Errorf("failed to decode image for resize: %w", err)
	}

	bounds := img.Bounds()
	origWidth := bounds.Dx()
	origHeight := bounds.Dy()

	if origWidth <= maxDimension && origHeight <= maxDimension {
		return nil, "", nil
	}

	newWidth, newHeight := calculateThumbnailDimensions(origWidth, origHeight, maxDimension)

	resized := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))
	draw.CatmullRom.Scale(resized, resized.Bounds(), img, bounds, draw.Over, nil)

	var buf bytes.Buffer
	err = jpeg.Encode(&buf, resized, &jpeg.Options{Quality: jpegQuality})
	if err != nil {
		return nil, "", fmt.Errorf("failed to encode resized image: %w", err)
	}

	log.Debug().
		Str("path", filePath).
		Int("orig_width", origWidth).
		Int("orig_height", origHeight).
		Int("new_width", newWidth).
		Int("new_height", newHeight).
		Int("output_size", buf.Len()).
		Msg("Image resized to JPEG for Gemini (pure Go fallback)")

	return buf.Bytes(), "image/jpeg", nil
}
