package filehandler

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

	"github.com/chai2010/webp"
	"github.com/rs/zerolog/log"
	"golang.org/x/image/draw"
)

// DefaultThumbnailMaxDimension is the maximum dimension (width or height) for thumbnails.
const DefaultThumbnailMaxDimension = 1024

// GenerateThumbnail creates a low-resolution thumbnail of a media file.
// Returns the thumbnail bytes, MIME type, and any error.
//
// Strategy:
//   - JPEG/PNG: Resize using pure Go (golang.org/x/image/draw) and encode as WebP
//   - HEIC/HEIF: Use ffmpeg to convert to WebP thumbnail (cross-platform, DDR-027)
//   - GIF/WebP: Return original file (typically small)
//   - Video (MP4/MOV/AVI/WebM/MKV): Extract frame at 1s using ffmpeg and encode as WebP (DDR-030)
func GenerateThumbnail(mediaFile *MediaFile, maxDimension int) ([]byte, string, error) {
	ext := strings.ToLower(filepath.Ext(mediaFile.Path))

	log.Debug().
		Str("path", mediaFile.Path).
		Str("mime_type", mediaFile.MIMEType).
		Int("max_dimension", maxDimension).
		Msg("Generating thumbnail")

	var data []byte
	var mimeType string
	var err error
	method := ""

	switch ext {
	case ".jpg", ".jpeg", ".png":
		data, mimeType, err = generateThumbnailPureGo(mediaFile.Path, ext, maxDimension)
		method = "pure-go"

	case ".heic", ".heif":
		data, mimeType, err = generateThumbnailHEIC(mediaFile.Path, maxDimension)
		method = "ffmpeg-heic"

	case ".gif", ".webp":
		// Return original file for small formats
		data, err = os.ReadFile(mediaFile.Path)
		if err != nil {
			return nil, "", fmt.Errorf("failed to read file: %w", err)
		}
		mimeType = mediaFile.MIMEType
		method = "original"

	case ".mp4", ".mov", ".avi", ".webm", ".mkv":
		data, mimeType, err = GenerateVideoThumbnail(mediaFile.Path, maxDimension)
		method = "ffmpeg-video"

	default:
		return nil, "", fmt.Errorf("unsupported format for thumbnail: %s", ext)
	}

	if err != nil {
		return nil, "", err
	}

	log.Debug().
		Str("path", mediaFile.Path).
		Int("output_size", len(data)).
		Str("method", method).
		Msg("Thumbnail generation complete")

	return data, mimeType, nil
}

// GenerateVideoThumbnail extracts a frame from a video at the 1-second mark
// and returns it as a WebP thumbnail. Uses ffmpeg for extraction.
// Falls back to a frame at 0s if the video is shorter than 1 second.
// See DDR-030: Cloud Selection Backend Architecture.
func GenerateVideoThumbnail(videoPath string, maxDimension int) ([]byte, string, error) {
	log.Debug().
		Str("path", videoPath).
		Int("max_dimension", maxDimension).
		Msg("Generating video thumbnail")

	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, "", fmt.Errorf("ffmpeg not found: video thumbnail generation requires ffmpeg")
	}

	// Extract frame as PNG first (WebP encoding via ffmpeg may not be available)
	tmpFile, err := os.CreateTemp("", "vthumb-*.png")
	if err != nil {
		return nil, "", fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	// ffmpeg -i input.mp4 -ss 1 -vframes 1 -vf "scale='min(1024,iw)':-2" -y output.png
	// -ss 1: seek to 1 second (avoids black/blank first frames)
	// -vframes 1: extract single frame
	// scale filter: downscale only if larger, preserve aspect ratio, ensure even height
	vf := fmt.Sprintf("scale='min(%d,iw)':-2", maxDimension)
	cmd := exec.Command(ffmpegPath,
		"-i", videoPath,
		"-ss", "1",
		"-vframes", "1",
		"-vf", vf,
		"-f", "image2",
		"-y", tmpPath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		// Retry at 0s in case the video is shorter than 1 second
		cmd2 := exec.Command(ffmpegPath,
			"-i", videoPath,
			"-vframes", "1",
			"-vf", vf,
			"-f", "image2",
			"-y", tmpPath,
		)
		output2, err2 := cmd2.CombinedOutput()
		if err2 != nil {
			return nil, "", fmt.Errorf("ffmpeg frame extraction failed: %w: %s / %s", err2, string(output), string(output2))
		}
	}

	// Read the extracted frame and convert to WebP
	frameFile, err := os.Open(tmpPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read video thumbnail: %w", err)
	}
	defer frameFile.Close()

	img, err := png.Decode(frameFile)
	if err != nil {
		return nil, "", fmt.Errorf("failed to decode extracted frame: %w", err)
	}

	// Encode as WebP
	var buf bytes.Buffer
	err = webp.Encode(&buf, img, &webp.Options{Quality: 80, Lossless: false})
	if err != nil {
		return nil, "", fmt.Errorf("failed to encode thumbnail as WebP: %w", err)
	}

	if buf.Len() == 0 {
		return nil, "", fmt.Errorf("WebP encoding produced empty thumbnail for %s", filepath.Base(videoPath))
	}

	log.Debug().
		Str("path", videoPath).
		Int("output_size", buf.Len()).
		Msg("Video thumbnail generation complete")

	return buf.Bytes(), "image/webp", nil
}

