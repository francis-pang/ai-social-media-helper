package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/fpang/gemini-media-cli/internal/jobs"
	"github.com/rs/zerolog/log"
)

// --- Publish Endpoints (DDR-040) ---

type publishJob struct {
	mu              sync.Mutex
	id              string
	sessionID       string
	groupID         string
	status          string // "pending", "creating_containers", "processing_videos", "creating_carousel", "publishing", "published", "error"
	phase           string // same as status — exposed to frontend for progress display
	totalItems      int
	completedItems  int
	containerIDs    []string
	instagramPostID string
	errMsg          string
	// Input data stored for the background goroutine.
	mediaKeys []string
	caption   string
}

var (
	publishJobsMu sync.Mutex
	publishJobs   = make(map[string]*publishJob)
)

func newPublishJob(sessionID, groupID string) *publishJob {
	publishJobsMu.Lock()
	defer publishJobsMu.Unlock()
	id := jobs.GenerateID("pub-")
	j := &publishJob{
		id:        id,
		sessionID: sessionID,
		groupID:   groupID,
		status:    "pending",
		phase:     "pending",
	}
	publishJobs[id] = j
	return j
}

func getPublishJob(id string) *publishJob {
	publishJobsMu.Lock()
	defer publishJobsMu.Unlock()
	return publishJobs[id]
}

func setPublishJobError(job *publishJob, msg string) {
	job.mu.Lock()
	defer job.mu.Unlock()
	job.status = "error"
	job.phase = "error"
	job.errMsg = msg
	log.Error().Str("job", job.id).Str("error", msg).Msg("Publish job failed")
}

