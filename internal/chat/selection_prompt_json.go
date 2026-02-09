package chat

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/fpang/gemini-media-cli/internal/filehandler"
)

// BuildMediaSelectionJSONPrompt creates a prompt for structured JSON media selection.
// Unlike BuildMediaSelectionPrompt, this produces a prompt for the JSON output mode
// without an item limit — the AI selects all worthy items. See DDR-030.
func BuildMediaSelectionJSONPrompt(files []*filehandler.MediaFile, tripContext string) string {
	var sb strings.Builder

	// Count media types
	var imageCount, videoCount int
	for _, file := range files {
		ext := strings.ToLower(filepath.Ext(file.Path))
		if filehandler.IsImage(ext) {
			imageCount++
		} else if filehandler.IsVideo(ext) {
			videoCount++
		}
	}

	sb.WriteString("## Media Selection Task\n\n")
	sb.WriteString(fmt.Sprintf("You are reviewing %d media items (%d photos, %d videos). Select ALL items worthy of posting — there is no maximum limit.\n\n",
		len(files), imageCount, videoCount))

	// User context section
	sb.WriteString("### Trip/Event Context\n\n")
	if tripContext != "" {
		sb.WriteString(tripContext)
		sb.WriteString("\n\n")
	} else {
		sb.WriteString("No context provided. Infer the event type from media and metadata.\n\n")
	}

	sb.WriteString("### Media Metadata\n\n")
	sb.WriteString("Below is the metadata for each media item. Media files are provided in the same order.\n\n")

	writeMediaMetadata(&sb, files)

	sb.WriteString("### Output\n\n")
	sb.WriteString("Respond with ONLY the JSON object as specified in the system instruction. No other text.\n")

	return sb.String()
}
