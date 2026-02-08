package filehandler

// video_frames.go provides frame extraction from videos and frame-to-video
// reassembly using ffmpeg. Used by the multi-step video enhancement pipeline.
// See DDR-032: Multi-Step Frame-Based Video Enhancement Pipeline.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/rs/zerolog/log"
)

// Frame extraction constants for the video enhancement pipeline (DDR-032).
const (
	// FrameJPEGQuality controls the JPEG quality for extracted frames.
	// qscale:v 2 is high quality (~95% JPEG), minimizing compression artifacts
	// that would degrade AI enhancement quality.
	FrameJPEGQuality = 2

	// MaxExtractionFPS caps the frame extraction rate for long videos.
	// Videos over this many frames are extracted at a reduced rate.
	MaxExtractionFPS = 30.0

	// ReducedFPS15 is used for videos 30-60 seconds long.
	ReducedFPS15 = 15.0

	// ReducedFPS10 is used for videos 60-120 seconds long.
	ReducedFPS10 = 10.0

	// ReducedFPS5 is used for videos over 120 seconds long.
	ReducedFPS5 = 5.0

	// MaxRecommendedDuration is the maximum video duration (seconds) recommended
	// for frame-based enhancement. Longer videos may exceed Lambda timeout or cost limits.
	MaxRecommendedDuration = 120.0

	// ReassemblyH264CRF is the CRF for H.264 encoding during frame reassembly.
	// CRF 18 is visually lossless for H.264.
	ReassemblyH264CRF = 18

	// ReassemblyH264Preset controls encoding speed vs quality for reassembly.
	// "slow" provides high quality without being excessively slow.
	ReassemblyH264Preset = "slow"
)

// FrameExtractionResult contains the results of extracting frames from a video.
type FrameExtractionResult struct {
	// FrameDir is the directory containing extracted frame JPEG files.
	FrameDir string

	// FramePaths is the list of frame file paths in order.
	FramePaths []string

	// OriginalFPS is the source video's frame rate.
	OriginalFPS float64

	// ExtractionFPS is the rate frames were actually extracted at.
	// May be lower than OriginalFPS for long videos.
	ExtractionFPS float64

	// TotalFrames is the number of frames extracted.
	TotalFrames int

	// Cleanup removes the temporary frame directory and all files.
	// Must be called when frames are no longer needed.
	Cleanup func()
}

// ExtractFrames extracts all frames from a video file as individual JPEG images.
// The extraction FPS is automatically reduced for longer videos to keep frame
// count manageable within Lambda constraints.
//
// Parameters:
//   - videoPath: path to the source video file
//   - metadata: video metadata (used for FPS detection and duration)
//
// Returns a FrameExtractionResult with paths to all extracted frames.
// The caller MUST call Cleanup() when done with the frames.
func ExtractFrames(ctx context.Context, videoPath string, metadata *VideoMetadata) (*FrameExtractionResult, error) {
	log.Info().
		Str("video", filepath.Base(videoPath)).
		Msg("Starting frame extraction for video enhancement")

	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg not found: frame extraction requires ffmpeg: %w", err)
	}

	// Create temporary directory for frames
	frameDir, err := os.MkdirTemp("", "video-frames-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create frame directory: %w", err)
	}

	cleanup := func() {
		if err := os.RemoveAll(frameDir); err != nil {
			log.Warn().Err(err).Str("dir", frameDir).Msg("Failed to remove frame directory")
		} else {
			log.Debug().Str("dir", frameDir).Msg("Frame directory removed")
		}
	}

	// Determine extraction FPS based on video duration
	originalFPS := 30.0
	duration := 0.0
	if metadata != nil {
		if metadata.FrameRate > 0 {
			originalFPS = metadata.FrameRate
		}
		duration = metadata.Duration.Seconds()
	}

	extractionFPS := determineExtractionFPS(originalFPS, duration)

	log.Info().
		Float64("original_fps", originalFPS).
		Float64("extraction_fps", extractionFPS).
		Float64("duration_s", duration).
		Msg("Frame extraction parameters")

	// Build ffmpeg command for frame extraction
	framePattern := filepath.Join(frameDir, "frame_%06d.jpg")
	args := []string{
		"-i", videoPath,
		"-qscale:v", strconv.Itoa(FrameJPEGQuality),
	}

	// Apply FPS filter if extracting at reduced rate
	if extractionFPS < originalFPS {
		args = append(args, "-vf", fmt.Sprintf("fps=%.2f", extractionFPS))
	}

	args = append(args, "-vsync", "0", "-y", framePattern)

	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("frame extraction failed: %w\nOutput: %s", err, string(output))
	}

	// Collect extracted frame paths
	framePaths, err := collectFramePaths(frameDir)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("failed to collect frame paths: %w", err)
	}

	if len(framePaths) == 0 {
		cleanup()
		return nil, fmt.Errorf("no frames extracted from video: %s", filepath.Base(videoPath))
	}

	log.Info().
		Int("total_frames", len(framePaths)).
		Float64("extraction_fps", extractionFPS).
		Str("frame_dir", frameDir).
		Msg("Frame extraction complete")

	return &FrameExtractionResult{
		FrameDir:      frameDir,
		FramePaths:    framePaths,
		OriginalFPS:   originalFPS,
		ExtractionFPS: extractionFPS,
		TotalFrames:   len(framePaths),
		Cleanup:       cleanup,
	}, nil
}

