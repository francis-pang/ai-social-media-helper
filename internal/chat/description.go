package chat

// description.go implements AI-generated Instagram carousel captions.
// See DDR-036: AI Post Description Generation with Full Media Context.
//
// The caption is generated using Gemini with full media context — actual
// thumbnails and compressed videos are sent alongside the post group label,
// trip context, and media metadata. This produces higher-quality captions
// that can reference specific visual details in the media.
//
// Multi-turn feedback: The user can iteratively refine the caption by
// providing feedback (e.g., "make it shorter", "more casual"). Gemini
// receives the full conversation history for contextual regeneration.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fpang/gemini-media-cli/internal/assets"
	"github.com/fpang/gemini-media-cli/internal/jsonutil"
	"github.com/rs/zerolog/log"
	"google.golang.org/genai"
)

// --- Description types (DDR-036) ---

// DescriptionResult is the structured AI caption output.
type DescriptionResult struct {
	Caption     string   `json:"caption"`
	Hashtags    []string `json:"hashtags"`
	LocationTag string   `json:"locationTag"`
}

// DescriptionMediaItem represents a media item to include in the description prompt.
// This contains the data needed to send to Gemini — thumbnails for images,
// compressed video data (or Files API reference) for videos.
type DescriptionMediaItem struct {
	Filename string // display filename
	Type     string // "Photo" or "Video"
	Scene    string // scene name from selection step (if available)

	// For images: inline thumbnail data
	ThumbnailData     []byte
	ThumbnailMIMEType string

	// For videos: Gemini Files API reference (uploaded separately)
	VideoFileURI  string
	VideoMIMEType string

	// Metadata
	GPSLat  float64
	GPSLon  float64
	HasGPS  bool
	Date    string // formatted date string
	HasDate bool
}

// DescriptionConversationEntry records one round of description feedback.
type DescriptionConversationEntry struct {
	UserFeedback  string `json:"userFeedback"`
	ModelResponse string `json:"modelResponse"` // raw JSON response from Gemini
}

// --- Description generation ---

// GenerateDescription sends media with context to Gemini and returns a structured caption.
// groupLabel is the user's descriptive text for the post group (from Step 6).
// tripContext is the overall trip/event description (from Step 1).
// mediaItems contains the thumbnail data and metadata for each item in the group.
// cacheMgr is an optional CacheManager for context caching (DDR-065). Pass nil to disable.
// sessionID is required when cacheMgr is provided.
func GenerateDescription(
	ctx context.Context,
	client *genai.Client,
	groupLabel string,
	tripContext string,
	mediaItems []DescriptionMediaItem,
	cacheMgr *CacheManager,
	sessionID string,
	ragContext string,
) (*DescriptionResult, string, error) {
	log.Debug().
		Str("group_label", truncateString(groupLabel, 100)).
		Str("trip_context", truncateString(tripContext, 100)).
		Int("media_count", len(mediaItems)).
		Msg("Starting description generation")

	// Build the user prompt
	prompt := BuildDescriptionPrompt(groupLabel, tripContext, mediaItems, ragContext)

	// Configure model with description system instruction
	config := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{{Text: assets.DescriptionSystemPrompt}},
		},
	}

	// Build parts: media first, then text prompt
	var parts []*genai.Part

	// Add Francis reference photo for identification (DDR-017)
	parts = append(parts, &genai.Part{
		InlineData: &genai.Blob{
			MIMEType: assets.FrancisReferenceMIMEType,
			Data:     assets.FrancisReferencePhoto,
		},
	})

	// Add media items
	for _, item := range mediaItems {
		if item.Type == "Photo" && len(item.ThumbnailData) > 0 {
			parts = append(parts, &genai.Part{
				InlineData: &genai.Blob{
					MIMEType: item.ThumbnailMIMEType,
					Data:     item.ThumbnailData,
				},
			})
		} else if item.Type == "Video" && item.VideoFileURI != "" {
			parts = append(parts, &genai.Part{
				FileData: &genai.FileData{
					MIMEType: item.VideoMIMEType,
					FileURI:  item.VideoFileURI,
				},
			})
		}
	}

	// Add the text prompt
	parts = append(parts, &genai.Part{Text: prompt})

	log.Info().
		Int("media_parts", len(parts)-2). // -2 for reference photo and prompt
		Bool("cache_enabled", cacheMgr != nil).
		Msg("Sending media to Gemini for caption generation...")

	// Generate content
	modelName := GetModelName()
	callStart := time.Now()

	var resp *genai.GenerateContentResponse
	var err error

	if cacheMgr != nil && sessionID != "" {
		// DDR-065: Use context caching for media + system instruction.
		mediaParts := parts[:len(parts)-1] // All parts except the text prompt
		cacheContents := []*genai.Content{{Role: "user", Parts: mediaParts}}
		userParts := []*genai.Part{{Text: prompt}}

		log.Debug().
			Str("model", modelName).
			Int("media_parts", len(mediaParts)).
			Msg("Starting cached Gemini API call for description generation")

		resp, err = cacheMgr.GenerateWithCache(ctx, CacheConfig{
			SessionID: sessionID,
			Operation: "description",
		}, modelName, config.SystemInstruction, cacheContents, userParts, nil)
	} else {
		log.Debug().
			Str("model", modelName).
			Int("prompt_length", len(prompt)).
			Int("media_part_count", len(parts)-1).
			Msg("Starting Gemini API call for description generation")
		contents := []*genai.Content{{Role: "user", Parts: parts}}
		resp, err = client.Models.GenerateContent(ctx, modelName, contents, config)
	}

	duration := time.Since(callStart)
	if err != nil {
		log.Error().Err(err).Dur("duration", duration).Msg("Failed to generate description from Gemini")
		return nil, "", fmt.Errorf("failed to generate content: %w", err)
	}

	if resp == nil {
		return nil, "", fmt.Errorf("received empty response from Gemini API")
	}

	responseText := resp.Text()
	log.Debug().
		Int("response_length", len(responseText)).
		Dur("duration", duration).
		Msg("Gemini API response received for description generation")

	// Parse JSON response
	log.Debug().Msg("Parsing description response")
	result, err := parseDescriptionResponse(responseText)
	if err != nil {
		log.Debug().Err(err).Msg("Failed to parse description response")
		return nil, responseText, fmt.Errorf("failed to parse description response: %w", err)
	}

	log.Debug().
		Int("caption_length", len(result.Caption)).
		Int("hashtag_count", len(result.Hashtags)).
		Str("location", result.LocationTag).
		Msg("Description response parsed successfully")

	log.Info().
		Int("caption_length", len(result.Caption)).
		Int("hashtag_count", len(result.Hashtags)).
		Str("location", result.LocationTag).
		Msg("Caption generation complete")

	return result, responseText, nil
}

