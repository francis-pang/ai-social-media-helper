package filehandler

// video_histogram.go provides color histogram computation and frame grouping
// for the multi-step video enhancement pipeline.
// See DDR-032: Multi-Step Frame-Based Video Enhancement Pipeline.

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"math"
	"os"
	"os/exec"
	"strings"

	"github.com/rs/zerolog/log"
)

// Histogram constants for frame grouping (DDR-032).
const (
	// HistogramBins is the number of bins per RGB channel.
	// 32 bins provides enough granularity for scene change detection
	// while being robust to noise and minor lighting variations.
	HistogramBins = 32

	// DefaultSimilarityThreshold is the minimum histogram correlation
	// for two consecutive frames to be considered part of the same group.
	// - 0.95+: Too sensitive (splits on minor camera movement)
	// - 0.92: Good balance (groups continuous scenes, splits on scene changes)
	// - 0.85: Too loose (may group different scenes with similar palettes)
	DefaultSimilarityThreshold = 0.92
)

// ColorHistogram is a 3D RGB color histogram with HistogramBins bins per channel.
// Stored as a flat array for cache efficiency: index = r*B*B + g*B + b
// where B = HistogramBins.
type ColorHistogram struct {
	Bins       [HistogramBins * HistogramBins * HistogramBins]float64
	TotalPixels int
}

// FrameGroup represents a group of consecutive frames with similar visual content.
type FrameGroup struct {
	// StartIndex is the index of the first frame in this group (0-based).
	StartIndex int

	// EndIndex is the index of the last frame in this group (inclusive, 0-based).
	EndIndex int

	// RepresentativeIndex is the index of the frame chosen to represent this group.
	// This is the middle frame of the group.
	RepresentativeIndex int

	// RepresentativePath is the file path to the representative frame.
	RepresentativePath string

	// FramePaths is the list of all frame file paths in this group.
	FramePaths []string

	// FrameCount is the number of frames in this group.
	FrameCount int
}

// ComputeHistogram computes a normalized 3D RGB color histogram for an image file.
// The image is loaded from disk and processed in a single pass.
func ComputeHistogram(imagePath string) (*ColorHistogram, error) {
	f, err := os.Open(imagePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open image: %w", err)
	}
	defer f.Close()

	img, err := jpeg.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("failed to decode JPEG: %w", err)
	}

	return ComputeHistogramFromImage(img), nil
}

// ComputeHistogramFromImage computes a normalized 3D RGB color histogram
// from an in-memory image.
func ComputeHistogramFromImage(img image.Image) *ColorHistogram {
	hist := &ColorHistogram{}
	bounds := img.Bounds()

	binSize := 256 / HistogramBins

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			// RGBA() returns 16-bit values; scale to 8-bit
			r8 := int(r >> 8)
			g8 := int(g >> 8)
			b8 := int(b >> 8)

			// Map to histogram bin
			rBin := r8 / binSize
			gBin := g8 / binSize
			bBin := b8 / binSize

			// Clamp to valid range
			if rBin >= HistogramBins {
				rBin = HistogramBins - 1
			}
			if gBin >= HistogramBins {
				gBin = HistogramBins - 1
			}
			if bBin >= HistogramBins {
				bBin = HistogramBins - 1
			}

			idx := rBin*HistogramBins*HistogramBins + gBin*HistogramBins + bBin
			hist.Bins[idx]++
			hist.TotalPixels++
		}
	}

	// Normalize histogram
	if hist.TotalPixels > 0 {
		total := float64(hist.TotalPixels)
		for i := range hist.Bins {
			hist.Bins[i] /= total
		}
	}

	return hist
}

// CompareHistograms computes the Pearson correlation coefficient between
// two color histograms. Returns a value in [-1, 1]:
//   - 1.0: identical histograms
//   - 0.0: uncorrelated
//   - -1.0: inverse histograms
//
// This is equivalent to OpenCV's HISTCMP_CORREL method.
func CompareHistograms(h1, h2 *ColorHistogram) float64 {
	n := len(h1.Bins)

	// Compute means
	var mean1, mean2 float64
	for i := 0; i < n; i++ {
		mean1 += h1.Bins[i]
		mean2 += h2.Bins[i]
	}
	mean1 /= float64(n)
	mean2 /= float64(n)

	// Compute Pearson correlation
	var numerator, denom1, denom2 float64
	for i := 0; i < n; i++ {
		d1 := h1.Bins[i] - mean1
		d2 := h2.Bins[i] - mean2
		numerator += d1 * d2
		denom1 += d1 * d1
		denom2 += d2 * d2
	}

	denom := math.Sqrt(denom1 * denom2)
	if denom < 1e-10 {
		// Both histograms are essentially uniform — consider identical
		return 1.0
	}

	return numerator / denom
}

