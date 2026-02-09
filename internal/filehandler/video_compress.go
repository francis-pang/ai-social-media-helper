package filehandler

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/fpang/gemini-media-cli/internal/metrics"
	"github.com/rs/zerolog/log"
)

// Compression constants for Gemini 3 Pro optimization (DDR-018).
// These are MAXIMUM values - we never upscale lower quality sources.
const (
	// MaxResolution is the maximum resolution (longest edge) for single-tile processing.
	// Gemini 3 Pro treats frames ≤768px as single tiles (258 tokens/frame at MEDIUM).
	MaxResolution = 768

	// MaxFrameRate is the maximum frames per second for temporal analysis.
	// Higher rates waste tokens without improving AI analysis quality.
	MaxFrameRate = 5.0

	// VideoCRF is the Constant Rate Factor for AV1 encoding.
	// AV1 handles higher CRF values well (range 0-63). 35 provides good quality/size balance.
	VideoCRF = 35

	// VideoPreset controls encoding speed vs efficiency (0-13, lower = slower but better).
	// Preset 4 provides high efficiency since encoding time is not a priority.
	VideoPreset = 4

	// AudioBitrate is the target audio bitrate for Opus encoding.
	// Opus excels at low bitrates; 24kbps is sufficient for speech/sound analysis.
	AudioBitrate = "24k"
)

// CheckFFmpegAvailable checks if ffmpeg is available in the system PATH.
// This can be called at startup to validate video compression capability.
// Returns nil if ffmpeg is available, or an error describing the issue.
func CheckFFmpegAvailable() error {
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		return fmt.Errorf("ffmpeg not found in PATH: video compression will be unavailable. Install FFmpeg with: brew install ffmpeg (macOS) or apt install ffmpeg (Linux)")
	}
	log.Debug().Str("path", path).Msg("ffmpeg found")
	return nil
}

// IsFFmpegAvailable returns true if ffmpeg is available in the system PATH.
// This is a convenience wrapper around CheckFFmpegAvailable for boolean checks.
func IsFFmpegAvailable() bool {
	return CheckFFmpegAvailable() == nil
}

// CompressVideoForGemini compresses a video for optimal Gemini 3 Pro upload.
// Uses AV1 video codec and Opus audio codec for maximum efficiency (DDR-018).
//
// Key features:
//   - AV1 video codec (30-50% smaller than H.265)
//   - Opus audio codec (efficient at low bitrates)
//   - No upscaling: preserves source quality if lower than targets
//   - Preserves aspect ratio (no square padding)
//   - WebM container (native for AV1+Opus, preferred by Google)
//
// The cleanup function MUST be called to remove the temporary compressed file.
// Metadata should be extracted from the ORIGINAL file before calling this function,
// as compression may strip vendor-specific metadata (GPS, timestamps, device info).
//
// Returns:
//   - outputPath: Path to the compressed temporary file
//   - outputSize: Size of the compressed file in bytes
//   - cleanup: Function to delete the temporary file (must be called)
//   - err: Error if compression fails
func CompressVideoForGemini(ctx context.Context, inputPath string, metadata *VideoMetadata) (
	outputPath string,
	outputSize int64,
	cleanup func(),
	err error,
) {
	// Get input file size for logging
	var inputSize int64
	if inputInfo, err := os.Stat(inputPath); err == nil {
		inputSize = inputInfo.Size()
	}

	var targetResolution int = MaxResolution
	var targetFPS float64 = MaxFrameRate
	if metadata != nil {
		if metadata.Width > 0 && metadata.Height > 0 {
			targetResolution = minInt(MaxResolution, maxInt(metadata.Width, metadata.Height))
		}
		if metadata.FrameRate > 0 {
			targetFPS = minFloat(MaxFrameRate, metadata.FrameRate)
		}
	}

	log.Info().
		Str("input_path", inputPath).
		Int64("input_size_bytes", inputSize).
		Int("target_resolution", targetResolution).
		Float64("target_fps", targetFPS).
		Int("target_crf", VideoCRF).
		Str("target_audio_bitrate", AudioBitrate).
		Msg("Starting video compression for Gemini optimization")

	// Check if ffmpeg is available
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return "", 0, nil, fmt.Errorf("ffmpeg not found in PATH: %w", err)
	}

	// Create temporary output file
	tempFile, err := os.CreateTemp("", "gemini-video-*.webm")
	if err != nil {
		return "", 0, nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	outputPath = tempFile.Name()
	tempFile.Close()

	// Create cleanup function
	cleanup = func() {
		if err := os.Remove(outputPath); err != nil && !os.IsNotExist(err) {
			log.Warn().Err(err).Str("path", outputPath).Msg("Failed to remove compressed temp file")
		} else {
			log.Debug().Str("path", outputPath).Msg("Compressed temp file removed")
		}
	}

	// Build FFmpeg arguments with smart no-upscaling logic
	args := buildFFmpegArgs(inputPath, outputPath, metadata)

	log.Debug().
		Strs("args", args).
		Msg("Running FFmpeg compression")

	// Run FFmpeg with context for cancellation support
	ffmpegStart := time.Now()
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	output, err := cmd.CombinedOutput()
	ffmpegElapsed := time.Since(ffmpegStart)
	if err != nil {
		cleanup() // Clean up temp file on error
		log.Warn().
			Err(err).
			Str("input_path", inputPath).
			Str("ffmpeg_output", string(output)).
			Dur("duration", ffmpegElapsed).
			Msg("FFmpeg compression failed, falling back to defaults")
		metrics.New("AiSocialMedia").
			Metric("VideoCompressionMs", float64(ffmpegElapsed.Milliseconds()), metrics.UnitMilliseconds).
			Count("VideoCompressionErrors").
			Flush()
		return "", 0, nil, fmt.Errorf("ffmpeg compression failed: %w\nOutput: %s", err, string(output))
	}

	// Get compressed file size
	info, err := os.Stat(outputPath)
	if err != nil {
		cleanup()
		return "", 0, nil, fmt.Errorf("failed to stat compressed file: %w", err)
	}
	outputSize = info.Size()

	compressionRatio := float64(0)
	if outputSize > 0 {
		compressionRatio = float64(inputSize) / float64(outputSize)
	}

	// Get video duration for logging
	var duration time.Duration
	if metadata != nil && metadata.Duration > 0 {
		duration = metadata.Duration
	}

	// Emit video compression metrics
	metrics.New("AiSocialMedia").
		Metric("VideoCompressionMs", float64(ffmpegElapsed.Milliseconds()), metrics.UnitMilliseconds).
		Metric("MediaFileSizeBytes", float64(inputSize), metrics.UnitBytes).
		Metric("VideoCompressionRatio", compressionRatio, metrics.UnitNone).
		Count("VideoCompressions").
		Flush()

	log.Info().
		Str("input_path", inputPath).
		Str("output_path", outputPath).
		Int64("input_size_bytes", inputSize).
		Int64("output_size_bytes", outputSize).
		Dur("duration", duration).
		Dur("compression_time", ffmpegElapsed).
		Float64("compression_ratio", compressionRatio).
		Msg("Video compression complete")

	return outputPath, outputSize, cleanup, nil
}

