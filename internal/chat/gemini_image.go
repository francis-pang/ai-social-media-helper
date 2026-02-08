package chat

// gemini_image.go provides a REST API client for Gemini 3 Pro Image editing.
// This uses direct HTTP calls instead of the Go SDK because the current SDK
// (github.com/google/generative-ai-go v0.19.0) does not support image output.
// See DDR-031: Multi-Step Photo Enhancement Pipeline.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
)

// geminiBaseURL is the Gemini REST API base URL.
const geminiBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// GeminiImageClient calls the Gemini 3 Pro Image model via REST API for photo editing.
type GeminiImageClient struct {
	apiKey     string
	model      string
	httpClient *http.Client
}

// NewGeminiImageClient creates a new client for Gemini image editing.
func NewGeminiImageClient(apiKey string) *GeminiImageClient {
	return &GeminiImageClient{
		apiKey: apiKey,
		model:  ModelGemini3ProImage,
		httpClient: &http.Client{
			Timeout: 120 * time.Second, // Image generation can take 10-30s
		},
	}
}

// --- REST API request/response types ---

type geminiRequest struct {
	Contents          []geminiContent         `json:"contents"`
	SystemInstruction *geminiContent          `json:"systemInstruction,omitempty"`
	GenerationConfig  *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text       string          `json:"text,omitempty"`
	InlineData *geminiBlobData `json:"inlineData,omitempty"`
}

type geminiGenerationConfig struct {
	ResponseModalities []string `json:"responseModalities,omitempty"`
}

type geminiBlobData struct {
	MIMEType string `json:"mimeType"`
	Data     string `json:"data"` // base64 encoded
}

type geminiResponse struct {
	Candidates []geminiCandidate `json:"candidates"`
	Error      *geminiError      `json:"error,omitempty"`
}

type geminiCandidate struct {
	Content geminiContent `json:"content"`
}

type geminiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
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
	return c.EditImageMultiTurn(ctx, imageData, imageMIMEType, instruction, systemInstruction, nil)
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
	log.Info().
		Str("model", c.model).
		Int("image_bytes", len(imageData)).
		Str("image_mime", imageMIMEType).
		Int("history_turns", len(history)).
		Msg("Sending image to Gemini for editing")

	// Build request
	req := geminiRequest{
		GenerationConfig: &geminiGenerationConfig{
			ResponseModalities: []string{"TEXT", "IMAGE"},
		},
	}

	// Add system instruction if provided
	if systemInstruction != "" {
		req.SystemInstruction = &geminiContent{
			Parts: []geminiPart{{Text: systemInstruction}},
		}
	}

	// Add conversation history
	for _, turn := range history {
		content := geminiContent{
			Role: turn.Role,
		}
		if turn.ImageData != nil {
			content.Parts = append(content.Parts, geminiPart{
				InlineData: &geminiBlobData{
					MIMEType: turn.ImageMIME,
					Data:     base64.StdEncoding.EncodeToString(turn.ImageData),
				},
			})
		}
		if turn.Text != "" {
			content.Parts = append(content.Parts, geminiPart{Text: turn.Text})
		}
		req.Contents = append(req.Contents, content)
	}

	// Add current turn: image + instruction
	currentParts := []geminiPart{
		{
			InlineData: &geminiBlobData{
				MIMEType: imageMIMEType,
				Data:     base64.StdEncoding.EncodeToString(imageData),
			},
		},
		{Text: instruction},
	}
	req.Contents = append(req.Contents, geminiContent{
		Role:  "user",
		Parts: currentParts,
	})

	// Marshal request body
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Build URL
	url := fmt.Sprintf("%s/models/%s:generateContent?key=%s", geminiBaseURL, c.model, c.apiKey)

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Execute request
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		log.Error().
			Int("status", resp.StatusCode).
			Str("body", truncateString(string(respBody), 500)).
			Msg("Gemini image editing API returned error")
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, truncateString(string(respBody), 200))
	}

	// Parse response
	var geminiResp geminiResponse
	if err := json.Unmarshal(respBody, &geminiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if geminiResp.Error != nil {
		return nil, fmt.Errorf("API error: %s (code: %d)", geminiResp.Error.Message, geminiResp.Error.Code)
	}

	// Extract image and text from response
	result := &GeminiImageResult{}
	for _, candidate := range geminiResp.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.InlineData != nil {
				decoded, err := base64.StdEncoding.DecodeString(part.InlineData.Data)
				if err != nil {
					return nil, fmt.Errorf("failed to decode image data: %w", err)
				}
				result.ImageData = decoded
				result.ImageMIMEType = part.InlineData.MIMEType
			}
			if part.Text != "" {
				result.Text += part.Text
			}
		}
	}

	if result.ImageData == nil {
		return nil, fmt.Errorf("no image returned in response (text: %s)", truncateString(result.Text, 200))
	}

	log.Info().
		Int("output_bytes", len(result.ImageData)).
		Str("output_mime", result.ImageMIMEType).
		Dur("duration", time.Since(startTime)).
		Msg("Gemini image editing complete")

	return result, nil
}

// AnalyzeImage sends an image to Gemini 3 Pro for text-only analysis.
// Used in Phase 2 to determine what further enhancements are needed.
func (c *GeminiImageClient) AnalyzeImage(ctx context.Context, imageData []byte, imageMIMEType string, analysisPrompt string, systemInstruction string) (string, error) {
	startTime := time.Now()
	log.Info().
		Str("model", ModelGemini3ProPreview).
		Int("image_bytes", len(imageData)).
		Msg("Sending image to Gemini for analysis")

	// Use Pro (text) model for analysis, not the image model
	req := geminiRequest{
		GenerationConfig: &geminiGenerationConfig{
			ResponseModalities: []string{"TEXT"},
		},
		Contents: []geminiContent{
			{
				Role: "user",
				Parts: []geminiPart{
					{
						InlineData: &geminiBlobData{
							MIMEType: imageMIMEType,
							Data:     base64.StdEncoding.EncodeToString(imageData),
						},
					},
					{Text: analysisPrompt},
				},
			},
		},
	}

	if systemInstruction != "" {
		req.SystemInstruction = &geminiContent{
			Parts: []geminiPart{{Text: systemInstruction}},
		}
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// Use the text Pro model for analysis
	url := fmt.Sprintf("%s/models/%s:generateContent?key=%s", geminiBaseURL, ModelGemini3ProPreview, c.apiKey)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API returned status %d: %s", resp.StatusCode, truncateString(string(respBody), 200))
	}

	var geminiResp geminiResponse
	if err := json.Unmarshal(respBody, &geminiResp); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	if geminiResp.Error != nil {
		return "", fmt.Errorf("API error: %s", geminiResp.Error.Message)
	}

	var text string
	for _, candidate := range geminiResp.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.Text != "" {
				text += part.Text
			}
		}
	}

	log.Info().
		Int("response_length", len(text)).
		Dur("duration", time.Since(startTime)).
		Msg("Gemini image analysis complete")

	return text, nil
}

// truncateString truncates a string to maxLen, appending "..." if truncated.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
