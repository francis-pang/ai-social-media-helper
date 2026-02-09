package chat

// imagen.go provides a REST API client for Imagen 3 mask-based image editing
// via the Vertex AI API. Used in Phase 3 of the multi-step enhancement pipeline
// for localized surgical edits (object removal, background cleanup, inpainting).
// See DDR-031: Multi-Step Photo Enhancement Pipeline.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
)

// ImagenClient calls the Imagen 3 model via Vertex AI REST API for mask-based editing.
type ImagenClient struct {
	projectID   string
	region      string
	accessToken string // GCP OAuth2 access token
	httpClient  *http.Client
}

// NewImagenClient creates a new client for Imagen 3 editing.
// accessToken is a GCP OAuth2 access token (not the Gemini API key).
func NewImagenClient(projectID, region, accessToken string) *ImagenClient {
	return &ImagenClient{
		projectID:   projectID,
		region:      region,
		accessToken: accessToken,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// --- Vertex AI Imagen 3 request/response types ---

type imagenRequest struct {
	Instances  []imagenInstance `json:"instances"`
	Parameters imagenParameters `json:"parameters"`
}

type imagenInstance struct {
	Prompt string      `json:"prompt"`
	Image  imagenData  `json:"image"`
	Mask   *imagenMask `json:"mask,omitempty"`
}

type imagenData struct {
	BytesBase64Encoded string `json:"bytesBase64Encoded"`
}

type imagenMask struct {
	Image imagenData `json:"image"`
	// MaskMode: "MASK_MODE_FOREGROUND" means white pixels are edited.
	MaskMode string `json:"maskMode,omitempty"`
}

type imagenParameters struct {
	SampleCount int    `json:"sampleCount"`
	EditMode    string `json:"editMode,omitempty"` // "inpainting-insert", "inpainting-remove", "outpainting"
}

type imagenResponse struct {
	Predictions []imagenPrediction `json:"predictions"`
	Error       *imagenError       `json:"error,omitempty"`
}

type imagenPrediction struct {
	BytesBase64Encoded string `json:"bytesBase64Encoded"`
	MimeType           string `json:"mimeType"`
}

type imagenError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ImagenEditResult holds the result of an Imagen 3 edit.
type ImagenEditResult struct {
	ImageData []byte
	MIMEType  string
}

// EditWithMask performs a mask-based edit on an image using Imagen 3.
// The mask is a binary image where white pixels indicate the region to edit.
//
// Parameters:
//   - imageData: raw bytes of the source image
//   - maskData: raw bytes of the mask image (same dimensions, white = edit region)
//   - prompt: description of what to do in the masked region
//   - editMode: "inpainting-remove" (remove content) or "inpainting-insert" (add content)
func (c *ImagenClient) EditWithMask(ctx context.Context, imageData []byte, maskData []byte, prompt string, editMode string) (*ImagenEditResult, error) {
	log.Debug().
		Str("instruction", truncateString(prompt, 100)).
		Str("edit_mode", editMode).
		Int("image_bytes", len(imageData)).
		Int("mask_bytes", len(maskData)).
		Msg("EditWithMask: Starting Imagen API call")

	startTime := time.Now()

	req := imagenRequest{
		Instances: []imagenInstance{
			{
				Prompt: prompt,
				Image: imagenData{
					BytesBase64Encoded: base64.StdEncoding.EncodeToString(imageData),
				},
				Mask: &imagenMask{
					Image: imagenData{
						BytesBase64Encoded: base64.StdEncoding.EncodeToString(maskData),
					},
					MaskMode: "MASK_MODE_FOREGROUND",
				},
			},
		},
		Parameters: imagenParameters{
			SampleCount: 1,
			EditMode:    editMode,
		},
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf(
		"https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/google/models/imagen-3.0-capability-001:predict",
		c.region, c.projectID, c.region,
	)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.accessToken)

	resp, err := c.httpClient.Do(httpReq)
	httpDuration := time.Since(startTime)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	log.Debug().
		Int("status_code", resp.StatusCode).
		Dur("duration", httpDuration).
		Msg("EditWithMask: HTTP call completed")

	if resp.StatusCode != http.StatusOK {
		log.Error().
			Int("status", resp.StatusCode).
			Str("body", truncateString(string(respBody), 500)).
			Msg("Imagen 3 API returned error")
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, truncateString(string(respBody), 200))
	}

	var imagenResp imagenResponse
	if err := json.Unmarshal(respBody, &imagenResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if imagenResp.Error != nil {
		return nil, fmt.Errorf("API error: %s (code: %d)", imagenResp.Error.Message, imagenResp.Error.Code)
	}

	if len(imagenResp.Predictions) == 0 {
		return nil, fmt.Errorf("no predictions returned from Imagen 3")
	}

	decoded, err := base64.StdEncoding.DecodeString(imagenResp.Predictions[0].BytesBase64Encoded)
	if err != nil {
		return nil, fmt.Errorf("failed to decode response image: %w", err)
	}

	totalDuration := time.Since(startTime)
	log.Debug().
		Int("output_bytes", len(decoded)).
		Dur("duration", totalDuration).
		Msg("EditWithMask: Imagen API call completed successfully")

	return &ImagenEditResult{
		ImageData: decoded,
		MIMEType:  imagenResp.Predictions[0].MimeType,
	}, nil
}

// IsConfigured returns true if the Imagen client has the required Vertex AI configuration.
func (c *ImagenClient) IsConfigured() bool {
	return c.projectID != "" && c.region != "" && c.accessToken != ""
}

// --- Mask Generation Helpers ---

// RegionMask defines a rectangular region for mask generation.
type RegionMask struct {
	// Region name from Gemini analysis (e.g., "top-left", "center", "bottom-right")
	Region string
}

// GenerateRegionMask creates a mask image for a named region of the photo.
// The mask is a JPEG image with the same dimensions where white pixels indicate
// the area to edit. Region names map to quadrants/sections of the image.
//
// Supported regions:
//
//	"top-left", "top-center", "top-right",
//	"center-left", "center", "center-right",
//	"bottom-left", "bottom-center", "bottom-right",
//	"background", "foreground", "global"
func GenerateRegionMask(width, height int, region string) ([]byte, error) {
	log.Debug().
		Str("region", region).
		Int("width", width).
		Int("height", height).
		Msg("GenerateRegionMask: Creating mask")

	mask := image.NewRGBA(image.Rect(0, 0, width, height))

	// Fill entire mask with black (keep)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			mask.Set(x, y, color.Black)
		}
	}

	// Define region boundaries (3x3 grid with overlap margins)
	thirdW := width / 3
	thirdH := height / 3
	margin := width / 20 // 5% margin for overlap

	var x1, y1, x2, y2 int
	switch region {
	case "top-left":
		x1, y1 = 0, 0
		x2, y2 = thirdW+margin, thirdH+margin
	case "top-center":
		x1, y1 = thirdW-margin, 0
		x2, y2 = 2*thirdW+margin, thirdH+margin
	case "top-right":
		x1, y1 = 2*thirdW-margin, 0
		x2, y2 = width, thirdH+margin
	case "center-left":
		x1, y1 = 0, thirdH-margin
		x2, y2 = thirdW+margin, 2*thirdH+margin
	case "center":
		x1, y1 = thirdW-margin, thirdH-margin
		x2, y2 = 2*thirdW+margin, 2*thirdH+margin
	case "center-right":
		x1, y1 = 2*thirdW-margin, thirdH-margin
		x2, y2 = width, 2*thirdH+margin
	case "bottom-left":
		x1, y1 = 0, 2*thirdH-margin
		x2, y2 = thirdW+margin, height
	case "bottom-center":
		x1, y1 = thirdW-margin, 2*thirdH-margin
		x2, y2 = 2*thirdW+margin, height
	case "bottom-right":
		x1, y1 = 2*thirdW-margin, 2*thirdH-margin
		x2, y2 = width, height
	case "background":
		// Use edges (20% from each side) as background proxy
		edgeW := width / 5
		edgeH := height / 5
		// Top edge
		fillRegion(mask, 0, 0, width, edgeH, color.White)
		// Bottom edge
		fillRegion(mask, 0, height-edgeH, width, height, color.White)
		// Left edge
		fillRegion(mask, 0, edgeH, edgeW, height-edgeH, color.White)
		// Right edge
		fillRegion(mask, width-edgeW, edgeH, width, height-edgeH, color.White)
		return encodeMaskJPEG(mask)
	case "foreground":
		// Center 60% of the image
		x1, y1 = width/5, height/5
		x2, y2 = 4*width/5, 4*height/5
	case "global":
		// Entire image is white (edit everything)
		fillRegion(mask, 0, 0, width, height, color.White)
		return encodeMaskJPEG(mask)
	default:
		return nil, fmt.Errorf("unknown region: %s", region)
	}

	// Clamp boundaries
	if x1 < 0 {
		x1 = 0
	}
	if y1 < 0 {
		y1 = 0
	}
	if x2 > width {
		x2 = width
	}
	if y2 > height {
		y2 = height
	}

	// Fill the target region with white (edit)
	fillRegion(mask, x1, y1, x2, y2, color.White)

	return encodeMaskJPEG(mask)
}

// fillRegion fills a rectangular area of the mask with the given color.
func fillRegion(img *image.RGBA, x1, y1, x2, y2 int, c color.Color) {
	for y := y1; y < y2; y++ {
		for x := x1; x < x2; x++ {
			img.Set(x, y, c)
		}
	}
}

// encodeMaskJPEG encodes an image as a JPEG byte slice.
func encodeMaskJPEG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		return nil, fmt.Errorf("failed to encode mask: %w", err)
	}
	return buf.Bytes(), nil
}
