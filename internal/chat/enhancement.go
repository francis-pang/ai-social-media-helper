package chat

// enhancement.go implements the multi-step photo enhancement pipeline.
// See DDR-031: Multi-Step Photo Enhancement Pipeline.
//
// The pipeline has three phases per photo:
//   Phase 1: Gemini 3 Pro Image — global creative enhancement
//   Phase 2: Gemini 3 Pro (text) — professional quality analysis
//   Phase 3: Imagen 3 — localized surgical edits (if applicable)
//
// Feedback sessions: Gemini 3 Pro Image first, fall back to Imagen 3.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fpang/gemini-media-cli/internal/assets"
	"github.com/rs/zerolog/log"
)

// Enhancement phase constants.
const (
	PhaseInitial  = "initial"  // Not yet started
	PhaseOne      = "phase1"   // Gemini 3 Pro Image: global enhancement
	PhaseTwo      = "phase2"   // Gemini 3 Pro: analysis
	PhaseThree    = "phase3"   // Imagen 3: surgical edits
	PhaseFeedback = "feedback" // User feedback loop
	PhaseComplete = "complete" // Enhancement finished
	PhaseError    = "error"    // Enhancement failed
)

// MaxImagenIterations is the maximum number of Imagen 3 iterations per photo.
const MaxImagenIterations = 3

// ProfessionalScoreThreshold is the score above which no further edits are needed.
const ProfessionalScoreThreshold = 8.5

// --- Analysis types (Phase 2 output) ---

// AnalysisResult is the structured output from Phase 2 quality analysis.
type AnalysisResult struct {
	OverallAssessment     string            `json:"overallAssessment"`
	RemainingImprovements []ImprovementItem `json:"remainingImprovements"`
	ProfessionalScore     float64           `json:"professionalScore"`
	TargetScore           float64           `json:"targetScore"`
	NoFurtherEditsNeeded  bool              `json:"noFurtherEditsNeeded"`
}

// ImprovementItem describes a single remaining enhancement opportunity.
type ImprovementItem struct {
	Type            string `json:"type"` // "object-removal", "background-cleanup", "color-grading", etc.
	Description     string `json:"description"`
	Region          string `json:"region"`          // "top-left", "center", "background", "global", etc.
	Impact          string `json:"impact"`          // "high", "medium", "low"
	ImagenSuitable  bool   `json:"imagenSuitable"`  // true if needs mask-based localized edit
	EditInstruction string `json:"editInstruction"` // specific instruction for the editing model
}

// --- Enhancement state (per photo item) ---

// EnhancementState tracks the full state of enhancement for one photo.
type EnhancementState struct {
	Phase           string          `json:"phase"`
	OriginalData    []byte          `json:"-"` // not serialized (in S3)
	CurrentData     []byte          `json:"-"` // current best version (in S3)
	CurrentMIME     string          `json:"currentMIME"`
	Phase1Text      string          `json:"phase1Text"`      // Gemini's description of Phase 1 changes
	Analysis        *AnalysisResult `json:"analysis"`        // Phase 2 analysis
	ImagenEdits     int             `json:"imagenEdits"`     // Number of Imagen iterations done
	FeedbackHistory []FeedbackEntry `json:"feedbackHistory"` // Multi-turn feedback
	Error           string          `json:"error,omitempty"`
}

// FeedbackEntry records one round of feedback and its result.
type FeedbackEntry struct {
	UserFeedback  string `json:"userFeedback"`
	ModelResponse string `json:"modelResponse"`
	Method        string `json:"method"` // "gemini" or "imagen"
	Success       bool   `json:"success"`
}

// --- Phase 1: Gemini 3 Pro Image Enhancement ---

// RunPhaseOne performs the initial global enhancement using Gemini 3 Pro Image.
// Returns the enhanced image data and a text description of changes.
func RunPhaseOne(ctx context.Context, geminiClient *GeminiImageClient, imageData []byte, imageMIME string) ([]byte, string, string, error) {
	log.Info().
		Int("image_bytes", len(imageData)).
		Str("mime", imageMIME).
		Msg("Phase 1: Starting Gemini 3 Pro Image global enhancement")

	instruction := `Enhance this photo to professional quality for Instagram posting.

Apply all necessary improvements:
- Fix exposure, lighting, and white balance
- Correct color balance and boost vibrancy naturally
- Improve contrast and clarity
- Reduce noise while preserving detail
- Sharpen key subjects
- For portraits: enhance skin naturally, brighten eyes
- For landscapes: enhance sky and natural colors
- For food: boost warmth and make colors appetizing

Make it look like a professionally shot and edited photo.
Describe what changes you made.`

	result, err := geminiClient.EditImage(ctx, imageData, imageMIME, instruction, assets.EnhancementSystemPrompt)
	if err != nil {
		return nil, "", "", fmt.Errorf("phase 1 failed: %w", err)
	}

	log.Info().
		Int("enhanced_bytes", len(result.ImageData)).
		Str("changes", truncateString(result.Text, 200)).
		Msg("Phase 1 complete: global enhancement applied")

	return result.ImageData, result.ImageMIMEType, result.Text, nil
}

// --- Phase 2: Professional Quality Analysis ---

