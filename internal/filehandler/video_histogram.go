package filehandler

// video_histogram.go provides color histogram computation and frame grouping
// for the multi-step video enhancement pipeline.
// See DDR-032: Multi-Step Frame-Based Video Enhancement Pipeline.

import (
	"fmt"
	"image"
	"image/jpeg"
	"math"
	"os"

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
	Bins        [HistogramBins * HistogramBins * HistogramBins]float64
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