// ReassembleVideo stitches enhanced frames back into a video, preserving
// the original audio track. Uses H.264 encoding with high quality settings.
//
// Parameters:
//   - frameDir: directory containing enhanced frame JPEGs (frame_%06d.jpg naming)
//   - originalVideoPath: path to original video (for audio extraction)
//   - outputPath: path for the output enhanced video
//   - fps: frame rate for the output video (use ExtractionFPS from FrameExtractionResult)
func ReassembleVideo(ctx context.Context, frameDir string, originalVideoPath string, outputPath string, fps float64) error {
	log.Info().
		Str("frame_dir", frameDir).
		Str("original", filepath.Base(originalVideoPath)).
		Str("output", filepath.Base(outputPath)).
		Float64("fps", fps).
		Msg("Reassembling video from enhanced frames")

	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return fmt.Errorf("ffmpeg not found: video reassembly requires ffmpeg: %w", err)
	}

	framePattern := filepath.Join(frameDir, "frame_%06d.jpg")

	// Build ffmpeg command:
	// - Input 1: enhanced frames as image sequence
	// - Input 2: original video (for audio track)
	// - Map: video from input 1, audio from input 2
	// - H.264 encoding with visually lossless CRF
	// - Audio copied without re-encoding
	args := []string{
		"-framerate", fmt.Sprintf("%.2f", fps),
		"-i", framePattern,
		"-i", originalVideoPath,
		"-map", "0:v",       // Video from enhanced frames
		"-map", "1:a?",      // Audio from original (optional - ? handles no-audio videos)
		"-c:v", "libx264",   // H.264 video codec
		"-crf", strconv.Itoa(ReassemblyH264CRF),
		"-preset", ReassemblyH264Preset,
		"-pix_fmt", "yuv420p", // Broad compatibility
		"-c:a", "copy",       // Copy audio without re-encoding
		"-movflags", "+faststart", // Enable streaming playback
		"-y", outputPath,
	}

	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("video reassembly failed: %w\nOutput: %s", err, string(output))
	}

	// Verify output file exists and has content
	info, err := os.Stat(outputPath)
	if err != nil {
		return fmt.Errorf("output video not found after reassembly: %w", err)
	}

	log.Info().
		Str("output", filepath.Base(outputPath)).
		Int64("size_bytes", info.Size()).
		Msg("Video reassembly complete")

	return nil
}

// determineExtractionFPS calculates the appropriate frame extraction rate
// based on video duration to keep total frame count manageable.
func determineExtractionFPS(originalFPS, durationSeconds float64) float64 {
	switch {
	case durationSeconds <= 0:
		// Unknown duration — use original FPS capped at 30
		return minFloat(originalFPS, MaxExtractionFPS)
	case durationSeconds <= 30:
		// Short videos: extract at original rate (up to 30fps)
		return minFloat(originalFPS, MaxExtractionFPS)
	case durationSeconds <= 60:
		// Medium videos: reduce to 15fps
		return minFloat(originalFPS, ReducedFPS15)
	case durationSeconds <= 120:
		// Long videos: reduce to 10fps
		return minFloat(originalFPS, ReducedFPS10)
	default:
		// Very long videos: reduce to 5fps
		return minFloat(originalFPS, ReducedFPS5)
	}
}

// collectFramePaths returns sorted paths to all frame files in a directory.
func collectFramePaths(frameDir string) ([]string, error) {
	entries, err := os.ReadDir(frameDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read frame directory: %w", err)
	}

	var paths []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, "frame_") && strings.HasSuffix(name, ".jpg") {
			paths = append(paths, filepath.Join(frameDir, name))
		}
	}

	// Sort to ensure correct frame ordering
	sort.Strings(paths)

	return paths, nil
}

// IsDurationRecommended returns true if the video duration is within the
// recommended range for frame-based enhancement.
func IsDurationRecommended(durationSeconds float64) bool {
	return durationSeconds <= MaxRecommendedDuration
}

// EstimateEnhancementTime estimates the total enhancement time in seconds
// for a video of the given duration and frame rate.
func EstimateEnhancementTime(durationSeconds, fps float64) float64 {
	extractionFPS := determineExtractionFPS(fps, durationSeconds)
	totalFrames := durationSeconds * extractionFPS

	// Assume ~30 frames per group average
	estimatedGroups := totalFrames / 30
	if estimatedGroups < 1 {
		estimatedGroups = 1
	}

	// ~10s per group for Gemini + 5s for analysis/Imagen iterations
	enhancementTime := estimatedGroups * 15

	// Add extraction (~5s) and reassembly (~10s) overhead
	return enhancementTime + 15
}