// GroupFramesByHistogram groups consecutive frames by color histogram similarity.
// Frames with histogram correlation >= threshold are placed in the same group.
// Each group selects the middle frame as its representative.
//
// Parameters:
//   - framePaths: ordered list of frame file paths
//   - threshold: minimum correlation to consider frames similar (default: 0.92)
//
// Returns a slice of FrameGroup, each containing one or more consecutive frames.
func GroupFramesByHistogram(framePaths []string, threshold float64) ([]FrameGroup, error) {
	if len(framePaths) == 0 {
		return nil, fmt.Errorf("no frames to group")
	}

	if threshold <= 0 || threshold > 1 {
		threshold = DefaultSimilarityThreshold
	}

	log.Info().
		Int("total_frames", len(framePaths)).
		Float64("threshold", threshold).
		Msg("Grouping frames by color histogram similarity")

	// Compute histogram for the first frame
	prevHist, err := ComputeHistogram(framePaths[0])
	if err != nil {
		return nil, fmt.Errorf("failed to compute histogram for frame 0: %w", err)
	}

	// Track group boundaries
	type boundary struct {
		start int
		end   int
	}
	var boundaries []boundary
	currentStart := 0

	// Compare consecutive frames
	for i := 1; i < len(framePaths); i++ {
		currentHist, err := ComputeHistogram(framePaths[i])
		if err != nil {
			log.Warn().Err(err).Int("frame", i).Msg("Failed to compute histogram, starting new group")
			// On error, close current group and start a new one
			boundaries = append(boundaries, boundary{start: currentStart, end: i - 1})
			currentStart = i
			prevHist = currentHist
			continue
		}

		correlation := CompareHistograms(prevHist, currentHist)

		if correlation < threshold {
			// Scene change detected — close current group, start new
			log.Debug().
				Int("frame", i).
				Float64("correlation", correlation).
				Msg("Scene change detected")

			boundaries = append(boundaries, boundary{start: currentStart, end: i - 1})
			currentStart = i
		}

		prevHist = currentHist
	}

	// Close final group
	boundaries = append(boundaries, boundary{start: currentStart, end: len(framePaths) - 1})

	// Build FrameGroup objects
	groups := make([]FrameGroup, len(boundaries))
	for i, b := range boundaries {
		frameCount := b.end - b.start + 1
		repIdx := b.start + frameCount/2 // Middle frame

		groupPaths := make([]string, frameCount)
		copy(groupPaths, framePaths[b.start:b.end+1])

		groups[i] = FrameGroup{
			StartIndex:          b.start,
			EndIndex:            b.end,
			RepresentativeIndex: repIdx,
			RepresentativePath:  framePaths[repIdx],
			FramePaths:          groupPaths,
			FrameCount:          frameCount,
		}
	}

	log.Info().
		Int("total_groups", len(groups)).
		Int("total_frames", len(framePaths)).
		Float64("avg_group_size", float64(len(framePaths))/float64(len(groups))).
		Msg("Frame grouping complete")

	// Log group sizes for debugging
	for i, g := range groups {
		log.Debug().
			Int("group", i).
			Int("start", g.StartIndex).
			Int("end", g.EndIndex).
			Int("representative", g.RepresentativeIndex).
			Int("frame_count", g.FrameCount).
			Msg("Frame group")
	}

	return groups, nil
}

