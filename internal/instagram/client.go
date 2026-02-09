// Package instagram provides a client for the Instagram Graph API
// content publishing endpoints. It supports creating and publishing
// single-media posts, reels, and carousels (up to 20 items).
//
// The client requires a long-lived Instagram access token and user ID,
// both typically loaded from SSM Parameter Store at Lambda cold start.
//
// Instagram publishing is a multi-step process:
//  1. Create media containers (one per item, uploaded via public URL)
//  2. For carousels: create a carousel container referencing child containers
//  3. Publish the container
//  4. For videos: poll container status until processing completes before publishing
//
// See DDR-040: Instagram Publishing Client for the full design.
package instagram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

const (
	// defaultBaseURL is the Instagram Graph API base URL.
	defaultBaseURL = "https://graph.instagram.com/v22.0"

	// defaultTimeout is the HTTP client timeout for API calls.
	defaultTimeout = 30 * time.Second

	// maxCarouselItems is the Instagram carousel size limit.
	maxCarouselItems = 20

	// Video container processing poll settings.
	initialPollInterval = 5 * time.Second
	maxPollInterval     = 30 * time.Second
	defaultPollTimeout  = 5 * time.Minute
)

// Client provides methods for publishing to Instagram via the Graph API.
type Client struct {
	httpClient  *http.Client
	accessToken string
	userID      string
	baseURL     string
}

// NewClient creates an Instagram API client.
// accessToken and userID are loaded from SSM Parameter Store at Lambda cold start.
func NewClient(accessToken, userID string) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
		accessToken: accessToken,
		userID:      userID,
		baseURL:     defaultBaseURL,
	}
}

// --- API response types ---

// apiResponse is the generic Instagram Graph API response.
type apiResponse struct {
	ID    string   `json:"id"`
	Error *apiErr  `json:"error,omitempty"`
}

type apiErr struct {
	Message   string `json:"message"`
	Type      string `json:"type"`
	Code      int    `json:"code"`
	FBTraceID string `json:"fbtrace_id,omitempty"`
}

// containerStatusResponse is the response from GET /{container_id}?fields=status_code,status.
type containerStatusResponse struct {
	ID         string `json:"id"`
	StatusCode string `json:"status_code"` // IN_PROGRESS, FINISHED, ERROR
	Status     string `json:"status,omitempty"`
	Error      *apiErr `json:"error,omitempty"`
}

// --- Container creation ---

// CreateImageContainer creates an image media container.
// imageURL must be a publicly accessible URL (e.g., presigned S3 GET URL).
// If isCarousel is true, the container is created as a carousel child item.
func (c *Client) CreateImageContainer(ctx context.Context, imageURL string, isCarousel bool) (string, error) {
	log.Debug().Bool("isCarousel", isCarousel).Msg("Creating image container")
	params := url.Values{
		"image_url":    {imageURL},
		"access_token": {c.accessToken},
	}
	if isCarousel {
		params.Set("is_carousel_item", "true")
	}

	resp, err := c.postForm(ctx, fmt.Sprintf("/%s/media", c.userID), params)
	if err != nil {
		return "", fmt.Errorf("create image container: %w", err)
	}
	log.Info().Str("containerId", resp.ID).Str("type", "image").Msg("Image container created")
	return resp.ID, nil
}

// CreateVideoContainer creates a video/reel media container.
// videoURL must be a publicly accessible URL.
// If isCarousel is true, the container is created as a carousel child item.
// If isCarousel is false, the video is published as a Reel.
func (c *Client) CreateVideoContainer(ctx context.Context, videoURL string, isCarousel bool) (string, error) {
	params := url.Values{
		"video_url":    {videoURL},
		"access_token": {c.accessToken},
	}
	if isCarousel {
		params.Set("is_carousel_item", "true")
		params.Set("media_type", "VIDEO")
	} else {
		params.Set("media_type", "REELS")
	}

	resp, err := c.postForm(ctx, fmt.Sprintf("/%s/media", c.userID), params)
	if err != nil {
		return "", fmt.Errorf("create video container: %w", err)
	}
	return resp.ID, nil
}

// CreateCarouselContainer creates a carousel container from child container IDs.
// caption is the full post caption text (including hashtags).
func (c *Client) CreateCarouselContainer(ctx context.Context, children []string, caption string) (string, error) {
	if len(children) < 2 {
		return "", fmt.Errorf("carousel requires at least 2 items, got %d", len(children))
	}
	if len(children) > maxCarouselItems {
		return "", fmt.Errorf("carousel supports at most %d items, got %d", maxCarouselItems, len(children))
	}

	params := url.Values{
		"media_type":   {"CAROUSEL"},
		"children":     {strings.Join(children, ",")},
		"caption":      {caption},
		"access_token": {c.accessToken},
	}

	resp, err := c.postForm(ctx, fmt.Sprintf("/%s/media", c.userID), params)
	if err != nil {
		return "", fmt.Errorf("create carousel container: %w", err)
	}
	return resp.ID, nil
}

// CreateSingleImagePost creates a single-image post container with caption.
func (c *Client) CreateSingleImagePost(ctx context.Context, imageURL, caption string) (string, error) {
	params := url.Values{
		"image_url":    {imageURL},
		"caption":      {caption},
		"access_token": {c.accessToken},
	}

	resp, err := c.postForm(ctx, fmt.Sprintf("/%s/media", c.userID), params)
	if err != nil {
		return "", fmt.Errorf("create single image post: %w", err)
	}
	return resp.ID, nil
}

