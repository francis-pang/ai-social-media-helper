package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/fpang/gemini-media-cli/internal/jobs"
	"github.com/fpang/gemini-media-cli/internal/store"
	"github.com/rs/zerolog/log"
)

// --- Publish Endpoints (DDR-040, DDR-050: DynamoDB + async Worker Lambda) ---

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

	jobID := jobs.GenerateID("pub-")

	// Write pending job to DynamoDB (DDR-050).
	if sessionStore != nil {
		pendingJob := &store.PublishJob{
			ID:         jobID,
			GroupID:    req.GroupID,
			Status:     "pending",
			Phase:      "pending",
			TotalItems: len(req.Keys),
		}
		if err := sessionStore.PutPublishJob(context.Background(), req.SessionID, pendingJob); err != nil {
			log.Error().Err(err).Str("jobId", jobID).Msg("Failed to persist pending publish job")
			httpError(w, http.StatusInternalServerError, "failed to create job")
			return
		}
	}

	// Dispatch to Worker Lambda asynchronously (DDR-050).
	if err := invokeWorkerAsync(context.Background(), map[string]interface{}{
		"type":      "publish",
		"sessionId": req.SessionID,
		"jobId":     jobID,
		"groupId":   req.GroupID,
		"keys":      req.Keys,
		"caption":   fullCaption,
	}); err != nil {
		log.Error().Err(err).Str("jobId", jobID).Msg("Failed to invoke worker for publish")
		if sessionStore != nil {
			errJob := &store.PublishJob{ID: jobID, GroupID: req.GroupID, Status: "error", Phase: "error", Error: "failed to start processing"}
			sessionStore.PutPublishJob(context.Background(), req.SessionID, errJob)
		}
		httpError(w, http.StatusInternalServerError, "failed to start processing")
		return
	}

	respondJSON(w, http.StatusAccepted, map[string]string{
		"id": jobID,
	})
}

func handlePublishRoutes(w http.ResponseWriter, r *http.Request) {
	jobID, action, ok := jobs.ParseRoute(r.URL.Path, "/api/publish/", "pub-")
	if !ok {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	switch action {
	case "status":
		handlePublishStatus(w, r, jobID)
	default:
		httpError(w, http.StatusNotFound, "not found")
	}
}

// GET /api/publish/{id}/status?sessionId=...
func handlePublishStatus(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodGet {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	sessionID := r.URL.Query().Get("sessionId")
	if sessionID == "" {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	if sessionStore == nil {
		httpError(w, http.StatusServiceUnavailable, "store not configured")
		return
	}

	job, err := sessionStore.GetPublishJob(context.Background(), sessionID, jobID)
	if err != nil {
		log.Error().Err(err).Str("jobId", jobID).Msg("Failed to read publish job from DynamoDB")
		httpError(w, http.StatusInternalServerError, "failed to read job status")
		return
	}
	if job == nil {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	resp := map[string]interface{}{
		"id":     job.ID,
		"status": job.Status,
		"phase":  job.Phase,
		"progress": map[string]int{
			"completed": job.CompletedItems,
			"total":     job.TotalItems,
		},
	}
	if job.InstagramPostID != "" {
		resp["instagramPostId"] = job.InstagramPostID
	}
	if job.Error != "" {
		resp["error"] = job.Error
	}
	respondJSON(w, http.StatusOK, resp)
}
