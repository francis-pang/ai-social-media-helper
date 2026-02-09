package chat

// enhancement_pipeline.go contains the full enhancement pipeline orchestrator
// and the feedback processing loop.
// See DDR-031: Multi-Step Photo Enhancement Pipeline.

import (
	"context"
	"fmt"

	"github.com/fpang/gemini-media-cli/internal/assets"
	"github.com/rs/zerolog/log"
)

// RunFullEnhancement executes the complete three-phase enhancement pipeline for one photo.
// Returns the final enhanced image data, MIME type, and the enhancement state.
func RunFullEnhancement(ctx context.Context, geminiClient *GeminiImageClient, imagenClient *ImagenClient, imageData []byte, imageMIME string, imageWidth, imageHeight int) (*EnhancementState, error) {
	state := &EnhancementState{
		Phase:       PhaseOne,
		CurrentMIME: imageMIME,
	}

	// Phase 1: Gemini 3 Pro Image global enhancement
	enhancedData, enhancedMIME, changeText, err := RunPhaseOne(ctx, geminiClient, imageData, imageMIME)
	if err != nil {
		state.Phase = PhaseError
		state.Error = fmt.Sprintf("Phase 1 error: %v", err)
		return state, err
	}
	state.CurrentData = enhancedData
	state.CurrentMIME = enhancedMIME
	state.Phase1Text = changeText

	// Phase 2: Analysis
	state.Phase = PhaseTwo
	analysis, err := RunPhaseTwo(ctx, geminiClient, enhancedData, enhancedMIME)
	if err != nil {
		state.Phase = PhaseError
		state.Error = fmt.Sprintf("Phase 2 error: %v", err)
		return state, err
	}
	state.Analysis = analysis

	// Check if no further edits needed
	if analysis.NoFurtherEditsNeeded || analysis.ProfessionalScore >= ProfessionalScoreThreshold {
		log.Info().
			Float64("score", analysis.ProfessionalScore).
			Msg("Photo already at professional quality after Phase 1, skipping Phase 3")
		state.Phase = PhaseComplete
		return state, nil
	}

	// Collect non-Imagen improvements for a second Gemini pass
	var geminiImprovements []string
	for _, imp := range analysis.RemainingImprovements {
		if !imp.ImagenSuitable && (imp.Impact == "high" || imp.Impact == "medium") {
			geminiImprovements = append(geminiImprovements, imp.EditInstruction)
		}
	}

	// Second Gemini pass for remaining global improvements
	if len(geminiImprovements) > 0 {
		instruction := "Apply these additional improvements to the photo:\n"
		for i, imp := range geminiImprovements {
			instruction += fmt.Sprintf("%d. %s\n", i+1, imp)
		}
		instruction += "\nMake these specific changes while preserving the improvements already applied."

		result, err := geminiClient.EditImage(ctx, enhancedData, enhancedMIME, instruction, assets.EnhancementSystemPrompt)
		if err != nil {
			log.Warn().Err(err).Msg("Second Gemini pass failed, continuing with Phase 1 result")
		} else {
			state.CurrentData = result.ImageData
			state.CurrentMIME = result.ImageMIMEType
			log.Info().Msg("Second Gemini pass applied successfully")
		}
	}

	// Phase 3: Imagen 3 surgical edits
	state.Phase = PhaseThree
	finalData, editsApplied, err := RunPhaseThree(ctx, imagenClient, state.CurrentData, analysis, imageWidth, imageHeight)
	if err != nil {
		log.Warn().Err(err).Msg("Phase 3 failed, using Phase 1/2 result")
	} else {
		state.CurrentData = finalData
		state.ImagenEdits = editsApplied
	}

	state.Phase = PhaseComplete
	return state, nil
}