// ComputeColorLUT computes a 3D color Look-Up Table that maps colors from
// the original image to the enhanced image. This LUT can then be applied
// to all frames in a group to propagate the enhancement consistently.
//
// The LUT uses 64 entries per channel (64×64×64 = 262,144 entries) for
// smooth color mapping with minimal banding.
//
// Parameters:
//   - originalPath: path to the original representative frame
//   - enhancedPath: path to the enhanced representative frame
//
// Returns a .cube format LUT string that can be used with ffmpeg's lut3d filter.
func ComputeColorLUT(originalPath, enhancedPath string) (string, error) {
	log.Debug().
		Str("original", originalPath).
		Str("enhanced", enhancedPath).
		Msg("Computing color LUT from original→enhanced mapping")

	origFile, err := os.Open(originalPath)
	if err != nil {
		return "", fmt.Errorf("failed to open original frame: %w", err)
	}
	defer origFile.Close()

	origImg, err := jpeg.Decode(origFile)
	if err != nil {
		return "", fmt.Errorf("failed to decode original frame: %w", err)
	}

	enhFile, err := os.Open(enhancedPath)
	if err != nil {
		return "", fmt.Errorf("failed to open enhanced frame: %w", err)
	}
	defer enhFile.Close()

	enhImg, err := jpeg.Decode(enhFile)
	if err != nil {
		return "", fmt.Errorf("failed to decode enhanced frame: %w", err)
	}

	return computeLUTFromImages(origImg, enhImg)
}

// ComputeColorLUTFromData computes a 3D color LUT from raw image data.
func ComputeColorLUTFromData(originalData, enhancedData []byte) (string, error) {
	origImg, err := jpeg.Decode(bytes.NewReader(originalData))
	if err != nil {
		return "", fmt.Errorf("failed to decode original image: %w", err)
	}

	enhImg, err := jpeg.Decode(bytes.NewReader(enhancedData))
	if err != nil {
		return "", fmt.Errorf("failed to decode enhanced image: %w", err)
	}

	return computeLUTFromImages(origImg, enhImg)
}

// lutSize is the number of entries per channel in the 3D LUT.
const lutSize = 64

// computeLUTFromImages builds a .cube format 3D LUT by sampling the color
// transformation between original and enhanced images.
func computeLUTFromImages(origImg, enhImg image.Image) (string, error) {
	origBounds := origImg.Bounds()
	enhBounds := enhImg.Bounds()

	// Images may have different dimensions (Gemini may resize).
	// We sample from both using normalized coordinates.
	origW := origBounds.Dx()
	origH := origBounds.Dy()
	enhW := enhBounds.Dx()
	enhH := enhBounds.Dy()

	if origW == 0 || origH == 0 || enhW == 0 || enhH == 0 {
		return "", fmt.Errorf("invalid image dimensions: orig=%dx%d, enh=%dx%d", origW, origH, enhW, enhH)
	}

	// Build accumulation buffers for the LUT
	// For each (r,g,b) bin, accumulate the sum of enhanced colors and count
	type lutEntry struct {
		sumR, sumG, sumB float64
		count            int
	}
	lut := make([]lutEntry, lutSize*lutSize*lutSize)

	binSize := 256.0 / float64(lutSize)

	// Sample pixels from both images at corresponding normalized positions
	sampleStep := 2 // Sample every 2nd pixel for speed
	for y := 0; y < origH; y += sampleStep {
		// Map y to enhanced image coordinates
		enhY := y * enhH / origH
		if enhY >= enhH {
			enhY = enhH - 1
		}

		for x := 0; x < origW; x += sampleStep {
			// Map x to enhanced image coordinates
			enhX := x * enhW / origW
			if enhX >= enhW {
				enhX = enhW - 1
			}

			// Get original pixel color
			oR, oG, oB, _ := origImg.At(origBounds.Min.X+x, origBounds.Min.Y+y).RGBA()
			// Get enhanced pixel color at corresponding position
			eR, eG, eB, _ := enhImg.At(enhBounds.Min.X+enhX, enhBounds.Min.Y+enhY).RGBA()

			// Scale from 16-bit to 8-bit
			oR8 := float64(oR >> 8)
			oG8 := float64(oG >> 8)
			oB8 := float64(oB >> 8)
			eR8 := float64(eR >> 8)
			eG8 := float64(eG >> 8)
			eB8 := float64(eB >> 8)

			// Map to LUT bin
			rBin := int(oR8 / binSize)
			gBin := int(oG8 / binSize)
			bBin := int(oB8 / binSize)
			if rBin >= lutSize {
				rBin = lutSize - 1
			}
			if gBin >= lutSize {
				gBin = lutSize - 1
			}
			if bBin >= lutSize {
				bBin = lutSize - 1
			}

			idx := rBin*lutSize*lutSize + gBin*lutSize + bBin
			lut[idx].sumR += eR8
			lut[idx].sumG += eG8
			lut[idx].sumB += eB8
			lut[idx].count++
		}
	}

	// Generate .cube file content
	// .cube format: https://resolve.cafe/developers/luts/
	var sb strings.Builder
	sb.WriteString("# Generated by ai-social-media-helper (DDR-032)\n")
	sb.WriteString("TITLE \"Video Enhancement LUT\"\n")
	sb.WriteString(fmt.Sprintf("LUT_3D_SIZE %d\n", lutSize))
	sb.WriteString("\n")

	// Write LUT entries: iterate B (fastest), G, R (slowest)
	for rBin := 0; rBin < lutSize; rBin++ {
		for gBin := 0; gBin < lutSize; gBin++ {
			for bBin := 0; bBin < lutSize; bBin++ {
				idx := rBin*lutSize*lutSize + gBin*lutSize + bBin
				entry := lut[idx]

				var outR, outG, outB float64
				if entry.count > 0 {
					// Average the enhanced colors for this bin
					outR = entry.sumR / float64(entry.count) / 255.0
					outG = entry.sumG / float64(entry.count) / 255.0
					outB = entry.sumB / float64(entry.count) / 255.0
				} else {
					// No samples for this bin — use identity mapping
					outR = float64(rBin) / float64(lutSize-1)
					outG = float64(gBin) / float64(lutSize-1)
					outB = float64(bBin) / float64(lutSize-1)
				}

				// Clamp to [0, 1]
				outR = math.Max(0, math.Min(1, outR))
				outG = math.Max(0, math.Min(1, outG))
				outB = math.Max(0, math.Min(1, outB))

				sb.WriteString(fmt.Sprintf("%.6f %.6f %.6f\n", outR, outG, outB))
			}
		}
	}

	return sb.String(), nil
}