// RegenerateDescription regenerates a caption using multi-turn feedback.
// The conversation history provides context for Gemini to understand what
// the user wants changed.
func RegenerateDescription(
	ctx context.Context,
	client *genai.Client,
	groupLabel string,
	tripContext string,
	mediaItems []DescriptionMediaItem,
	feedback string,
	history []DescriptionConversationEntry,
) (*DescriptionResult, string, error) {
	log.Debug().
		Str("group_label", truncateString(groupLabel, 100)).
		Int("feedback_length", len(feedback)).
		Int("history_length", len(history)).
		Int("media_count", len(mediaItems)).
		Msg("Starting description regeneration with feedback")

	// Configure model with description system instruction
	config := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{{Text: assets.DescriptionSystemPrompt}},
		},
	}

	// Build the initial user message with media
	var initialParts []*genai.Part

	// Add Francis reference photo
	initialParts = append(initialParts, &genai.Part{
		InlineData: &genai.Blob{
			MIMEType: assets.FrancisReferenceMIMEType,
			Data:     assets.FrancisReferencePhoto,
		},
	})

	// Add media items
	for _, item := range mediaItems {
		if item.Type == "Photo" && len(item.ThumbnailData) > 0 {
			initialParts = append(initialParts, &genai.Part{
				InlineData: &genai.Blob{
					MIMEType: item.ThumbnailMIMEType,
					Data:     item.ThumbnailData,
				},
			})
		} else if item.Type == "Video" && item.VideoFileURI != "" {
			initialParts = append(initialParts, &genai.Part{
				FileData: &genai.FileData{
					MIMEType: item.VideoMIMEType,
					FileURI:  item.VideoFileURI,
				},
			})
		}
	}

	// Add the original prompt
	prompt := BuildDescriptionPrompt(groupLabel, tripContext, mediaItems, "")
	initialParts = append(initialParts, &genai.Part{Text: prompt})

	// Build multi-turn conversation
	var contents []*genai.Content

	// First message: original request with media
	contents = append(contents, &genai.Content{Role: "user", Parts: initialParts})

	// Add conversation history
	for _, entry := range history {
		// Model's previous response
		contents = append(contents, &genai.Content{
			Role:  "model",
			Parts: []*genai.Part{{Text: entry.ModelResponse}},
		})
		// User's feedback
		contents = append(contents, &genai.Content{
			Role:  "user",
			Parts: []*genai.Part{{Text: entry.UserFeedback}},
		})
	}

	// Add the new feedback as the latest user message
	feedbackPrompt := fmt.Sprintf(
		"Please update the caption based on this feedback: %s\n\nRespond with ONLY the updated JSON object in the same format.",
		feedback,
	)
	contents = append(contents, &genai.Content{
		Role:  "model",
		Parts: []*genai.Part{{Text: history[len(history)-1].ModelResponse}},
	})
	// If there's existing history, the last model response was already added above.
	// We need to handle the case where history is empty vs non-empty.
	if len(history) == 0 {
		// No history — this shouldn't happen (first generation doesn't use this function)
		return nil, "", fmt.Errorf("regeneration requires at least one previous generation")
	}

	// Remove the duplicate last model response we just added
	contents = contents[:len(contents)-1]

	// Add the feedback message
	contents = append(contents, &genai.Content{
		Role:  "user",
		Parts: []*genai.Part{{Text: feedbackPrompt}},
	})

	log.Info().
		Int("conversation_turns", len(contents)).
		Msg("Sending multi-turn feedback to Gemini...")

	// Generate content with conversation history
	modelName := GetModelName()
	callStart := time.Now()
	log.Debug().
		Str("model", modelName).
		Int("conversation_turns", len(contents)).
		Msg("Starting Gemini API call for description regeneration")
	resp, err := client.Models.GenerateContent(ctx, modelName, contents, config)
	duration := time.Since(callStart)
	if err != nil {
		log.Error().Err(err).Dur("duration", duration).Msg("Failed to regenerate description from Gemini")
		return nil, "", fmt.Errorf("failed to generate content: %w", err)
	}

	if resp == nil {
		return nil, "", fmt.Errorf("received empty response from Gemini API")
	}

	responseText := resp.Text()
	log.Debug().
		Int("response_length", len(responseText)).
		Dur("duration", duration).
		Msg("Gemini API response received for description regeneration")

	// Parse JSON response
	result, err := parseDescriptionResponse(responseText)
	if err != nil {
		return nil, responseText, fmt.Errorf("failed to parse regenerated description: %w", err)
	}

	log.Info().
		Int("caption_length", len(result.Caption)).
		Int("hashtag_count", len(result.Hashtags)).
		Msg("Caption regeneration complete")

	return result, responseText, nil
}

