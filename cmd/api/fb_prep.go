package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/fpang/ai-social-media-helper/internal/jobs"
	"github.com/fpang/ai-social-media-helper/internal/store"
	"github.com/rs/zerolog/log"
)

// --- FB Prep Endpoints (DynamoDB + async Worker Lambda) ---

// POST /api/fb-prep/start
// Body: {"sessionId": "uuid", "mediaItems": [{"key": "uuid/file.jpg"}, ...], "economyMode": bool}
func handleFBPrepStart(w http.ResponseWriter, r *http.Request) {
	log.Debug().Str("method", r.Method).Str("path", r.URL.Path).Msg("Handler entry: handleFBPrepStart")

	if r.Method != http.MethodPost {
		log.Warn().Str("param", "method").Msg("Method not allowed")
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		SessionID   string `json:"sessionId"`
		MediaItems  []struct { Key string `json:"key"` } `json:"mediaItems"`
		EconomyMode bool   `json:"economyMode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Debug().Err(err).Msg("Request body decoding failed")
		log.Warn().Str("param", "body").Msg("Invalid request body")
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	log.Debug().Str("sessionId", req.SessionID).Int("mediaCount", len(req.MediaItems)).Bool("economyMode", req.EconomyMode).Msg("Request body decoded successfully")

	if err := validateSessionID(req.SessionID); err != nil {
		log.Debug().Err(err).Str("sessionId", req.SessionID).Msg("SessionId validation failed")
		log.Warn().Str("param", "sessionId").Msg("SessionId validation failed")
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.MediaItems) == 0 {
		log.Warn().Str("param", "mediaItems").Msg("Media items are required")
		httpError(w, http.StatusBadRequest, "media items are required")
		return
	}
	keys := make([]string, 0, len(req.MediaItems))
	for _, item := range req.MediaItems {
		if err := validateS3Key(item.Key); err != nil {
			log.Debug().Err(err).Str("key", item.Key).Msg("S3 key validation failed")
			log.Warn().Str("param", "mediaItems").Str("key", item.Key).Msg("Invalid S3 key")
			httpError(w, http.StatusBadRequest, fmt.Sprintf("invalid key: %s", item.Key))
			return
		}
		keys = append(keys, item.Key)
	}

	jobID := jobs.GenerateID("fb-")

	// Write pending job to DynamoDB.
	if sessionStore != nil {
		now := time.Now().UTC().Format(time.RFC3339)
		pendingJob := &store.FBPrepJob{
			ID:          jobID,
			Status:      "processing",
			EconomyMode: req.EconomyMode,
			MediaKeys:   keys,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := sessionStore.PutFBPrepJob(context.Background(), req.SessionID, pendingJob); err != nil {
			log.Error().Err(err).Str("jobId", jobID).Msg("Failed to persist pending FB prep job")
			httpError(w, http.StatusInternalServerError, "failed to create job")
			return
		}
	}

	// Dispatch to FB Prep Lambda asynchronously.
	payload := map[string]interface{}{
		"type":        "fb-prep",
		"sessionId":   req.SessionID,
		"jobId":      jobID,
		"mediaKeys":  keys,
		"economyMode": req.EconomyMode,
	}
	log.Info().
		Str("jobId", jobID).
		Str("sessionId", req.SessionID).
		Msg("Job dispatched to fb-prep-lambda")
	if err := invokeAsync(context.Background(), fbPrepLambdaArn, payload); err != nil {
		log.Error().Err(err).Str("jobId", jobID).Str("lambdaArn", fbPrepLambdaArn).Msg("Failed to invoke fb-prep-lambda")
		errDetail := fmt.Sprintf("failed to start processing: %v", err)
		if sessionStore != nil {
			errJob := &store.FBPrepJob{ID: jobID, Status: "error", Error: errDetail}
			sessionStore.PutFBPrepJob(context.Background(), req.SessionID, errJob)
		}
		httpError(w, http.StatusInternalServerError, errDetail)
		return
	}

	respondJSON(w, http.StatusAccepted, map[string]interface{}{
		"session_id": req.SessionID,
		"id":         jobID,
		"status":     "processing",
	})
}

func handleFBPrepRoutes(w http.ResponseWriter, r *http.Request) {
	jobID, action, ok := jobs.ParseRoute(r.URL.Path, "/api/fb-prep/", "fb-")
	if !ok {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	switch action {
	case "results":
		handleFBPrepResults(w, r, jobID)
	case "feedback":
		handleFBPrepFeedback(w, r, jobID)
	default:
		httpError(w, http.StatusNotFound, "not found")
	}
}

// GET /api/fb-prep/{id}/results?sessionId=...
func handleFBPrepResults(w http.ResponseWriter, r *http.Request, jobID string) {
	log.Debug().Str("method", r.Method).Str("path", r.URL.Path).Str("jobId", jobID).Msg("Handler entry: handleFBPrepResults")

	if r.Method != http.MethodGet {
		log.Warn().Str("param", "method").Msg("Method not allowed")
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	sessionID := r.URL.Query().Get("sessionId")
	if sessionID == "" {
		log.Warn().Str("param", "sessionId").Msg("SessionId is required")
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	if sessionStore == nil {
		httpError(w, http.StatusServiceUnavailable, "store not configured")
		return
	}

	job, err := sessionStore.GetFBPrepJob(context.Background(), sessionID, jobID)
	if err != nil {
		log.Error().Err(err).Str("jobId", jobID).Msg("Failed to read FB prep job from DynamoDB")
		httpError(w, http.StatusInternalServerError, "failed to read job status")
		return
	}
	if job == nil {
		log.Debug().Str("jobId", jobID).Str("sessionId", sessionID).Msg("FB prep job not found in DynamoDB")
		httpError(w, http.StatusNotFound, "not found")
		return
	}
	log.Debug().Str("jobId", jobID).Str("status", job.Status).Msg("FB prep job found in DynamoDB")

	resp := map[string]interface{}{
		"id":     job.ID,
		"status": job.Status,
	}
	if len(job.Items) > 0 {
		resp["items"] = job.Items
	}
	if job.Error != "" {
		resp["error"] = job.Error
	}
	respondJSON(w, http.StatusOK, resp)
}

// POST /api/fb-prep/{id}/feedback
// Body: {"sessionId": "uuid", "itemIndex": 0, "feedback": "make it shorter"}
func handleFBPrepFeedback(w http.ResponseWriter, r *http.Request, jobID string) {
	log.Debug().Str("method", r.Method).Str("path", r.URL.Path).Str("jobId", jobID).Msg("Handler entry: handleFBPrepFeedback")

	if r.Method != http.MethodPost {
		log.Warn().Str("param", "method").Msg("Method not allowed")
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		SessionID  string `json:"sessionId"`
		ItemIndex  int    `json:"itemIndex"`
		Feedback   string `json:"feedback"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Debug().Err(err).Msg("Request body decoding failed")
		log.Warn().Str("param", "body").Msg("Invalid request body")
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	log.Debug().Str("sessionId", req.SessionID).Int("itemIndex", req.ItemIndex).Int("feedbackLength", len(req.Feedback)).Msg("Request body decoded successfully")

	if req.SessionID == "" {
		log.Warn().Str("param", "sessionId").Msg("SessionId is required")
		httpError(w, http.StatusNotFound, "not found")
		return
	}
	if req.Feedback == "" {
		log.Warn().Str("param", "feedback").Msg("Feedback is required")
		httpError(w, http.StatusBadRequest, "feedback is required")
		return
	}

	// Verify job exists and is complete before accepting feedback
	if sessionStore != nil {
		job, err := sessionStore.GetFBPrepJob(context.Background(), req.SessionID, jobID)
		if err != nil || job == nil {
			httpError(w, http.StatusNotFound, "not found")
			return
		}
		if job.Status != "complete" {
			httpError(w, http.StatusBadRequest, "fb prep must be complete before providing feedback")
			return
		}
		if req.ItemIndex < 0 || req.ItemIndex >= len(job.Items) {
			httpError(w, http.StatusBadRequest, "invalid item index")
			return
		}
	}

	// Dispatch feedback processing to FB Prep Lambda (always real-time, not batch).
	payload := map[string]interface{}{
		"type":       "fb-prep-feedback",
		"sessionId":  req.SessionID,
		"jobId":      jobID,
		"itemIndex":  req.ItemIndex,
		"feedback":   req.Feedback,
	}
	log.Info().
		Str("jobId", jobID).
		Str("sessionId", req.SessionID).
		Int("itemIndex", req.ItemIndex).
		Int("feedbackLength", len(req.Feedback)).
		Msg("Job dispatched to fb-prep-lambda for feedback")
	if err := invokeAsync(context.Background(), fbPrepLambdaArn, payload); err != nil {
		log.Error().Err(err).Str("jobId", jobID).Msg("Failed to invoke fb-prep-lambda for feedback")
		httpError(w, http.StatusInternalServerError, "failed to start feedback processing")
		return
	}

	respondJSON(w, http.StatusAccepted, map[string]string{
		"status": "processing",
	})
}