// buildFFmpegArgs constructs FFmpeg arguments with smart no-upscaling logic.
// Never upscales any attribute - if source is lower quality than target, keeps original.
func buildFFmpegArgs(inputPath, outputPath string, metadata *VideoMetadata) []string {
	args := []string{"-i", inputPath}

	// Video codec: AV1 via libsvtav1
	args = append(args, "-c:v", "libsvtav1")
	args = append(args, "-preset", strconv.Itoa(VideoPreset))
	args = append(args, "-crf", strconv.Itoa(VideoCRF))

	// Frame rate: min(MaxFrameRate, source_fps) - never upscale
	if metadata != nil && metadata.FrameRate > 0 {
		targetFPS := minFloat(MaxFrameRate, metadata.FrameRate)
		args = append(args, "-r", fmt.Sprintf("%.2f", targetFPS))
		log.Debug().
			Float64("source_fps", metadata.FrameRate).
			Float64("target_fps", targetFPS).
			Msg("Frame rate: using min(max, source)")
	} else {
		// No metadata available, cap at max
		args = append(args, "-r", fmt.Sprintf("%.2f", MaxFrameRate))
	}

	// Resolution: scale down only if larger than MaxResolution, preserve aspect ratio
	// scale='min(768,iw)':-2 keeps aspect ratio and ensures even dimensions
	// NO padding - we preserve native aspect ratio
	vf := fmt.Sprintf("scale='min(%d,iw)':-2,format=yuv420p", MaxResolution)
	args = append(args, "-vf", vf)

	// Stream mapping: video required, audio optional (handles videos without audio)
	args = append(args, "-map", "0:v:0", "-map", "0:a?")

	// Audio codec: Opus (most efficient for Gemini)
	args = append(args, "-c:a", "libopus")
	args = append(args, "-b:a", AudioBitrate)
	args = append(args, "-vbr", "on")
	args = append(args, "-ac", "1") // Mono

	// Audio sample rate: Round up to nearest Opus-supported rate.
	// libopus only supports: 48000, 24000, 16000, 12000, 8000 Hz.
	if metadata != nil && metadata.AudioRate > 0 {
		targetRate := roundUpToOpusSampleRate(metadata.AudioRate)
		args = append(args, "-ar", strconv.Itoa(targetRate))
		log.Debug().
			Int("source_rate", metadata.AudioRate).
			Int("target_rate", targetRate).
			Msg("Audio sample rate: rounded to nearest Opus rate")
	} else {
		// Default to 48kHz if no metadata
		args = append(args, "-ar", "48000")
		log.Debug().
			Int("target_rate", 48000).
			Msg("Audio sample rate: defaulting to 48kHz (no metadata)")
	}

	// Overwrite output file
	args = append(args, "-y", outputPath)

	return args
}

// minFloat returns the smaller of two float64 values.
func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// minInt returns the smaller of two int values.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// maxInt returns the larger of two int values.
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// opusSampleRates are the sample rates supported by libopus, in descending order.
var opusSampleRates = []int{48000, 24000, 16000, 12000, 8000}

// roundUpToOpusSampleRate returns the smallest Opus-supported sample rate
// that is >= the source rate. If source exceeds all rates, returns 48000.
func roundUpToOpusSampleRate(sourceRate int) int {
	// Find the smallest rate that is >= sourceRate
	for i := len(opusSampleRates) - 1; i >= 0; i-- {
		if opusSampleRates[i] >= sourceRate {
			return opusSampleRates[i]
		}
	}
	// Source exceeds max, use highest (48000)
	return opusSampleRates[0]
}