// RunPhaseTwo analyzes the enhanced image and returns structured recommendations.
func RunPhaseTwo(ctx context.Context, geminiClient *GeminiImageClient, imageData []byte, imageMIME string) (*AnalysisResult, error) {
	log.Info().
		Int("image_bytes", len(imageData)).
		Msg("Phase 2: Analyzing enhanced image for remaining improvements")

	analysisPrompt := "Analyze this photo that has been enhanced once. Identify what further improvements would bring it to professional publication quality. Follow the response format in the system instruction exactly."

	responseText, err := geminiClient.AnalyzeImage(ctx, imageData, imageMIME, analysisPrompt, assets.EnhancementAnalysisPrompt)
	if err != nil {
		return nil, fmt.Errorf("phase 2 analysis failed: %w", err)
	}

	// Parse the JSON response
	analysis, err := parseAnalysisResponse(responseText)
	if err != nil {
		log.Warn().
			Err(err).
			Str("response", truncateString(responseText, 500)).
			Msg("Failed to parse analysis response, treating as no improvements needed")
		// Return a default "no improvements" result rather than failing
		return &AnalysisResult{
			OverallAssessment:    "Analysis parsing failed — photo appears ready for publication",
			ProfessionalScore:    8.5,
			TargetScore:          9.0,
			NoFurtherEditsNeeded: true,
		}, nil
	}

	log.Info().
		Float64("score", analysis.ProfessionalScore).
		Int("improvements", len(analysis.RemainingImprovements)).
		Bool("edits_needed", !analysis.NoFurtherEditsNeeded).
		Msg("Phase 2 complete: analysis results")

	return analysis, nil
}

// parseAnalysisResponse extracts and parses the JSON from Gemini's analysis response.
func parseAnalysisResponse(response string) (*AnalysisResult, error) {
	text := strings.TrimSpace(response)

	// Strip markdown code fences if present
	if strings.HasPrefix(text, "```") {
		lines := strings.Split(text, "\n")
		if len(lines) >= 3 {
			startIdx := 1
			endIdx := len(lines) - 1
			for i := len(lines) - 1; i >= 0; i-- {
				if strings.TrimSpace(lines[i]) == "```" {
					endIdx = i
					break
				}
			}
			text = strings.Join(lines[startIdx:endIdx], "\n")
		}
	}

	text = strings.TrimSpace(text)

	// Find JSON object
	if !strings.HasPrefix(text, "{") {
		startIdx := strings.Index(text, "{")
		if startIdx == -1 {
			return nil, fmt.Errorf("no JSON object found in analysis response")
		}
		text = text[startIdx:]
	}

	if endIdx := strings.LastIndex(text, "}"); endIdx != -1 {
		text = text[:endIdx+1]
	}

	var result AnalysisResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return nil, fmt.Errorf("invalid JSON in analysis response: %w", err)
	}

	return &result, nil
}

// --- Phase 3: Imagen 3 Surgical Edits ---

// RunPhaseThree applies Imagen 3 mask-based edits for localized improvements.
// It iterates through improvements marked as imagenSuitable and applies each one.
// imageWidth and imageHeight are the dimensions of the image (for mask generation).
func RunPhaseThree(ctx context.Context, imagenClient *ImagenClient, imageData []byte, analysis *AnalysisResult, imageWidth, imageHeight int) ([]byte, int, error) {
	if imagenClient == nil || !imagenClient.IsConfigured() {
		log.Warn().Msg("Phase 3: Imagen client not configured, skipping surgical edits")
		return imageData, 0, nil
	}

	// Collect Imagen-suitable improvements
	var imagenEdits []ImprovementItem
	for _, imp := range analysis.RemainingImprovements {
		if imp.ImagenSuitable && (imp.Impact == "high" || imp.Impact == "medium") {
			imagenEdits = append(imagenEdits, imp)
		}
	}

	if len(imagenEdits) == 0 {
		log.Info().Msg("Phase 3: No Imagen-suitable edits needed")
		return imageData, 0, nil
	}

	log.Info().
		Int("edits_count", len(imagenEdits)).
		Msg("Phase 3: Starting Imagen 3 surgical edits")

	currentImage := imageData
	editsApplied := 0

	for i, edit := range imagenEdits {
		if i >= MaxImagenIterations {
			log.Warn().
				Int("max", MaxImagenIterations).
				Int("remaining", len(imagenEdits)-i).
				Msg("Phase 3: Max Imagen iterations reached, stopping")
			break
		}

		log.Info().
			Int("iteration", i+1).
			Str("type", edit.Type).
			Str("region", edit.Region).
			Str("description", edit.Description).
			Msg("Phase 3: Applying Imagen edit")

		// Generate mask for the target region
		maskData, err := GenerateRegionMask(imageWidth, imageHeight, edit.Region)
		if err != nil {
			log.Warn().Err(err).Str("region", edit.Region).Msg("Failed to generate mask, skipping edit")
			continue
		}

		// Determine edit mode
		editMode := "inpainting-remove"
		if edit.Type == "background-cleanup" || edit.Type == "composition-fix" {
			editMode = "inpainting-insert"
		}

		result, err := imagenClient.EditWithMask(ctx, currentImage, maskData, edit.EditInstruction, editMode)
		if err != nil {
			log.Warn().Err(err).Str("type", edit.Type).Msg("Imagen edit failed, continuing with other edits")
			continue
		}

		currentImage = result.ImageData
		editsApplied++
		log.Info().
			Int("iteration", i+1).
			Int("result_bytes", len(result.ImageData)).
			Msg("Phase 3: Imagen edit applied successfully")
	}

	log.Info().
		Int("edits_applied", editsApplied).
		Int("edits_total", len(imagenEdits)).
		Msg("Phase 3 complete")

	return currentImage, editsApplied, nil
}

// --- Full Enhancement Pipeline ---

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

// --- Feedback Processing ---

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