// CreateSingleReelPost creates a single reel (video) post container with caption.
func (c *Client) CreateSingleReelPost(ctx context.Context, videoURL, caption string) (string, error) {
	params := url.Values{
		"video_url":    {videoURL},
		"media_type":   {"REELS"},
		"caption":      {caption},
		"access_token": {c.accessToken},
	}

	resp, err := c.postForm(ctx, fmt.Sprintf("/%s/media", c.userID), params)
	if err != nil {
		return "", fmt.Errorf("create single reel post: %w", err)
	}
	return resp.ID, nil
}

// --- Publishing ---

// Publish publishes a media container (carousel or single).
// Returns the Instagram media ID of the published post.
func (c *Client) Publish(ctx context.Context, containerID string) (string, error) {
	log.Debug().Str("containerId", containerID).Msg("Publishing container")
	params := url.Values{
		"creation_id":  {containerID},
		"access_token": {c.accessToken},
	}

	resp, err := c.postForm(ctx, fmt.Sprintf("/%s/media_publish", c.userID), params)
	if err != nil {
		return "", fmt.Errorf("publish container %s: %w", containerID, err)
	}
	log.Info().Str("containerId", containerID).Str("postId", resp.ID).Msg("Container published successfully")
	return resp.ID, nil
}

// --- Status polling ---

// ContainerStatus returns the processing status of a media container.
// Used for video containers which require server-side processing.
// Returns: "IN_PROGRESS", "FINISHED", or "ERROR".
func (c *Client) ContainerStatus(ctx context.Context, containerID string) (string, error) {
	endpoint := fmt.Sprintf("/%s?fields=status_code,status&access_token=%s",
		containerID, url.QueryEscape(c.accessToken))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}

	httpResp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("container status request: %w", err)
	}
	defer httpResp.Body.Close()

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	var status containerStatusResponse
	if err := json.Unmarshal(body, &status); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	if status.Error != nil {
		return "", fmt.Errorf("API error: %s (code %d)", status.Error.Message, status.Error.Code)
	}

	return status.StatusCode, nil
}

// WaitForContainer polls container status until FINISHED or ERROR.
// Uses exponential backoff: 5s, 10s, 20s, 30s (max).
func (c *Client) WaitForContainer(ctx context.Context, containerID string, timeout time.Duration) error {
	if timeout == 0 {
		timeout = defaultPollTimeout
	}

	deadline := time.Now().Add(timeout)
	interval := initialPollInterval

	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("container %s: timed out after %s waiting for processing", containerID, timeout)
		}

		status, err := c.ContainerStatus(ctx, containerID)
		if err != nil {
			// Transient errors — log and retry
			log.Warn().Err(err).Str("containerId", containerID).Msg("Container status poll error, retrying")
		} else {
			switch status {
			case "FINISHED":
				log.Debug().Str("containerId", containerID).Msg("Container processing finished")
				return nil
			case "ERROR":
				return fmt.Errorf("container %s: processing failed on Instagram's side", containerID)
			case "IN_PROGRESS":
				log.Debug().Str("containerId", containerID).Dur("nextPoll", interval).Msg("Container still processing")
			default:
				log.Warn().Str("containerId", containerID).Str("status", status).Msg("Unknown container status")
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}

		// Exponential backoff up to maxPollInterval
		interval = interval * 2
		if interval > maxPollInterval {
			interval = maxPollInterval
		}
	}
}

// --- Internal helpers ---

// postForm sends a POST request with form-encoded parameters to the Instagram API.
func (c *Client) postForm(ctx context.Context, endpoint string, params url.Values) (*apiResponse, error) {
	startTime := time.Now()
	
	// Log form parameter names (not values) at Trace level
	paramNames := make([]string, 0, len(params))
	for key := range params {
		paramNames = append(paramNames, key)
	}
	log.Trace().Strs("formParams", paramNames).Msg("Form parameters")

	log.Debug().Str("method", http.MethodPost).Str("path", endpoint).Msg("Instagram API request")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+endpoint,
		strings.NewReader(params.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	httpResp, err := c.httpClient.Do(req)
	duration := time.Since(startTime)
	if err != nil {
		log.Debug().Int("statusCode", 0).Dur("duration", duration).Err(err).Msg("Instagram API response")
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer httpResp.Body.Close()

	log.Debug().Int("statusCode", httpResp.StatusCode).Dur("duration", duration).Msg("Instagram API response")

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var resp apiResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse response: %w (body: %s)", err, truncate(string(body), 200))
	}

	if resp.Error != nil {
		log.Error().Str("errorMessage", resp.Error.Message).Str("errorType", resp.Error.Type).Int("errorCode", resp.Error.Code).Msg("Instagram API error")
		return nil, fmt.Errorf("Instagram API error: %s (type: %s, code: %d)",
			resp.Error.Message, resp.Error.Type, resp.Error.Code)
	}

	if resp.ID == "" {
		return nil, fmt.Errorf("unexpected response: no ID returned (body: %s)", truncate(string(body), 200))
	}

	return &resp, nil
}

// truncate returns the first n characters of s, appending "..." if truncated.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
