// Package assets provides embedded static assets for the application.
//
// Prompt templates are stored as text files under prompts/ and embedded at compile time.
// See DDR-019: Externalized Prompt Templates.

package assets

import (
	"bytes"
	_ "embed"
	"text/template"
)

// --- Static prompts (no dynamic data) ---

// SystemInstructionPrompt provides context for media analysis with extracted metadata.
// See DDR-017: Francis Reference Photo for Person Identification.
//
//go:embed prompts/system-instruction.txt
var SystemInstructionPrompt string

// SelectionSystemPrompt provides context for quality-agnostic photo selection tasks.
// See DDR-016: Quality-Agnostic Metadata-Driven Photo Selection.
// See DDR-017: Francis Reference Photo for Person Identification.
//
//go:embed prompts/selection-system.txt
var SelectionSystemPrompt string

// TriageSystemPrompt provides context for batch media triage evaluation.
// See DDR-021: Media Triage Command with Batch AI Evaluation.
//
//go:embed prompts/triage-system.txt
var TriageSystemPrompt string

// EnhancementSystemPrompt provides instructions for AI photo enhancement.
// See DDR-031: Multi-Step Photo Enhancement Pipeline.
//
//go:embed prompts/enhancement-system.txt
var EnhancementSystemPrompt string

// EnhancementAnalysisPrompt provides instructions for analyzing an enhanced photo
// to determine what further improvements are needed.
// See DDR-031: Multi-Step Photo Enhancement Pipeline.
//
//go:embed prompts/enhancement-analysis.txt
var EnhancementAnalysisPrompt string

// VideoEnhancementSystemPrompt provides instructions for AI video frame enhancement.
// See DDR-032: Multi-Step Frame-Based Video Enhancement Pipeline.
//
//go:embed prompts/video-enhancement-system.txt
var VideoEnhancementSystemPrompt string

// VideoEnhancementAnalysisPrompt provides instructions for analyzing an enhanced
// video frame to determine what further improvements are needed.
// See DDR-032: Multi-Step Frame-Based Video Enhancement Pipeline.
//
//go:embed prompts/video-enhancement-analysis.txt
var VideoEnhancementAnalysisPrompt string

// DescriptionSystemPrompt provides instructions for AI-generated Instagram carousel captions.
// See DDR-036: AI Post Description Generation with Full Media Context.
//
//go:embed prompts/description-system.txt
var DescriptionSystemPrompt string

// --- Dynamic prompt templates (require metadata context) ---

//go:embed prompts/social-media-image.txt
var socialMediaImageTemplate string

//go:embed prompts/social-media-video.txt
var socialMediaVideoTemplate string

//go:embed prompts/social-media-generic.txt
var socialMediaGenericTemplate string

// Pre-parsed templates for efficiency. template.Must panics on malformed templates,
// catching errors at program startup rather than at call time.
var (
	imagePromptTmpl   = template.Must(template.New("image").Parse(socialMediaImageTemplate))
	videoPromptTmpl   = template.Must(template.New("video").Parse(socialMediaVideoTemplate))
	genericPromptTmpl = template.Must(template.New("generic").Parse(socialMediaGenericTemplate))
)

// PromptData holds the dynamic data injected into prompt templates.
type PromptData struct {
	// MetadataContext is the formatted EXIF/FFmpeg metadata string.
	// Empty string if no metadata is available.
	MetadataContext string
}

// RenderSocialMediaImagePrompt renders the image analysis prompt template
// with the provided metadata context.
func RenderSocialMediaImagePrompt(metadataContext string) string {
	return renderTemplate(imagePromptTmpl, metadataContext)
}

// RenderSocialMediaVideoPrompt renders the video analysis prompt template
// with the provided metadata context.
func RenderSocialMediaVideoPrompt(metadataContext string) string {
	return renderTemplate(videoPromptTmpl, metadataContext)
}

// RenderSocialMediaGenericPrompt renders the generic media analysis prompt template
// with the provided metadata context.
func RenderSocialMediaGenericPrompt(metadataContext string) string {
	return renderTemplate(genericPromptTmpl, metadataContext)
}

// renderTemplate executes a pre-parsed template with the given metadata context.
func renderTemplate(tmpl *template.Template, metadataContext string) string {
	var buf bytes.Buffer
	// Template execution errors are not expected with our simple templates,
	// but we handle them gracefully by returning whatever was rendered.
	_ = tmpl.Execute(&buf, PromptData{MetadataContext: metadataContext})
	return buf.String()
}