// generateThumbnailPureGo resizes JPEG/PNG images using pure Go.
func generateThumbnailPureGo(filePath, ext string, maxDimension int) ([]byte, string, error) {
	log.Debug().
		Str("path", filePath).
		Str("format", ext).
		Int("max_dimension", maxDimension).
		Msg("Generating thumbnail using pure Go")

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

	// Skip resize if already smaller - but still convert to WebP for consistency
	if origWidth <= maxDimension && origHeight <= maxDimension {
		// Still convert to WebP even if no resize needed
		var buf bytes.Buffer
		err = webp.Encode(&buf, img, &webp.Options{Quality: 80})
		if err != nil {
			return nil, "", fmt.Errorf("failed to encode thumbnail as WebP: %w", err)
		}
		return buf.Bytes(), "image/webp", nil
	}

	// Create resized image
	resized := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))
	draw.CatmullRom.Scale(resized, resized.Bounds(), img, bounds, draw.Over, nil)

	// Encode to WebP (optimized format for thumbnails)
	var buf bytes.Buffer
	err = webp.Encode(&buf, resized, &webp.Options{Quality: 80})
	if err != nil {
		return nil, "", fmt.Errorf("failed to encode thumbnail: %w", err)
	}

	log.Debug().
		Str("path", filePath).
		Int("orig_width", origWidth).
		Int("orig_height", origHeight).
		Int("new_width", newWidth).
		Int("new_height", newHeight).
		Int("output_size", buf.Len()).
		Msg("Thumbnail generated (pure Go)")

	return buf.Bytes(), "image/webp", nil
}

// generateThumbnailHEIC uses ffmpeg to convert HEIC/HEIF to a WebP thumbnail.
// This replaces the macOS-only sips tool (DDR-027) and works cross-platform:
// locally (if ffmpeg is installed) and in Lambda (ffmpeg bundled in container image).
// Falls back to returning the original HEIC file if ffmpeg is unavailable.
func generateThumbnailHEIC(filePath string, maxDimension int) ([]byte, string, error) {
	log.Debug().
		Str("path", filePath).
		Int("max_dimension", maxDimension).
		Msg("Generating HEIC thumbnail")

	// Check if ffmpeg is available
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		log.Warn().
			Str("file", filePath).
			Msg("ffmpeg not found, falling back to original HEIC file for thumbnail")

		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, "", fmt.Errorf("failed to read original file: %w", err)
		}
		return data, "image/heic", nil
	}

	// Create temp file for output (PNG first, then convert to WebP)
	tmpFile, err := os.CreateTemp("", "thumb-*.png")
	if err != nil {
		return nil, "", fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	// ffmpeg -i input.heic -vf "scale='min(1024,iw)':-2" -frames:v 1 -y output.png
	// - scale filter: downscale only if larger than maxDimension, preserve aspect ratio
	// - -2 ensures even height (required by some encoders)
	// - -frames:v 1: extract single frame (HEIC is a single image)
	vf := fmt.Sprintf("scale='min(%d,iw)':-2", maxDimension)
	cmd := exec.Command(ffmpegPath,
		"-i", filePath,
		"-vf", vf,
		"-frames:v", "1",
		"-y", tmpPath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Warn().
			Err(err).
			Str("output", string(output)).
			Str("file", filePath).
			Msg("ffmpeg HEIC conversion failed, falling back to original file")

		// Fallback: return original HEIC file
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, "", fmt.Errorf("failed to read original file: %w", err)
		}
		return data, "image/heic", nil
	}

	// Read the extracted frame and convert to WebP
	frameFile, err := os.Open(tmpPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read thumbnail: %w", err)
	}
	defer frameFile.Close()

	img, err := png.Decode(frameFile)
	if err != nil {
		return nil, "", fmt.Errorf("failed to decode extracted frame: %w", err)
	}

	// Encode as WebP
	var buf bytes.Buffer
	err = webp.Encode(&buf, img, &webp.Options{Quality: 80, Lossless: false})
	if err != nil {
		return nil, "", fmt.Errorf("failed to encode thumbnail as WebP: %w", err)
	}

	log.Debug().
		Str("file", filepath.Base(filePath)).
		Int("thumb_size", buf.Len()).
		Msg("Thumbnail generated (ffmpeg HEIC)")

	return buf.Bytes(), "image/webp", nil
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
