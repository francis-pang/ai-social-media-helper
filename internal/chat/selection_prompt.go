package chat

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpang/gemini-media-cli/internal/filehandler"
)

// BuildMediaSelectionPrompt creates a prompt for mixed media (photos + videos) selection
// using quality-agnostic, metadata-driven criteria. It includes metadata context,
// scene detection guidance, and user trip description for informed selection.
func BuildMediaSelectionPrompt(files []*filehandler.MediaFile, maxItems int, tripContext string) string {
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
	sb.WriteString(fmt.Sprintf("You are reviewing %d media items (%d photos, %d videos) for an Instagram carousel (max %d).\n\n",
		len(files), imageCount, videoCount, maxItems))

	// User context section
	sb.WriteString("### Trip/Event Context\n\n")
	if tripContext != "" {
		sb.WriteString(tripContext)
		sb.WriteString("\n\n")
	} else {
		sb.WriteString("No context provided. Infer the event type from media and metadata.\n\n")
	}

	sb.WriteString("### Selection Criteria\n\n")
	sb.WriteString("Remember: Quality is NOT a criterion unless the item is completely unusable.\n")
	sb.WriteString("Photos and videos compete EQUALLY - select the best media regardless of type.\n\n")
	sb.WriteString("Focus on:\n")
	sb.WriteString("- **Subject Diversity** (Highest): food, architecture, landscape, people, activities\n")
	sb.WriteString("- **Scene Representation**: ensure each sub-event/location is represented\n")
	sb.WriteString("- **Media Type Synergy**: consider which type best captures each moment\n")
	sb.WriteString("- **Audio Content** (Videos): consider music, speech, ambient sounds\n")
	sb.WriteString("- **Enhancement Potential**: when choosing between duplicates, pick easiest to enhance\n")
	sb.WriteString("- **People Variety** (Lower): different groups/individuals\n")
	sb.WriteString("- **Time of Day** (Tiebreaker): only use to break ties\n\n")

	sb.WriteString("### Scene Detection\n\n")
	sb.WriteString("Detect scenes using combined signals:\n")
	sb.WriteString("- **Visual similarity**: similar backgrounds, subjects, composition\n")
	sb.WriteString("- **Time gaps**: 2+ hour gaps suggest a new scene/sub-event\n")
	sb.WriteString("- **Location gaps**: 1km+ GPS distance suggests a new venue\n\n")

	sb.WriteString("### Media Metadata\n\n")
	sb.WriteString("Below is the metadata for each media item. Media files are provided in the same order.\n\n")

	writeMediaMetadata(&sb, files)

	sb.WriteString("### Required Output Format\n\n")
	sb.WriteString("You MUST provide all three sections:\n\n")

	sb.WriteString("#### 1. Ranked List\n")
	sb.WriteString("```\n")
	sb.WriteString("RANK | ITEM | TYPE | SCENE | JUSTIFICATION\n")
	sb.WriteString("-----|------|------|-------|---------------\n")
	sb.WriteString("1    | Media X: filename.jpg | Photo | Scene Name | Why this item was selected\n")
	sb.WriteString("2    | Media Y: filename.mp4 | Video | Scene Name | Why selected (audio: music playing)\n")
	sb.WriteString("... (continue for all selected items, up to 20)\n")
	sb.WriteString("```\n\n")

	sb.WriteString("#### 2. Scene Grouping\n")
	sb.WriteString("```\n")
	sb.WriteString("## Detected Scenes\n\n")
	sb.WriteString("### Scene 1: [Name] (GPS: [Venue/Coordinates], [Time Range])\n")
	sb.WriteString("Total items in scene: X\n")
	sb.WriteString("Selected: Y items\n")
	sb.WriteString("- filename.jpg [Photo]: Brief description (SELECTED)\n")
	sb.WriteString("- filename.mp4 [Video]: Brief description (SELECTED)\n")
	sb.WriteString("...\n")
	sb.WriteString("```\n\n")

	sb.WriteString("#### 3. Exclusion Report (MANDATORY - DO NOT SKIP)\n")
	sb.WriteString("You MUST list EVERY item that was not selected with a specific reason.\n\n")
	sb.WriteString("```\n")
	sb.WriteString("## Excluded Media\n\n")
	sb.WriteString("### Near-Duplicates (X items)\n")
	sb.WriteString("| Item | Duplicate Of | Reason Not Chosen |\n")
	sb.WriteString("|------|--------------|-------------------|\n")
	sb.WriteString("| Media 3: filename.jpg | Media 2: IMG_001.jpg | Same scene, worse expressions |\n")
	sb.WriteString("...\n\n")
	sb.WriteString("### Quality Issues (X items)\n")
	sb.WriteString("| Item | Issue | Enhancement Feasible? |\n")
	sb.WriteString("|------|-------|----------------------|\n")
	sb.WriteString("| Media 7: filename.jpg | Extremely blurry | No - subject unrecognizable |\n")
	sb.WriteString("...\n\n")
	sb.WriteString("### Content Mismatch (X items)\n")
	sb.WriteString("| Item | Issue |\n")
	sb.WriteString("|------|-------|\n")
	sb.WriteString("| Media 5: filename.mp4 | Content doesn't fit event theme |\n")
	sb.WriteString("...\n\n")
	sb.WriteString("### Redundant Scenes (X items)\n")
	sb.WriteString("| Item | Scene | Reason |\n")
	sb.WriteString("|------|-------|--------|\n")
	sb.WriteString("| Media 12: filename.jpg | Restaurant | Scene already well-represented |\n")
	sb.WriteString("...\n")
	sb.WriteString("```\n")

	return sb.String()
}