// --- Prompt building ---

// BuildDescriptionPrompt creates the user prompt for caption generation.
// Combines the group label, trip context, and media metadata into a structured prompt.
func BuildDescriptionPrompt(groupLabel string, tripContext string, mediaItems []DescriptionMediaItem, ragContext string) string {
	log.Trace().
		Int("media_count", len(mediaItems)).
		Msg("Building description prompt")
	var sb strings.Builder

	sb.WriteString("## Instagram Carousel Caption Request\n\n")

	// Count media types
	var photoCount, videoCount int
	for _, item := range mediaItems {
		if item.Type == "Photo" {
			photoCount++
		} else if item.Type == "Video" {
			videoCount++
		}
	}

	sb.WriteString(fmt.Sprintf("Generate a caption for a carousel post with %d items (%d photos, %d videos).\n\n",
		len(mediaItems), photoCount, videoCount))

	// Group label (primary context)
	sb.WriteString("### Post Group Description (from user)\n\n")
	if groupLabel != "" {
		sb.WriteString(groupLabel)
		sb.WriteString("\n\n")
	} else {
		sb.WriteString("No description provided. Infer the theme from the media content.\n\n")
	}

	// Trip context (secondary context)
	sb.WriteString("### Trip/Event Context\n\n")
	if tripContext != "" {
		sb.WriteString(tripContext)
		sb.WriteString("\n\n")
	} else {
		sb.WriteString("No overall context provided.\n\n")
	}

	// Media metadata
	sb.WriteString("### Media Details\n\n")
	sb.WriteString("The media files are provided in the same order as listed below. The first image is Francis's reference photo (not part of the post).\n\n")

	for i, item := range mediaItems {
		sb.WriteString(fmt.Sprintf("**Item %d: %s** [%s]\n", i+1, item.Filename, item.Type))
		if item.Scene != "" {
			sb.WriteString(fmt.Sprintf("- Scene: %s\n", item.Scene))
		}
		if item.HasGPS {
			sb.WriteString(fmt.Sprintf("- GPS: %.6f, %.6f\n", item.GPSLat, item.GPSLon))
		}
		if item.HasDate {
			sb.WriteString(fmt.Sprintf("- Date: %s\n", item.Date))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("### Instructions\n\n")
	sb.WriteString("1. Look at ALL the provided media to understand the visual story\n")
	sb.WriteString("2. Use the group description as your primary guide for the caption's theme and tone\n")
	sb.WriteString("3. Reference specific visual details you see in the photos/videos\n")
	sb.WriteString("4. Use GPS coordinates to identify the location for the location tag\n")
	sb.WriteString("5. Respond with ONLY the JSON object as specified in the system instruction\n")

	prompt := sb.String()
	if ragContext != "" {
		prompt = ragContext + "\n\n" + prompt
	}
	return prompt
}

// --- Response parsing ---

// parseDescriptionResponse extracts and parses the JSON caption from Gemini's response.
func parseDescriptionResponse(response string) (*DescriptionResult, error) {
	log.Debug().
		Int("response_length", len(response)).
		Msg("Parsing description response JSON")
	result, err := jsonutil.ParseJSON[DescriptionResult](response)
	if err != nil {
		log.Error().Err(err).Str("response", response).Msg("Failed to parse description response")
		return nil, fmt.Errorf("description response: %w", err)
	}
	if result.Caption == "" {
		return nil, fmt.Errorf("empty caption in description response")
	}
	log.Debug().
		Int("caption_length", len(result.Caption)).
		Int("hashtag_count", len(result.Hashtags)).
		Str("location_tag", result.LocationTag).
		Msg("Description response parsed successfully")
	return &result, nil
}
