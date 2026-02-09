package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/fpang/gemini-media-cli/internal/chat"
	"github.com/fpang/gemini-media-cli/internal/jobs"
	"github.com/fpang/gemini-media-cli/internal/store"
	"github.com/rs/zerolog/log"
)

// --- Triage Endpoints (DDR-050: DynamoDB + async Worker Lambda) ---

// POST /api/triage/start
// Body: {"sessionId": "uuid", "model": "optional-model-name"}
func handleTriageStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		SessionID string `json:"sessionId"`
		Model     string `json:"model,omitempty"`
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

	model := chat.DefaultModelName
	if req.Model != "" {
		model = req.Model
	}

	jobID := jobs.GenerateID("triage-")

	// Write pending job to DynamoDB (DDR-050).
	if sessionStore != nil {
		pendingJob := &store.TriageJob{
			ID:     jobID,
			Status: "pending",
		}
		if err := sessionStore.PutTriageJob(context.Background(), req.SessionID, pendingJob); err != nil {
			log.Error().Err(err).Str("jobId", jobID).Msg("Failed to persist pending triage job")
			httpError(w, http.StatusInternalServerError, "failed to create job")
			return
		}
	}

	// Dispatch to Worker Lambda asynchronously (DDR-050).
	if err := invokeWorkerAsync(context.Background(), map[string]interface{}{
		"type":      "triage",
		"sessionId": req.SessionID,
		"jobId":     jobID,
		"model":     model,
	}); err != nil {
		log.Error().Err(err).Str("jobId", jobID).Msg("Failed to invoke worker for triage")
		if sessionStore != nil {
			errJob := &store.TriageJob{ID: jobID, Status: "error", Error: "failed to start processing"}
			sessionStore.PutTriageJob(context.Background(), req.SessionID, errJob)
		}
		httpError(w, http.StatusInternalServerError, "failed to start processing")
		return
	}

	log.Info().
		Str("jobId", jobID).
		Str("sessionId", req.SessionID).
		Str("model", model).
		Msg("Triage job dispatched to Worker Lambda")

	respondJSON(w, http.StatusAccepted, map[string]string{
		"id": jobID,
	})
}

// --- Triage Routes ---

func handleTriageRoutes(w http.ResponseWriter, r *http.Request) {
	jobID, action, ok := jobs.ParseRoute(r.URL.Path, "/api/triage/", "triage-")
	if !ok {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	switch action {
	case "results":
		handleTriageResults(w, r, jobID)
	case "confirm":
		handleTriageConfirm(w, r, jobID)
	default:
		httpError(w, http.StatusNotFound, "not found")
	}
}

// GET /api/triage/{id}/results?sessionId=...
func handleTriageResults(w http.ResponseWriter, r *http.Request, jobID string) {
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

	job, err := sessionStore.GetTriageJob(context.Background(), sessionID, jobID)
	if err != nil {
		log.Error().Err(err).Str("jobId", jobID).Msg("Failed to read triage job from DynamoDB")
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
		"keep":    job.Keep,
		"discard": job.Discard,
	}
	if job.Error != "" {
		resp["error"] = job.Error
	}
	respondJSON(w, http.StatusOK, resp)
}

// POST /api/triage/{id}/confirm
func handleTriageConfirm(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		SessionID  string   `json:"sessionId"`
		DeleteKeys []string `json:"deleteKeys"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.SessionID == "" {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	// Read the triage job from DynamoDB to validate delete keys
	if sessionStore == nil {
		httpError(w, http.StatusServiceUnavailable, "store not configured")
		return
	}
	job, err := sessionStore.GetTriageJob(context.Background(), req.SessionID, jobID)
	if err != nil || job == nil {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	// Build a set of valid discard keys
	validKeys := make(map[string]bool)
	for _, item := range job.Discard {
		validKeys[item.Key] = true
	}

	ctx := context.Background()
	var deleted int
	var errMsgs []string

	for _, key := range req.DeleteKeys {
		if !validKeys[key] {
			errMsgs = append(errMsgs, fmt.Sprintf("key not in triage results: %s", key))
			continue
		}
		_, err := s3Client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: &mediaBucket,
			Key:    &key,
		})
		if err != nil {
			errMsgs = append(errMsgs, fmt.Sprintf("failed to delete %s: %v", key, err))
			continue
		}
		deleted++
		log.Info().Str("key", key).Msg("Deleted S3 object")
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"deleted":        deleted,
		"errors":         errMsgs,
		"reclaimedBytes": 0,
	})
}