// formatVideoDuration formats a duration in a human-readable format for prompts.
func formatVideoDuration(d time.Duration) string {
	totalSeconds := int(d.Seconds())
	minutes := totalSeconds / 60
	seconds := totalSeconds % 60
	if minutes > 0 {
		return fmt.Sprintf("%d:%02d", minutes, seconds)
	}
	return fmt.Sprintf("0:%02d", seconds)
}

// BuildPhotoSelectionPrompt creates a prompt asking Gemini to rank and select photos
// using quality-agnostic, metadata-driven criteria.
// It includes metadata context and user trip description for informed selection.
func BuildPhotoSelectionPrompt(files []*filehandler.MediaFile, maxPhotos int, tripContext string) string {
	var sb strings.Builder

	sb.WriteString("## Photo Selection Task\n\n")
	sb.WriteString(fmt.Sprintf("You are reviewing %d photos from a directory for an Instagram carousel (max %d).\n\n", len(files), maxPhotos))

	// User context section
	sb.WriteString("### Trip/Event Context\n\n")
	if tripContext != "" {
		sb.WriteString(tripContext)
		sb.WriteString("\n\n")
	} else {
		sb.WriteString("No context provided. Infer the event type from photos and metadata.\n\n")
	}

	sb.WriteString("### Selection Criteria\n\n")
	sb.WriteString("Remember: Quality is NOT a criterion unless the photo is completely unusable.\n\n")
	sb.WriteString("Focus on:\n")
	sb.WriteString("- **Subject Diversity** (Highest): food, architecture, landscape, people, activities\n")
	sb.WriteString("- **Scene Representation**: ensure each sub-event/location is represented\n")
	sb.WriteString("- **Enhancement Potential**: when choosing between duplicates, pick easiest to enhance\n")
	sb.WriteString("- **People Variety** (Lower): different groups/individuals\n")
	sb.WriteString("- **Time of Day** (Tiebreaker): only use to break ties\n\n")

	sb.WriteString("### Scene Detection\n\n")
	sb.WriteString("Detect scenes using combined signals:\n")
	sb.WriteString("- **Visual similarity**: similar backgrounds, subjects, composition\n")
	sb.WriteString("- **Time gaps**: 2+ hour gaps suggest a new scene/sub-event\n")
	sb.WriteString("- **Location gaps**: 1km+ GPS distance suggests a new venue\n\n")

	sb.WriteString("### Photo Metadata\n\n")
	sb.WriteString("Below is the metadata for each photo. Thumbnails are provided in the same order.\n\n")

	for i, file := range files {
		sb.WriteString(fmt.Sprintf("**Photo %d: %s**\n", i+1, filepath.Base(file.Path)))

		if file.Metadata != nil {
			if file.Metadata.HasGPSData() {
				lat, lon := file.Metadata.GetGPS()
				sb.WriteString(fmt.Sprintf("- GPS: %.6f, %.6f\n", lat, lon))
			}
			if file.Metadata.HasDateData() {
				date := file.Metadata.GetDate()
				sb.WriteString(fmt.Sprintf("- Date: %s\n", date.Format("Monday, January 2, 2006 at 3:04 PM")))
			}
			// Add camera info for images
			if imgMeta, ok := file.Metadata.(*filehandler.ImageMetadata); ok {
				if imgMeta.CameraMake != "" || imgMeta.CameraModel != "" {
					sb.WriteString(fmt.Sprintf("- Camera: %s %s\n", imgMeta.CameraMake, imgMeta.CameraModel))
				}
			}
		} else {
			sb.WriteString("- No metadata available\n")
		}
		sb.WriteString("\n")
	}

	sb.WriteString("### Required Output Format\n\n")
	sb.WriteString("You MUST provide all three sections:\n\n")

	sb.WriteString("#### 1. Ranked List\n")
	sb.WriteString("```\n")
	sb.WriteString("RANK | PHOTO | SCENE | JUSTIFICATION\n")
	sb.WriteString("-----|-------|-------|---------------\n")
	sb.WriteString("1    | Photo X: filename.jpg | Scene Name | Why this photo was selected\n")
	sb.WriteString("2    | Photo Y: filename.heic | Scene Name | Why this photo was selected\n")
	sb.WriteString("... (continue for all selected photos, up to 20)\n")
	sb.WriteString("```\n\n")

	sb.WriteString("#### 2. Scene Grouping\n")
	sb.WriteString("```\n")
	sb.WriteString("## Detected Scenes\n\n")
	sb.WriteString("### Scene 1: [Name] (GPS: [Venue/Coordinates], [Time Range])\n")
	sb.WriteString("Total photos in scene: X\n")
	sb.WriteString("Selected: Y photos\n")
	sb.WriteString("- filename.jpg: Brief description (SELECTED)\n")
	sb.WriteString("- filename2.jpg: Brief description (SELECTED)\n")
	sb.WriteString("...\n")
	sb.WriteString("```\n\n")

	sb.WriteString("#### 3. Exclusion Report (MANDATORY - DO NOT SKIP)\n")
	sb.WriteString("You MUST list EVERY photo that was not selected with a specific reason.\n\n")
	sb.WriteString("```\n")
	sb.WriteString("## Excluded Photos\n\n")
	sb.WriteString("### Near-Duplicates (X photos)\n")
	sb.WriteString("| Photo | Duplicate Of | Reason Not Chosen |\n")
	sb.WriteString("|-------|--------------|-------------------|\n")
	sb.WriteString("| Photo 3: filename.jpg | Photo 2: IMG_001.jpg | Same scene, worse expressions |\n")
	sb.WriteString("...\n\n")
	sb.WriteString("### Quality Issues (X photos)\n")
	sb.WriteString("| Photo | Issue | Enhancement Feasible? |\n")
	sb.WriteString("|-------|-------|----------------------|\n")
	sb.WriteString("| Photo 7: filename.jpg | Extremely blurry | No - subject unrecognizable |\n")
	sb.WriteString("...\n\n")
	sb.WriteString("### Content Mismatch (X photos)\n")
	sb.WriteString("| Photo | Issue |\n")
	sb.WriteString("|-------|-------|\n")
	sb.WriteString("| Photo 5: filename.jpg | Content doesn't fit event theme |\n")
	sb.WriteString("...\n\n")
	sb.WriteString("### Redundant Scenes (X photos)\n")
	sb.WriteString("| Photo | Scene | Reason |\n")
	sb.WriteString("|-------|-------|--------|\n")
	sb.WriteString("| Photo 12: filename.jpg | Restaurant | Scene already well-represented |\n")
	sb.WriteString("...\n")
	sb.WriteString("```\n")

	return sb.String()
}