// ProcessFeedback handles user feedback by first trying Gemini 3 Pro Image,
// then falling back to Imagen 3 if needed. Returns updated image and state.
func ProcessFeedback(ctx context.Context, geminiClient *GeminiImageClient, imagenClient *ImagenClient, imageData []byte, imageMIME string, feedback string, history []FeedbackEntry, imageWidth, imageHeight int) ([]byte, string, *FeedbackEntry, error) {
	log.Info().
		Str("feedback", truncateString(feedback, 200)).
		Int("history_len", len(history)).
		Msg("Processing enhancement feedback")

	entry := &FeedbackEntry{
		UserFeedback: feedback,
	}

	// Step 1: Try Gemini 3 Pro Image first
	log.Info().Msg("Feedback: Attempting Gemini 3 Pro Image edit")

	// Build conversation history for multi-turn context
	var convHistory []ConversationTurn
	for _, h := range history {
		convHistory = append(convHistory, ConversationTurn{
			Role: "user",
			Text: h.UserFeedback,
		})
		convHistory = append(convHistory, ConversationTurn{
			Role: "model",
			Text: h.ModelResponse,
		})
	}

	result, err := geminiClient.EditImageMultiTurn(
		ctx, imageData, imageMIME,
		feedback, assets.EnhancementSystemPrompt,
		convHistory,
	)

	if err == nil && result.ImageData != nil {
		entry.Method = "gemini"
		entry.ModelResponse = result.Text
		entry.Success = true
		log.Info().
			Int("result_bytes", len(result.ImageData)).
			Msg("Feedback: Gemini 3 Pro Image edit successful")
		return result.ImageData, result.ImageMIMEType, entry, nil
	}

	if err != nil {
		log.Warn().Err(err).Msg("Feedback: Gemini 3 Pro Image failed, analyzing for Imagen fallback")
	}

	// Step 2: Analyze what Gemini couldn't do and try Imagen 3
	if imagenClient != nil && imagenClient.IsConfigured() {
		log.Info().Msg("Feedback: Falling back to Imagen 3 for surgical edit")

		// Ask Gemini to analyze what specific localized edit is needed
		analysisPrompt := fmt.Sprintf(`The user requested: "%s"
This could not be fully accomplished with global image editing.
Analyze the image and determine the specific region and edit type needed.
Respond with ONLY JSON matching the analysis schema in your system instruction.`, feedback)

		analysisText, err := geminiClient.AnalyzeImage(ctx, imageData, imageMIME, analysisPrompt, assets.EnhancementAnalysisPrompt)
		if err != nil {
			entry.Method = "gemini"
			entry.ModelResponse = fmt.Sprintf("Analysis failed: %v", err)
			entry.Success = false
			return imageData, imageMIME, entry, fmt.Errorf("feedback analysis failed: %w", err)
		}

		analysis, err := parseAnalysisResponse(analysisText)
		if err != nil {
			entry.Method = "gemini"
			entry.ModelResponse = "Could not determine specific edits needed"
			entry.Success = false
			return imageData, imageMIME, entry, fmt.Errorf("feedback analysis parse failed: %w", err)
		}

		// Apply Imagen edits for suitable improvements
		finalData, editsApplied, err := RunPhaseThree(ctx, imagenClient, imageData, analysis, imageWidth, imageHeight)
		if err != nil {
			entry.Method = "imagen"
			entry.ModelResponse = fmt.Sprintf("Imagen edit failed: %v", err)
			entry.Success = false
			return imageData, imageMIME, entry, err
		}

		if editsApplied > 0 {
			entry.Method = "imagen"
			entry.ModelResponse = fmt.Sprintf("Applied %d surgical edit(s) via Imagen 3", editsApplied)
			entry.Success = true
			log.Info().Int("edits", editsApplied).Msg("Feedback: Imagen 3 edits applied")
			return finalData, imageMIME, entry, nil
		}
	}

	// Neither method could address the feedback
	entry.Method = "gemini"
	entry.ModelResponse = "Unable to apply the requested change"
	entry.Success = false
	return imageData, imageMIME, entry, fmt.Errorf("unable to apply feedback: %s", feedback)
}
