package chat

// video_enhance.go orchestrates the multi-step frame-based video enhancement pipeline.
// It decomposes a video into frames, groups similar frames by color histogram,
// enhances representative frames using Gemini 3 Pro Image + Imagen 3, then
// reassembles the video with the enhancements propagated via color LUT.
// See DDR-032: Multi-Step Frame-Based Video Enhancement Pipeline.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fpang/gemini-media-cli/internal/filehandler"

	"github.com/rs/zerolog/log"
)

// VideoEnhancementConfig configures the video enhancement pipeline.
type VideoEnhancementConfig struct {
	// GeminiAPIKey is the Gemini API key for image editing and analysis.
	GeminiAPIKey string

	// VertexAIProject is the GCP project ID for Imagen 3 (optional).
	VertexAIProject string

	// VertexAIRegion is the GCP region for Imagen 3 (optional).
	VertexAIRegion string

	// VertexAIAccessToken is the GCP OAuth2 access token for Imagen 3 (optional).
	VertexAIAccessToken string

	// SimilarityThreshold is the histogram correlation threshold for frame grouping.
	// Default: 0.92
	SimilarityThreshold float64

	// MaxAnalysisIterations is the maximum number of analysis→edit iterations per group.
	// Default: 3
	MaxAnalysisIterations int

	// TargetProfessionalScore is the minimum score to stop iterating.
	// Default: 8.5
	TargetProfessionalScore float64

	// UserFeedback is optional user feedback to incorporate during enhancement.
	// Used during feedback sessions to re-enhance with additional instructions.
	UserFeedback string
}

// VideoEnhancementResult contains the output of the video enhancement pipeline.
type VideoEnhancementResult struct {
	// OutputPath is the path to the enhanced video file.
	OutputPath string

	// TotalFrames is the number of frames processed.
	TotalFrames int

	// TotalGroups is the number of frame groups identified.
	TotalGroups int

	// GroupResults contains per-group enhancement details.
	GroupResults []GroupEnhancementResult

	// TotalDuration is the total processing time.
	TotalDuration time.Duration

	// EnhancementSummary is a human-readable summary of all enhancements.
	EnhancementSummary string
}

// GroupEnhancementResult contains the enhancement result for a single frame group.
type GroupEnhancementResult struct {
	// GroupIndex is the 0-based index of this group.
	GroupIndex int

	// FrameCount is the number of frames in this group.
	FrameCount int

	// Phase1Description is the description from Gemini's initial enhancement.
	Phase1Description string

	// AnalysisIterations is the number of analysis→edit cycles performed.
	AnalysisIterations int

	// FinalScore is the professional quality score after all enhancements.
	FinalScore float64

	// ImprovementsApplied lists the types of improvements made.
	ImprovementsApplied []string
}

// analysisResult mirrors the JSON response from the video enhancement analysis prompt.
type videoAnalysisResult struct {
	OverallAssessment     string                     `json:"overallAssessment"`
	RemainingImprovements []videoAnalysisImprovement `json:"remainingImprovements"`
	ProfessionalScore     float64                    `json:"professionalScore"`
	TargetScore           float64                    `json:"targetScore"`
	NoFurtherEditsNeeded  bool                       `json:"noFurtherEditsNeeded"`
}

type videoAnalysisImprovement struct {
	Type               string `json:"type"`
	Description        string `json:"description"`
	Region             string `json:"region"`
	Impact             string `json:"impact"`
	ImagenSuitable     bool   `json:"imagenSuitable"`
	EditInstruction    string `json:"editInstruction"`
	SafeForPropagation bool   `json:"safeForPropagation"`
}