// ApplyLUTToFrames applies a .cube format 3D LUT to all frames in a directory
// using ffmpeg's lut3d filter. Enhanced frames are written to the output directory.
//
// Parameters:
//   - framePaths: paths to the source frames to apply the LUT to
//   - lutContent: the .cube format LUT string
//   - outputDir: directory where LUT-applied frames will be written
func ApplyLUTToFrames(ctx context.Context, framePaths []string, lutContent string, outputDir string) error {
	if len(framePaths) == 0 {
		return nil
	}

	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return fmt.Errorf("ffmpeg not found: LUT application requires ffmpeg: %w", err)
	}

	// Write LUT to temporary file
	lutFile, err := os.CreateTemp("", "enhancement-lut-*.cube")
	if err != nil {
		return fmt.Errorf("failed to create temp LUT file: %w", err)
	}
	lutPath := lutFile.Name()
	defer os.Remove(lutPath)

	if _, err := lutFile.WriteString(lutContent); err != nil {
		lutFile.Close()
		return fmt.Errorf("failed to write LUT file: %w", err)
	}
	lutFile.Close()

	// Ensure output directory exists
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	log.Info().
		Int("frame_count", len(framePaths)).
		Str("output_dir", outputDir).
		Msg("Applying color LUT to frames")

	// Apply LUT to each frame using ffmpeg
	for i, framePath := range framePaths {
		outputPath := fmt.Sprintf("%s/frame_%06d.jpg", outputDir, i+1)

		cmd := exec.CommandContext(ctx, ffmpegPath,
			"-i", framePath,
			"-vf", fmt.Sprintf("lut3d='%s'", lutPath),
			"-qscale:v", "2",
			"-y", outputPath,
		)

		output, err := cmd.CombinedOutput()
		if err != nil {
			log.Warn().
				Err(err).
				Int("frame", i).
				Str("output", string(output)).
				Msg("Failed to apply LUT to frame, copying original")

			// Fallback: copy original frame
			data, readErr := os.ReadFile(framePath)
			if readErr != nil {
				return fmt.Errorf("failed to apply LUT and read fallback for frame %d: %w", i, readErr)
			}
			if writeErr := os.WriteFile(outputPath, data, 0o644); writeErr != nil {
				return fmt.Errorf("failed to write fallback frame %d: %w", i, writeErr)
			}
		}
	}

	log.Info().
		Int("frame_count", len(framePaths)).
		Msg("Color LUT applied to all frames")

	return nil
}
