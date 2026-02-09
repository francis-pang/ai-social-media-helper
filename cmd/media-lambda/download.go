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

// --- Download Endpoints (DDR-034, DDR-050: DynamoDB + async Worker Lambda) ---

// POST /api/download/start
// Body: {"sessionId": "uuid", "keys": ["uuid/enhanced/file1.jpg", ...], "groupLabel": "Tokyo Day 1"}
func handleDownloadStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		SessionID  string   `json:"sessionId"`
		Keys       []string `json:"keys"`
		GroupLabel string   `json:"groupLabel"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.SessionID == "" {
		httpError(w, http.StatusBadRequest, "sessionId is required")
		return
	}
	if err := validateSessionID(req.SessionID); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.Keys) == 0 {
		httpError(w, http.StatusBadRequest, "at least one key is required")
		return
	}

	// Validate all keys belong to the session
	for _, key := range req.Keys {
		if err := validateS3Key(key); err != nil {
			httpError(w, http.StatusBadRequest, fmt.Sprintf("invalid key: %s", err.Error()))
			return
		}
		if !strings.HasPrefix(key, req.SessionID+"/") {
			httpError(w, http.StatusBadRequest, "key does not belong to session")
			return
		}
	}

	jobID := jobs.GenerateID("dl-")

	// Write pending job to DynamoDB (DDR-050).
	if sessionStore != nil {
		pendingJob := &store.DownloadJob{
			ID:     jobID,
			Status: "pending",
		}
		if err := sessionStore.PutDownloadJob(context.Background(), req.SessionID, pendingJob); err != nil {
			log.Error().Err(err).Str("jobId", jobID).Msg("Failed to persist pending download job")
			httpError(w, http.StatusInternalServerError, "failed to create job")
			return
		}
	}

	// Dispatch to Worker Lambda asynchronously (DDR-050).
	if err := invokeWorkerAsync(context.Background(), map[string]interface{}{
		"type":       "download",
		"sessionId":  req.SessionID,
		"jobId":      jobID,
		"keys":       req.Keys,
		"groupLabel": req.GroupLabel,
	}); err != nil {
		log.Error().Err(err).Str("jobId", jobID).Msg("Failed to invoke worker for download")
		if sessionStore != nil {
			errJob := &store.DownloadJob{ID: jobID, Status: "error", Error: "failed to start processing"}
			sessionStore.PutDownloadJob(context.Background(), req.SessionID, errJob)
		}
		httpError(w, http.StatusInternalServerError, "failed to start processing")
		return
	}

	respondJSON(w, http.StatusAccepted, map[string]string{
		"id": jobID,
	})
}

func handleDownloadRoutes(w http.ResponseWriter, r *http.Request) {
	jobID, action, ok := jobs.ParseRoute(r.URL.Path, "/api/download/", "dl-")
	if !ok {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	switch action {
	case "results":
		handleDownloadResults(w, r, jobID)
	default:
		httpError(w, http.StatusNotFound, "not found")
	}
}

// GET /api/download/{id}/results?sessionId=...
func handleDownloadResults(w http.ResponseWriter, r *http.Request, jobID string) {
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

	job, err := sessionStore.GetDownloadJob(context.Background(), sessionID, jobID)
	if err != nil {
		log.Error().Err(err).Str("jobId", jobID).Msg("Failed to read download job from DynamoDB")
		httpError(w, http.StatusInternalServerError, "failed to read job status")
		return
	}
	if job == nil {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	resp := map[string]interface{}{
		"id":      job.ID,
		"status":  job.Status,
		"bundles": job.Bundles,
	}
	if job.Error != "" {
		resp["error"] = job.Error
	}
	respondJSON(w, http.StatusOK, resp)
}
