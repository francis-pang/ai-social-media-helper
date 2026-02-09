package chat

// gemini_image.go provides a client for Gemini 3 Pro Image editing using
// the google.golang.org/genai SDK. Migrated from REST API calls as part of
// SDK-A migration. See DDR-031: Multi-Step Photo Enhancement Pipeline.

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"google.golang.org/genai"
)

// GeminiImageClient calls the Gemini 3 Pro Image model via the genai SDK for photo editing.
type GeminiImageClient struct {
	client *genai.Client
	model  string
}

// NewGeminiImageClient creates a new client for Gemini image editing.
// Takes an existing genai.Client (created via NewGeminiClient).
func NewGeminiImageClient(client *genai.Client) *GeminiImageClient {
	return &GeminiImageClient{
		client: client,
		model:  ModelGemini3ProImage,
	}
}

// GeminiImageResult holds the result of a Gemini image editing call.
type GeminiImageResult struct {
	// ImageData is the raw bytes of the edited image (JPEG/PNG).
	ImageData []byte
	// ImageMIMEType is the MIME type of the output image.
	ImageMIMEType string
	// Text is any text description returned alongside the image.
	Text string
}

// EditImage sends a photo with an instruction to Gemini 3 Pro Image and returns
// the edited image. This is the core operation for Phase 1 enhancement.
//
// Parameters:
//   - imageData: raw bytes of the input image
//   - imageMIMEType: MIME type of the input image (e.g., "image/jpeg")
//   - instruction: natural language editing instruction
//   - systemInstruction: optional system-level instruction for context
func (c *GeminiImageClient) EditImage(ctx context.Context, imageData []byte, imageMIMEType string, instruction string, systemInstruction string) (*GeminiImageResult, error) {
	log.Debug().
		Int("instruction_length", len(instruction)).
		Int("image_bytes", len(imageData)).
		Str("image_mime", imageMIMEType).
		Msg("EditImage: Starting Gemini API call")

	startTime := time.Now()
	result, err := c.EditImageMultiTurn(ctx, imageData, imageMIMEType, instruction, systemInstruction, nil)
	duration := time.Since(startTime)

	if err != nil {
		return nil, err
	}

	log.Debug().
		Int("result_bytes", len(result.ImageData)).
		Dur("duration", duration).
		Msg("EditImage: Gemini API call completed")

	return result, nil
}

// ConversationTurn represents one turn in a multi-turn image editing conversation.
type ConversationTurn struct {
	Role      string // "user" or "model"
	Text      string
	ImageData []byte // optional image data for this turn
	ImageMIME string // MIME type if ImageData is set
}

// EditImageMultiTurn sends a photo with instruction and conversation history
// for multi-turn editing (feedback loops). Each turn preserves context.
func (c *GeminiImageClient) EditImageMultiTurn(ctx context.Context, imageData []byte, imageMIMEType string, instruction string, systemInstruction string, history []ConversationTurn) (*GeminiImageResult, error) {
	startTime := time.Now()
	log.Debug().
		Str("model", c.model).
		Int("image_bytes", len(imageData)).
		Str("image_mime", imageMIMEType).
		Int("history_length", len(history)).
		Msg("EditImageMultiTurn: Starting Gemini API call")

	// Build generation config with image output modality
	config := &genai.GenerateContentConfig{
		ResponseModalities: []string{"TEXT", "IMAGE"},
	}

	// Add system instruction if provided
	if systemInstruction != "" {
		config.SystemInstruction = &genai.Content{
			Parts: []*genai.Part{{Text: systemInstruction}},
		}
	}

	// Build contents: conversation history + current turn
	var contents []*genai.Content

	// Add conversation history
	for _, turn := range history {
		var parts []*genai.Part
		if turn.ImageData != nil {
			parts = append(parts, &genai.Part{
				InlineData: &genai.Blob{
					MIMEType: turn.ImageMIME,
					Data:     turn.ImageData,
				},
			})
		}
		if turn.Text != "" {
			parts = append(parts, &genai.Part{Text: turn.Text})
		}
		contents = append(contents, &genai.Content{
			Role:  turn.Role,
			Parts: parts,
		})
	}

	// Add current turn: image + instruction
	currentParts := []*genai.Part{
		{InlineData: &genai.Blob{
			MIMEType: imageMIMEType,
			Data:     imageData,
		}},
		{Text: instruction},
	}
	contents = append(contents, &genai.Content{
		Role:  "user",
		Parts: currentParts,
	})

	// Generate content
	resp, err := c.client.Models.GenerateContent(ctx, c.model, contents, config)
	if err != nil {
		return nil, fmt.Errorf("Gemini image editing failed: %w", err)
	}

	// Extract image and text from response
	result := &GeminiImageResult{}
	if resp != nil {
		for _, candidate := range resp.Candidates {
			if candidate.Content == nil {
				continue
			}
			for _, part := range candidate.Content.Parts {
				if part.InlineData != nil {
					result.ImageData = part.InlineData.Data
					result.ImageMIMEType = part.InlineData.MIMEType
				}
				if part.Text != "" {
					result.Text += part.Text
				}
			}
		}
	}

	if result.ImageData == nil {
		return nil, fmt.Errorf("no image returned in response (text: %s)", truncateString(result.Text, 200))
	}

	log.Debug().
		Int("output_bytes", len(result.ImageData)).
		Str("output_mime", result.ImageMIMEType).
		Dur("duration", time.Since(startTime)).
		Msg("EditImageMultiTurn: Gemini API call completed")

	return result, nil
}

// AnalyzeImage sends an image to Gemini 3 Pro for text-only analysis.
// Used in Phase 2 to determine what further enhancements are needed.
func (c *GeminiImageClient) AnalyzeImage(ctx context.Context, imageData []byte, imageMIMEType string, analysisPrompt string, systemInstruction string) (string, error) {
	startTime := time.Now()
	log.Debug().
		Str("model", ModelGemini3ProPreview).
		Int("image_bytes", len(imageData)).
		Int("prompt_length", len(analysisPrompt)).
		Msg("AnalyzeImage: Starting Gemini API call")

	// Build generation config — text only for analysis
	config := &genai.GenerateContentConfig{
		ResponseModalities: []string{"TEXT"},
	}

	if systemInstruction != "" {
		config.SystemInstruction = &genai.Content{
			Parts: []*genai.Part{{Text: systemInstruction}},
		}
	}

	// Build contents
	contents := []*genai.Content{
		{
			Role: "user",
			Parts: []*genai.Part{
				{InlineData: &genai.Blob{
					MIMEType: imageMIMEType,
					Data:     imageData,
				}},
				{Text: analysisPrompt},
			},
		},
	}

	// Use Pro (text) model for analysis, not the image model
	resp, err := c.client.Models.GenerateContent(ctx, ModelGemini3ProPreview, contents, config)
	if err != nil {
		return "", fmt.Errorf("Gemini image analysis failed: %w", err)
	}

	text := resp.Text()
	duration := time.Since(startTime)

	log.Debug().
		Int("response_length", len(text)).
		Dur("duration", duration).
		Msg("AnalyzeImage: Gemini API call completed")

	return text, nil
}

// truncateString truncates a string to maxLen, appending "..." if truncated.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
