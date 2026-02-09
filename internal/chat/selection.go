package chat

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/fpang/gemini-media-cli/internal/assets"
	"github.com/fpang/gemini-media-cli/internal/jsonutil"
	"github.com/fpang/gemini-media-cli/internal/metrics"
	"github.com/rs/zerolog/log"
	"google.golang.org/genai"
)

// DefaultMaxPhotos is the default maximum number of photos to select.
const DefaultMaxPhotos = 20

// DefaultMaxMedia is the default maximum number of media items to select.
const DefaultMaxMedia = 20

// --- Structured JSON Selection Types (DDR-030) ---

// SelectionResult is the structured AI selection output for programmatic parsing.
// See DDR-030: Cloud Selection Backend Architecture.
type SelectionResult struct {
	Selected    []SelectedItem `json:"selected"`
	Excluded    []ExcludedItem `json:"excluded"`
	SceneGroups []SceneGroup   `json:"sceneGroups"`
}

// SelectedItem represents a media item chosen by the AI.
type SelectedItem struct {
	Rank           int    `json:"rank"`
	Media          int    `json:"media"` // 1-indexed media number
	Filename       string `json:"filename"`
	Type           string `json:"type"` // "Photo" or "Video"
	Scene          string `json:"scene"`
	Justification  string `json:"justification"`
	ComparisonNote string `json:"comparisonNote,omitempty"`
}

// ExcludedItem represents a media item not chosen by the AI, with a reason.
type ExcludedItem struct {
	Media       int    `json:"media"`
	Filename    string `json:"filename"`
	Reason      string `json:"reason"`
	Category    string `json:"category"` // "near-duplicate", "quality-issue", "content-mismatch", "redundant-scene"
	DuplicateOf string `json:"duplicateOf,omitempty"`
}

// SceneGroup is a group of media items detected as belonging to the same scene.
type SceneGroup struct {
	Name      string           `json:"name"`
	GPS       string           `json:"gps,omitempty"`
	TimeRange string           `json:"timeRange,omitempty"`
	Items     []SceneGroupItem `json:"items"`
}

// SceneGroupItem is a media item within a scene group.
type SceneGroupItem struct {
	Media       int    `json:"media"`
	Filename    string `json:"filename"`
	Type        string `json:"type"`
	Selected    bool   `json:"selected"`
	Description string `json:"description"`
}

// SelectionSystemInstruction provides context for quality-agnostic photo selection tasks.
// Loaded from embedded prompt file. See DDR-019: Externalized Prompt Templates.
// See DDR-016: Quality-Agnostic Metadata-Driven Photo Selection.
// See DDR-017: Francis Reference Photo for Person Identification.
var SelectionSystemInstruction = assets.SelectionSystemPrompt

// parseSelectionResponse extracts and parses the JSON object from Gemini's response.
func parseSelectionResponse(response string) (*SelectionResult, error) {
	log.Debug().
		Int("response_length", len(response)).
		Msg("Parsing selection response JSON")
	result, err := jsonutil.ParseJSON[SelectionResult](response)
	if err != nil {
		log.Error().Err(err).Str("response", response).Msg("Failed to parse selection response")
		return nil, fmt.Errorf("selection response: %w", err)
	}
	if len(result.Selected) == 0 && len(result.Excluded) == 0 {
		return nil, fmt.Errorf("empty selection results (no items selected or excluded)")
	}
	log.Debug().
		Int("selected_count", len(result.Selected)).
		Int("excluded_count", len(result.Excluded)).
		Int("scene_group_count", len(result.SceneGroups)).
		Msg("Selection response parsed successfully")
	return &result, nil
}

// uploadVideoFile uploads a video file to Gemini Files API and waits for processing.
func uploadVideoFile(ctx context.Context, client *genai.Client, filePath string) (*genai.File, error) {
	// Open the file for streaming upload
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	// Get file info for logging
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	log.Debug().
		Str("path", filePath).
		Int64("size_bytes", info.Size()).
		Str("mime_type", "video/webm").
		Msg("Starting Gemini Files API upload for video")

	// Upload the file
	uploadStart := time.Now()
	file, err := client.Files.Upload(ctx, f, &genai.UploadFileConfig{
		MIMEType: "video/webm", // Compressed output is always WebM
	})
	if err != nil {
		return nil, fmt.Errorf("failed to upload file: %w", err)
	}

	log.Debug().
		Str("name", file.Name).
		Str("uri", file.URI).
		Dur("upload_duration", time.Since(uploadStart)).
		Msg("Video uploaded, waiting for processing...")

	// Wait for file to be processed
	const uploadPollingInterval = 5 * time.Second
	const uploadTimeout = 10 * time.Minute
	deadline := time.Now().Add(uploadTimeout)
	pollIteration := 0

	for file.State == genai.FileStateProcessing {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout waiting for video processing after %v", uploadTimeout)
		}

		pollIteration++
		log.Debug().
			Str("state", string(file.State)).
			Int("poll_iteration", pollIteration).
			Msg("Video still processing, waiting...")

		time.Sleep(uploadPollingInterval)

		// Get updated file state
		file, err = client.Files.Get(ctx, file.Name, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to get file state: %w", err)
		}
	}

	if file.State == genai.FileStateFailed {
		return nil, fmt.Errorf("video processing failed")
	}

	totalUploadTime := time.Since(uploadStart)
	log.Info().
		Str("name", file.Name).
		Str("state", string(file.State)).
		Dur("total_time", totalUploadTime).
		Int("poll_iterations", pollIteration).
		Msg("Video ready for inference")

	// Emit Gemini Files API upload metrics
	metrics.New("AiSocialMedia").
		Dimension("Operation", "filesApiUpload").
		Metric("GeminiFilesApiUploadMs", float64(totalUploadTime.Milliseconds()), metrics.UnitMilliseconds).
		Metric("GeminiFilesApiUploadBytes", float64(info.Size()), metrics.UnitBytes).
		Count("GeminiApiCalls").
		Flush()

	return file, nil
}
