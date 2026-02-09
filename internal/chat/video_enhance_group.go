package chat

// video_enhance_group.go handles per-group frame enhancement: Gemini enhancement,
// iterative analysis with Imagen 3 surgical edits, and utility functions for
// image I/O and frame copying.
// See DDR-032: Multi-Step Frame-Based Video Enhancement Pipeline.

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"

	"github.com/fpang/gemini-media-cli/internal/assets"
	"github.com/fpang/gemini-media-cli/internal/filehandler"
	"github.com/fpang/gemini-media-cli/internal/jsonutil"

	"github.com/rs/zerolog/log"
)

// internalGroupResult extends GroupEnhancementResult with internal state.
type internalGroupResult struct {
	GroupEnhancementResult
	lutContent string // .cube LUT for propagation
}

// enhanceFrameGroup enhances a single frame group's representative frame
// through all phases (Gemini enhancement → analysis → Imagen iteration).
func enhanceFrameGroup(ctx context.Context, geminiClient *GeminiImageClient, imagenClient *ImagenClient, group filehandler.FrameGroup, groupIndex int, config VideoEnhancementConfig) (*internalGroupResult, error) {
	log.Debug().
		Int("group_index", groupIndex).
		Int("frame_count", group.FrameCount).
		Msg("enhanceFrameGroup: Starting group enhancement")

	result := &internalGroupResult{
		GroupEnhancementResult: GroupEnhancementResult{
			GroupIndex: groupIndex,
			FrameCount: group.FrameCount,
		},
	}

	// Read the representative frame
	repData, err := os.ReadFile(group.RepresentativePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read representative frame: %w", err)
	}

	// --- Phase 3: Gemini 3 Pro Image Enhancement ---
	instruction := assets.VideoEnhancementSystemPrompt
	if config.UserFeedback != "" {
		instruction = fmt.Sprintf("%s\n\nADDITIONAL USER FEEDBACK:\n%s", instruction, config.UserFeedback)
	}

	geminiResult, err := geminiClient.EditImage(ctx, repData, "image/jpeg", instruction, "")
	if err != nil {
		return nil, fmt.Errorf("Gemini enhancement failed: %w", err)
	}

	result.Phase1Description = geminiResult.Text
	enhancedData := geminiResult.ImageData
	enhancedMIME := geminiResult.ImageMIMEType

	log.Info().
		Int("group", groupIndex).
		Int("enhanced_bytes", len(enhancedData)).
		Msg("Phase 3 complete: Gemini enhancement done")

	// --- Phase 4: Analysis + Imagen Iteration ---
	for iteration := 0; iteration < config.MaxAnalysisIterations; iteration++ {
		log.Info().
			Int("group", groupIndex).
			Int("iteration", iteration+1).
			Msg("Phase 4: Analyzing enhanced frame for further improvements")

		// Analyze the enhanced frame
		analysisText, err := geminiClient.AnalyzeImage(ctx, enhancedData, enhancedMIME, assets.VideoEnhancementAnalysisPrompt, "")
		if err != nil {
			log.Warn().Err(err).Int("group", groupIndex).Int("iteration", iteration+1).
				Msg("Analysis failed, stopping iterations")
			break
		}

		// Parse analysis result
		analysis, err := parseVideoAnalysis(analysisText)
		if err != nil {
			log.Warn().Err(err).Int("group", groupIndex).
				Str("raw_response", truncateString(analysisText, 500)).
				Msg("Failed to parse analysis response, stopping iterations")
			break
		}

		result.FinalScore = analysis.ProfessionalScore

		log.Info().
			Float64("score", analysis.ProfessionalScore).
			Int("improvements", len(analysis.RemainingImprovements)).
			Bool("no_further_edits", analysis.NoFurtherEditsNeeded).
			Msg("Analysis result")

		// Check if we've reached the target quality
		if analysis.NoFurtherEditsNeeded || analysis.ProfessionalScore >= config.TargetProfessionalScore {
			log.Info().
				Int("group", groupIndex).
				Float64("score", analysis.ProfessionalScore).
				Msg("Target quality reached, stopping iterations")
			break
		}

		// Filter improvements safe for video propagation
		var safeImprovements []videoAnalysisImprovement
		for _, imp := range analysis.RemainingImprovements {
			if imp.SafeForPropagation && (imp.Impact == "high" || imp.Impact == "medium") {
				safeImprovements = append(safeImprovements, imp)
			}
		}

		if len(safeImprovements) == 0 {
			log.Info().
				Int("group", groupIndex).
				Msg("No safe propagation improvements remaining")
			break
		}

		// Apply improvements
		enhancedData, enhancedMIME, err = applyVideoImprovements(ctx, geminiClient, imagenClient, enhancedData, enhancedMIME, safeImprovements, config)
		if err != nil {
			log.Warn().Err(err).Int("group", groupIndex).
				Msg("Improvement application failed, using current enhanced version")
			break
		}

		result.AnalysisIterations = iteration + 1
		for _, imp := range safeImprovements {
			result.ImprovementsApplied = append(result.ImprovementsApplied, imp.Type)
		}
	}

	// Compute color LUT from original → final enhanced representative
	// Write the enhanced representative frame to a temp file for LUT computation
	enhancedRepPath, err := writeTempJPEG(enhancedData, "enhanced-rep-*.jpg")
	if err != nil {
		return nil, fmt.Errorf("failed to write enhanced representative: %w", err)
	}
	defer os.Remove(enhancedRepPath)

	lutContent, err := filehandler.ComputeColorLUT(group.RepresentativePath, enhancedRepPath)
	if err != nil {
		log.Warn().Err(err).Int("group", groupIndex).
			Msg("LUT computation failed — enhancement won't propagate to other frames")
	} else {
		result.lutContent = lutContent
	}

	log.Info().
		Int("group_index", groupIndex).
		Int("iterations", result.AnalysisIterations).
		Float64("final_score", result.FinalScore).
		Msg("enhanceFrameGroup: Group enhancement completed")

	return result, nil
}

