package instagram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestClient creates a Client pointing at a test HTTP server.
func newTestClient(server *httptest.Server) *Client {
	return &Client{
		httpClient:  server.Client(),
		accessToken: "test-token",
		userID:      "12345",
		baseURL:     server.URL,
	}
}

func TestCreateImageContainer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/12345/media") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		r.ParseForm()
		if r.Form.Get("image_url") != "https://example.com/photo.jpg" {
			t.Errorf("unexpected image_url: %s", r.Form.Get("image_url"))
		}
		if r.Form.Get("is_carousel_item") != "true" {
			t.Errorf("expected is_carousel_item=true")
		}

		json.NewEncoder(w).Encode(apiResponse{ID: "container-img-001"})
	}))
	defer server.Close()

	client := newTestClient(server)
	id, err := client.CreateImageContainer(context.Background(), "https://example.com/photo.jpg", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "container-img-001" {
		t.Errorf("expected container-img-001, got %s", id)
	}
}

func TestCreateVideoContainer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("video_url") != "https://example.com/video.mp4" {
			t.Errorf("unexpected video_url: %s", r.Form.Get("video_url"))
		}
		if r.Form.Get("is_carousel_item") != "true" {
			t.Errorf("expected is_carousel_item=true for carousel video")
		}
		if r.Form.Get("media_type") != "VIDEO" {
			t.Errorf("expected media_type=VIDEO for carousel video, got %s", r.Form.Get("media_type"))
		}

		json.NewEncoder(w).Encode(apiResponse{ID: "container-vid-001"})
	}))
	defer server.Close()

	client := newTestClient(server)
	id, err := client.CreateVideoContainer(context.Background(), "https://example.com/video.mp4", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "container-vid-001" {
		t.Errorf("expected container-vid-001, got %s", id)
	}
}

func TestCreateVideoContainerAsReel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("media_type") != "REELS" {
			t.Errorf("expected media_type=REELS for standalone video, got %s", r.Form.Get("media_type"))
		}
		if r.Form.Get("is_carousel_item") != "" {
			t.Errorf("expected no is_carousel_item for standalone video")
		}

		json.NewEncoder(w).Encode(apiResponse{ID: "container-reel-001"})
	}))
	defer server.Close()

	client := newTestClient(server)
	id, err := client.CreateVideoContainer(context.Background(), "https://example.com/video.mp4", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "container-reel-001" {
		t.Errorf("expected container-reel-001, got %s", id)
	}
}

func TestCreateCarouselContainer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("media_type") != "CAROUSEL" {
			t.Errorf("expected media_type=CAROUSEL")
		}
		children := r.Form.Get("children")
		if children != "c1,c2,c3" {
			t.Errorf("unexpected children: %s", children)
		}
		if r.Form.Get("caption") != "Hello world" {
			t.Errorf("unexpected caption: %s", r.Form.Get("caption"))
		}

		json.NewEncoder(w).Encode(apiResponse{ID: "carousel-001"})
	}))
	defer server.Close()

	client := newTestClient(server)
	id, err := client.CreateCarouselContainer(context.Background(), []string{"c1", "c2", "c3"}, "Hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "carousel-001" {
		t.Errorf("expected carousel-001, got %s", id)
	}
}

func TestCreateCarouselContainerTooFewItems(t *testing.T) {
	client := &Client{userID: "12345", accessToken: "tok"}
	_, err := client.CreateCarouselContainer(context.Background(), []string{"c1"}, "caption")
	if err == nil || !strings.Contains(err.Error(), "at least 2") {
		t.Errorf("expected error about minimum items, got: %v", err)
	}
}

func TestPublish(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/12345/media_publish") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		r.ParseForm()
		if r.Form.Get("creation_id") != "carousel-001" {
			t.Errorf("unexpected creation_id: %s", r.Form.Get("creation_id"))
		}

		json.NewEncoder(w).Encode(apiResponse{ID: "post-001"})
	}))
	defer server.Close()

	client := newTestClient(server)
	id, err := client.Publish(context.Background(), "carousel-001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "post-001" {
		t.Errorf("expected post-001, got %s", id)
	}
}

func TestContainerStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}

		json.NewEncoder(w).Encode(containerStatusResponse{
			ID:         "container-001",
			StatusCode: "FINISHED",
		})
	}))
	defer server.Close()

	client := newTestClient(server)
	status, err := client.ContainerStatus(context.Background(), "container-001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != "FINISHED" {
		t.Errorf("expected FINISHED, got %s", status)
	}
}

func TestWaitForContainer_Finished(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		status := "IN_PROGRESS"
		if callCount >= 2 {
			status = "FINISHED"
		}
		json.NewEncoder(w).Encode(containerStatusResponse{
			ID:         "container-001",
			StatusCode: status,
		})
	}))
	defer server.Close()

	client := newTestClient(server)
	// Override poll intervals for faster tests
	origInitial := initialPollInterval
	defer func() {
		// Note: can't actually override package constants in tests,
		// but WaitForContainer uses its own timing. For testing we use a short timeout.
	}()
	_ = origInitial

	err := client.WaitForContainer(context.Background(), "container-001", 30*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount < 2 {
		t.Errorf("expected at least 2 polls, got %d", callCount)
	}
}

func TestAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(apiResponse{
			Error: &apiErr{
				Message: "Invalid OAuth access token",
				Type:    "OAuthException",
				Code:    190,
			},
		})
	}))
	defer server.Close()

	client := newTestClient(server)
	_, err := client.CreateImageContainer(context.Background(), "https://example.com/photo.jpg", false)
	if err == nil {
		t.Fatal("expected error for invalid token")
	}
	if !strings.Contains(err.Error(), "OAuthException") {
		t.Errorf("expected OAuthException in error, got: %v", err)
	}
}

func TestCreateSingleImagePost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("image_url") != "https://example.com/photo.jpg" {
			t.Errorf("unexpected image_url")
		}
		if r.Form.Get("caption") != "Great photo!" {
			t.Errorf("unexpected caption")
		}
		if r.Form.Get("is_carousel_item") != "" {
			t.Errorf("single post should not have is_carousel_item")
		}

		json.NewEncoder(w).Encode(apiResponse{ID: "single-001"})
	}))
	defer server.Close()

	client := newTestClient(server)
	id, err := client.CreateSingleImagePost(context.Background(), "https://example.com/photo.jpg", "Great photo!")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "single-001" {
		t.Errorf("expected single-001, got %s", id)
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input    string
		limit    int
		expected string
	}{
		{"short", 10, "short"},
		{"this is a long string", 10, "this is a ..."},
		{"exact", 5, "exact"},
	}
	for _, tt := range tests {
		got := truncate(tt.input, tt.limit)
		if got != tt.expected {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.limit, got, tt.expected)
		}
	}
}