// EnhanceVideo runs the full multi-step video enhancement pipeline.
//
// Pipeline phases:
//  1. Extract frames from video (ffmpeg)
//  2. Group frames by color histogram similarity
//  3. Enhance representative frames with Gemini 3 Pro Image
//  4. Analyze enhanced frames + apply Imagen 3 surgical edits (iterative)
//  5. Propagate enhancements via color LUT + reassemble video (ffmpeg)
//
// Parameters:
//   - videoPath: path to the source video file
//   - outputPath: path for the enhanced output video
//   - metadata: video metadata (for FPS and duration)
//   - config: enhancement configuration
func EnhanceVideo(ctx context.Context, videoPath string, outputPath string, metadata *filehandler.VideoMetadata, config VideoEnhancementConfig) (*VideoEnhancementResult, error) {
	startTime := time.Now()

	log.Info().
		Str("video_path", videoPath).
		Str("output_path", outputPath).
		Float64("frame_rate", metadata.FrameRate).
		Dur("duration", metadata.Duration).
		Int("width", metadata.Width).
		Int("height", metadata.Height).
		Msg("Starting multi-step video enhancement pipeline (DDR-032)")

	// Apply defaults
	if config.SimilarityThreshold <= 0 {
		config.SimilarityThreshold = filehandler.DefaultSimilarityThreshold
	}
	if config.MaxAnalysisIterations <= 0 {
		config.MaxAnalysisIterations = 3
	}
	if config.TargetProfessionalScore <= 0 {
		config.TargetProfessionalScore = 8.5
	}

	// Initialize AI clients — create SDK client from API key
	genaiClient, err := NewGeminiClient(ctx, config.GeminiAPIKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}
	geminiClient := NewGeminiImageClient(genaiClient)

	var imagenClient *ImagenClient
	if config.VertexAIProject != "" && config.VertexAIAccessToken != "" {
		imagenClient = NewImagenClient(config.VertexAIProject, config.VertexAIRegion, config.VertexAIAccessToken)
		log.Info().Msg("Imagen 3 client configured for surgical edits")
	} else {
		log.Info().Msg("Imagen 3 not configured — skipping mask-based edits")
	}

	// --- Phase 1: Frame Extraction ---
	log.Debug().Msg("Phase 1: Starting frame extraction")

	extraction, err := filehandler.ExtractFrames(ctx, videoPath, metadata)
	if err != nil {
		return nil, fmt.Errorf("phase 1 failed: %w", err)
	}
	defer extraction.Cleanup()

	log.Info().
		Int("total_frames", extraction.TotalFrames).
		Float64("extraction_fps", extraction.ExtractionFPS).
		Msg("Phase 1 complete: frames extracted")

	// --- Phase 2: Frame Grouping ---
	log.Debug().Msg("Phase 2: Starting frame grouping")

	groups, err := filehandler.GroupFramesByHistogram(extraction.FramePaths, config.SimilarityThreshold)
	if err != nil {
		return nil, fmt.Errorf("phase 2 failed: %w", err)
	}

	log.Info().
		Int("total_groups", len(groups)).
		Msg("Phase 2 complete: frames grouped")

	// Create output directory for enhanced frames
	enhancedFrameDir, err := os.MkdirTemp("", "enhanced-frames-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create enhanced frame directory: %w", err)
	}
	defer os.RemoveAll(enhancedFrameDir)

	// --- Phase 3 & 4: Enhance representative frames ---
	groupResults := make([]GroupEnhancementResult, len(groups))
	var summaryParts []string

	for i, group := range groups {
		log.Debug().
			Int("group", i).
			Int("frame_count", group.FrameCount).
			Str("representative", filepath.Base(group.RepresentativePath)).
			Msg("Phase 3-4: Starting group enhancement")

		result, err := enhanceFrameGroup(ctx, geminiClient, imagenClient, group, i, config)
		if err != nil {
			log.Error().Err(err).Int("group", i).Msg("Group enhancement failed, using original frames")
			// On failure, copy original frames to enhanced directory
			if copyErr := copyGroupFrames(group, enhancedFrameDir); copyErr != nil {
				return nil, fmt.Errorf("failed to copy original frames for group %d: %w", i, copyErr)
			}
			groupResults[i] = GroupEnhancementResult{
				GroupIndex: i,
				FrameCount: group.FrameCount,
			}
			continue
		}

		groupResults[i] = result.GroupEnhancementResult

		// Apply color LUT to propagate enhancement to all frames in group
		if result.lutContent != "" {
			log.Info().
				Int("group", i).
				Int("frame_count", group.FrameCount).
				Msg("Propagating enhancement via color LUT")

			err = filehandler.ApplyLUTToFrames(ctx, group.FramePaths, result.lutContent, enhancedFrameDir)
			if err != nil {
				log.Error().Err(err).Int("group", i).Msg("LUT propagation failed, copying enhanced rep + original others")
				if copyErr := copyGroupFrames(group, enhancedFrameDir); copyErr != nil {
					return nil, fmt.Errorf("failed to copy frames for group %d after LUT failure: %w", i, copyErr)
				}
			}
		} else {
			// No LUT — copy original frames (shouldn't happen normally)
			if copyErr := copyGroupFrames(group, enhancedFrameDir); copyErr != nil {
				return nil, fmt.Errorf("failed to copy frames for group %d: %w", i, copyErr)
			}
		}

		if result.Phase1Description != "" {
			summaryParts = append(summaryParts, fmt.Sprintf("Group %d (%d frames): %s", i+1, group.FrameCount, result.Phase1Description))
		}
	}

	// --- Phase 5: Reassemble Video ---
	log.Debug().Msg("Phase 5: Starting video reassembly")

	err = filehandler.ReassembleVideo(ctx, enhancedFrameDir, videoPath, outputPath, extraction.ExtractionFPS)
	if err != nil {
		return nil, fmt.Errorf("phase 5 failed: %w", err)
	}

	totalDuration := time.Since(startTime)

	summary := "Video enhancement complete."
	if len(summaryParts) > 0 {
		summary = fmt.Sprintf("Enhanced %d frame groups: %s", len(groups), joinStrings(summaryParts, "; "))
	}

	log.Info().
		Dur("total_duration", totalDuration).
		Int("groups", len(groups)).
		Int("frames", extraction.TotalFrames).
		Str("output_path", outputPath).
		Msg("Video enhancement pipeline completed")

	return &VideoEnhancementResult{
		OutputPath:         outputPath,
		TotalFrames:        extraction.TotalFrames,
		TotalGroups:        len(groups),
		GroupResults:       groupResults,
		TotalDuration:      totalDuration,
		EnhancementSummary: summary,
	}, nil
}

// joinStrings joins strings with a separator (avoids importing strings for this one use).
func joinStrings(parts []string, sep string) string {
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += sep
		}
		result += p
	}
	return result
}