// writeMediaMetadata writes per-item metadata to the string builder.
// Shared by BuildMediaSelectionPrompt and BuildMediaSelectionJSONPrompt.
func writeMediaMetadata(sb *strings.Builder, files []*filehandler.MediaFile) {
	for i, file := range files {
		ext := strings.ToLower(filepath.Ext(file.Path))
		mediaType := "Photo"
		if filehandler.IsVideo(ext) {
			mediaType = "Video"
		}

		sb.WriteString(fmt.Sprintf("**Media %d: %s** [%s]\n", i+1, filepath.Base(file.Path), mediaType))

		if file.Metadata != nil {
			if file.Metadata.HasGPSData() {
				lat, lon := file.Metadata.GetGPS()
				sb.WriteString(fmt.Sprintf("- GPS: %.6f, %.6f\n", lat, lon))
			}
			if file.Metadata.HasDateData() {
				date := file.Metadata.GetDate()
				sb.WriteString(fmt.Sprintf("- Date: %s\n", date.Format("Monday, January 2, 2006 at 3:04 PM")))
			}

			switch m := file.Metadata.(type) {
			case *filehandler.ImageMetadata:
				if m.CameraMake != "" || m.CameraModel != "" {
					sb.WriteString(fmt.Sprintf("- Camera: %s %s\n", m.CameraMake, m.CameraModel))
				}
			case *filehandler.VideoMetadata:
				if m.Duration > 0 {
					sb.WriteString(fmt.Sprintf("- Duration: %s\n", formatVideoDuration(m.Duration)))
				}
				if m.Width > 0 && m.Height > 0 {
					sb.WriteString(fmt.Sprintf("- Resolution: %dx%d\n", m.Width, m.Height))
				}
				hasAudio := m.AudioCodec != ""
				sb.WriteString(fmt.Sprintf("- Has Audio: %v\n", hasAudio))
				if hasAudio {
					sb.WriteString("- Audio Note: Analyze audio for music, speech, ambient sounds\n")
				}
			}
		} else {
			sb.WriteString("- No metadata available\n")
		}
		sb.WriteString("\n")
	}
}