// applyVideoImprovements applies a set of improvements to an enhanced frame.
// It first tries Gemini for global edits, then Imagen for localized mask-based edits.
func applyVideoImprovements(ctx context.Context, geminiClient *GeminiImageClient, imagenClient *ImagenClient, imageData []byte, imageMIME string, improvements []videoAnalysisImprovement, config VideoEnhancementConfig) ([]byte, string, error) {
	log.Debug().
		Int("improvements_count", len(improvements)).
		Msg("applyVideoImprovements: Starting improvement application")

	currentData := imageData
	currentMIME := imageMIME

	// Separate Imagen-suitable (localized) and global improvements
	var globalInstructions []string
	var imagenEdits []videoAnalysisImprovement

	for _, imp := range improvements {
		if imp.ImagenSuitable && imagenClient != nil && imagenClient.IsConfigured() {
			imagenEdits = append(imagenEdits, imp)
		} else {
			globalInstructions = append(globalInstructions, imp.EditInstruction)
		}
	}

	// Apply global improvements via Gemini in a single pass
	if len(globalInstructions) > 0 {
		combinedInstruction := "Apply these specific improvements to the video frame:\n"
		for i, inst := range globalInstructions {
			combinedInstruction += fmt.Sprintf("%d. %s\n", i+1, inst)
		}

		result, err := geminiClient.EditImage(ctx, currentData, currentMIME, combinedInstruction, "")
		if err != nil {
			log.Warn().Err(err).Msg("Gemini global improvement pass failed")
		} else {
			currentData = result.ImageData
			currentMIME = result.ImageMIMEType
			log.Info().
				Int("improvements", len(globalInstructions)).
				Msg("Global improvements applied via Gemini")
		}
	}

	// Apply localized improvements via Imagen 3
	for _, edit := range imagenEdits {
		if imagenClient == nil || !imagenClient.IsConfigured() {
			break
		}

		log.Debug().
			Str("type", edit.Type).
			Str("region", edit.Region).
			Str("instruction", truncateString(edit.EditInstruction, 100)).
			Msg("applyVideoImprovements: Attempting Imagen improvement")

		// Generate mask for the region — decode temporarily for dimensions
		width, height, err := getImageDimensions(currentData)
		if err != nil {
			log.Warn().Err(err).Str("region", edit.Region).
				Msg("Failed to get image dimensions for mask generation")
			continue
		}

		maskData, err := GenerateRegionMask(width, height, edit.Region)
		if err != nil {
			log.Warn().Err(err).Str("region", edit.Region).
				Msg("Failed to generate region mask")
			continue
		}

		editMode := "inpainting-remove"
		if edit.Type == "background-cleanup" || edit.Type == "blemish-removal" {
			editMode = "inpainting-remove"
		}

		imagenResult, err := imagenClient.EditWithMask(ctx, currentData, maskData, edit.EditInstruction, editMode)
		if err != nil {
			log.Warn().Err(err).
				Str("type", edit.Type).
				Str("region", edit.Region).
				Msg("Imagen edit failed, skipping")
			continue
		}

		currentData = imagenResult.ImageData
		currentMIME = imagenResult.MIMEType
		log.Debug().
			Str("type", edit.Type).
			Str("region", edit.Region).
			Int("result_bytes", len(imagenResult.ImageData)).
			Msg("applyVideoImprovements: Imagen improvement applied successfully")
	}

	return currentData, currentMIME, nil
}

// parseVideoAnalysis parses the JSON response from the video enhancement analysis.
func parseVideoAnalysis(text string) (*videoAnalysisResult, error) {
	result, err := jsonutil.ParseJSON[videoAnalysisResult](text)
	if err != nil {
		return nil, fmt.Errorf("video analysis: %w", err)
	}
	return &result, nil
}

// getImageDimensions decodes a JPEG/PNG to get its width and height.
func getImageDimensions(data []byte) (int, int, error) {
	img, _, err := decodeImage(data)
	if err != nil {
		return 0, 0, err
	}
	bounds := img.Bounds()
	return bounds.Dx(), bounds.Dy(), nil
}

// decodeImage tries to decode image data as JPEG, then PNG.
func decodeImage(data []byte) (image.Image, string, error) {
	// Try JPEG first
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err == nil {
		return img, "image/jpeg", nil
	}

	// Try PNG
	img, err = png.Decode(bytes.NewReader(data))
	if err == nil {
		return img, "image/png", nil
	}

	return nil, "", fmt.Errorf("failed to decode image as JPEG or PNG")
}

// writeTempJPEG writes image data to a temporary JPEG file.
func writeTempJPEG(data []byte, pattern string) (string, error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	path := f.Name()

	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return "", fmt.Errorf("failed to write image data: %w", err)
	}
	f.Close()

	return path, nil
}

// copyGroupFrames copies original frames to the enhanced directory
// (used as fallback when enhancement fails).
func copyGroupFrames(group filehandler.FrameGroup, outputDir string) error {
	for _, framePath := range group.FramePaths {
		data, err := os.ReadFile(framePath)
		if err != nil {
			return fmt.Errorf("failed to read frame %s: %w", framePath, err)
		}

		outputPath := filepath.Join(outputDir, filepath.Base(framePath))
		if err := os.WriteFile(outputPath, data, 0o644); err != nil {
			return fmt.Errorf("failed to write frame %s: %w", outputPath, err)
		}
	}
	return nil
}