// POST /api/publish/start
// Body: {"sessionId": "uuid", "groupId": "group-1", "keys": [...], "caption": "...", "hashtags": [...]}
func handlePublishStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if igClient == nil {
		httpError(w, http.StatusServiceUnavailable, "Instagram publishing is not configured — set INSTAGRAM_ACCESS_TOKEN and INSTAGRAM_USER_ID")
		return
	}

	var req struct {
		SessionID string   `json:"sessionId"`
		GroupID   string   `json:"groupId"`
		Keys      []string `json:"keys"`
		Caption   string   `json:"caption"`
		Hashtags  []string `json:"hashtags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := validateSessionID(req.SessionID); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.GroupID == "" {
		httpError(w, http.StatusBadRequest, "groupId is required")
		return
	}
	if len(req.Keys) == 0 {
		httpError(w, http.StatusBadRequest, "keys are required")
		return
	}
	for _, key := range req.Keys {
		if err := validateS3Key(key); err != nil {
			httpError(w, http.StatusBadRequest, fmt.Sprintf("invalid key: %s", key))
			return
		}
	}

	// Assemble full caption with hashtags
	fullCaption := req.Caption
	if len(req.Hashtags) > 0 {
		hashtagStrs := make([]string, len(req.Hashtags))
		for i, h := range req.Hashtags {
			if strings.HasPrefix(h, "#") {
				hashtagStrs[i] = h
			} else {
				hashtagStrs[i] = "#" + h
			}
		}
		fullCaption += "\n\n" + strings.Join(hashtagStrs, " ")
	}

	job := newPublishJob(req.SessionID, req.GroupID)
	job.mediaKeys = req.Keys
	job.caption = fullCaption

	go runPublishJob(job)

	respondJSON(w, http.StatusAccepted, map[string]string{
		"id": job.id,
	})
}

func handlePublishRoutes(w http.ResponseWriter, r *http.Request) {
	jobID, action, ok := jobs.ParseRoute(r.URL.Path, "/api/publish/", "pub-")
	if !ok {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	job := getPublishJob(jobID)
	if job == nil {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	switch action {
	case "status":
		handlePublishStatus(w, r, job)
	default:
		httpError(w, http.StatusNotFound, "not found")
	}
}

// GET /api/publish/{id}/status?sessionId=...
func handlePublishStatus(w http.ResponseWriter, r *http.Request, job *publishJob) {
	if r.Method != http.MethodGet {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Ownership check (DDR-028)
	if !jobs.CheckOwnership(r, job.sessionID) {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	job.mu.Lock()
	defer job.mu.Unlock()

	resp := map[string]interface{}{
		"id":     job.id,
		"status": job.status,
		"phase":  job.phase,
		"progress": map[string]int{
			"completed": job.completedItems,
			"total":     job.totalItems,
		},
	}
	if job.instagramPostID != "" {
		resp["instagramPostId"] = job.instagramPostID
	}
	if job.errMsg != "" {
		resp["error"] = job.errMsg
	}
	respondJSON(w, http.StatusOK, resp)
}

// runPublishJob executes the full Instagram publish flow in a background goroutine.
func runPublishJob(job *publishJob) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	logger := log.With().
		Str("jobId", job.id).
		Str("sessionId", job.sessionID).
		Str("groupId", job.groupID).
		Logger()

	mediaKeys := job.mediaKeys
	caption := job.caption

	logger.Info().Int("items", len(mediaKeys)).Msg("Starting Instagram publish job")

	job.mu.Lock()
	job.totalItems = len(mediaKeys)
	job.status = "creating_containers"
	job.phase = "creating_containers"
	job.mu.Unlock()

	// Step 1: Create media containers for each item.
	containerIDs := make([]string, 0, len(mediaKeys))
	videoContainerIDs := make([]string, 0)
	isCarousel := len(mediaKeys) > 1

	for i, key := range mediaKeys {
		// Generate presigned GET URL for Instagram to fetch the media.
		presignResult, err := presigner.PresignGetObject(ctx, &s3.GetObjectInput{
			Bucket: &mediaBucket,
			Key:    &key,
		}, s3.WithPresignExpires(1*time.Hour))
		if err != nil {
			setPublishJobError(job, fmt.Sprintf("failed to generate presigned URL for %s", key))
			return
		}

		mediaURL := presignResult.URL
		isVideo := isVideoKey(key)

		var containerID string
		if isCarousel {
			// Carousel item — no caption on individual items
			if isVideo {
				containerID, err = igClient.CreateVideoContainer(ctx, mediaURL, true)
			} else {
				containerID, err = igClient.CreateImageContainer(ctx, mediaURL, true)
			}
		} else {
			// Single item post — caption goes on the container
			if isVideo {
				containerID, err = igClient.CreateSingleReelPost(ctx, mediaURL, caption)
			} else {
				containerID, err = igClient.CreateSingleImagePost(ctx, mediaURL, caption)
			}
		}
		if err != nil {
			setPublishJobError(job, fmt.Sprintf("failed to create container for item %d: %v", i+1, err))
			return
		}

		containerIDs = append(containerIDs, containerID)
		if isVideo {
			videoContainerIDs = append(videoContainerIDs, containerID)
		}

		job.mu.Lock()
		job.completedItems = i + 1
		job.containerIDs = containerIDs
		job.mu.Unlock()

		logger.Debug().
			Str("containerID", containerID).
			Str("key", key).
			Bool("isVideo", isVideo).
			Int("item", i+1).
			Int("total", len(mediaKeys)).
			Msg("Container created")
	}

	// Step 2: Wait for video container processing (if any).
	if len(videoContainerIDs) > 0 {
		job.mu.Lock()
		job.status = "processing_videos"
		job.phase = "processing_videos"
		job.mu.Unlock()

		logger.Info().Int("videos", len(videoContainerIDs)).Msg("Waiting for video processing")

		for _, vid := range videoContainerIDs {
			if err := igClient.WaitForContainer(ctx, vid, 5*time.Minute); err != nil {
				setPublishJobError(job, fmt.Sprintf("video processing failed: %v", err))
				return
			}
		}
	}

	// Step 3: Create carousel container (if multi-item) or use the single container.
	var publishContainerID string
	if isCarousel {
		job.mu.Lock()
		job.status = "creating_carousel"
		job.phase = "creating_carousel"
		job.mu.Unlock()

		logger.Info().Int("children", len(containerIDs)).Msg("Creating carousel container")

		var err error
		publishContainerID, err = igClient.CreateCarouselContainer(ctx, containerIDs, caption)
		if err != nil {
			setPublishJobError(job, fmt.Sprintf("failed to create carousel: %v", err))
			return
		}
	} else {
		publishContainerID = containerIDs[0]
	}

	// Step 4: Publish!
	job.mu.Lock()
	job.status = "publishing"
	job.phase = "publishing"
	job.mu.Unlock()

	logger.Info().Str("containerId", publishContainerID).Msg("Publishing to Instagram")

	instagramPostID, err := igClient.Publish(ctx, publishContainerID)
	if err != nil {
		setPublishJobError(job, fmt.Sprintf("publish failed: %v", err))
		return
	}

	// Success!
	job.mu.Lock()
	job.status = "published"
	job.phase = "published"
	job.instagramPostID = instagramPostID
	job.mu.Unlock()

	logger.Info().
		Str("instagramPostId", instagramPostID).
		Int("items", len(mediaKeys)).
		Msg("Successfully published to Instagram")
}

// isVideoKey checks if an S3 key refers to a video file based on extension.
func isVideoKey(key string) bool {
	lower := strings.ToLower(key)
	for _, ext := range []string{".mp4", ".mov", ".avi", ".mkv", ".webm", ".m4v", ".3gp"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}
