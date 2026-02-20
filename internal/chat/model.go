package chat

import "os"

// Gemini Model IDs
//
// | Model Name                  | API Model ID                | Use Case                      |
// |-----------------------------|---------------------------  |-------------------------------|
// | Gemini 3.1 Pro (Preview)    | gemini-3.1-pro-preview      | Best for complex reasoning    |
// | Gemini 3 Flash (Preview)    | gemini-3-flash-preview      | Best for speed + intelligence |
// | Gemini 2.5 Pro              | gemini-2.5-pro              | Stable, high-reasoning tasks  |
// | Gemini 2.5 Flash            | gemini-2.5-flash            | Stable, balanced performance  |
// | Gemini 2.5 Flash-Lite       | gemini-2.5-flash-lite       | High-throughput, lowest cost  |
// | Gemini 3 Pro Image          | gemini-3-pro-image-preview  | Advanced image generation     |
const (
	// ModelGemini31ProPreview is best for complex reasoning/coding (1M context).
	ModelGemini31ProPreview = "gemini-3.1-pro-preview"

	// ModelGemini3FlashPreview is best for speed + intelligence.
	ModelGemini3FlashPreview = "gemini-3-flash-preview"

	// ModelGemini25Pro is stable, for high-reasoning tasks.
	ModelGemini25Pro = "gemini-2.5-pro"

	// ModelGemini25Flash is stable, balanced performance.
	ModelGemini25Flash = "gemini-2.5-flash"

	// ModelGemini25FlashLite is for high-throughput, lowest cost.
	ModelGemini25FlashLite = "gemini-2.5-flash-lite"

	// ModelGemini3ProImage is for advanced image generation/edit.
	ModelGemini3ProImage = "gemini-3-pro-image-preview"
)

// DefaultModelName is the default Gemini model to use.
// Can be overridden via GEMINI_MODEL environment variable.
const DefaultModelName = ModelGemini3FlashPreview

// GetModelName returns the Gemini model to use, resolved from:
// 1. GEMINI_MODEL environment variable (if set)
// 2. Default: gemini-3-flash-preview (best for speed + intelligence)
//
// Available models:
//   - "gemini-3.1-pro-preview"     - Best for complex reasoning/coding (1M context)
//   - "gemini-3-flash-preview"     - Best for speed + intelligence (default)
//   - "gemini-2.5-pro"             - Stable, high-reasoning tasks
//   - "gemini-2.5-flash"           - Stable, balanced performance
//   - "gemini-2.5-flash-lite"      - High-throughput, lowest cost
//   - "gemini-3-pro-image-preview" - Advanced image generation/edit
func GetModelName() string {
	if env := os.Getenv("GEMINI_MODEL"); env != "" {
		return env
	}
	return DefaultModelName
}
