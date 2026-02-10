package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/fpang/gemini-media-cli/internal/jobs"
	"github.com/fpang/gemini-media-cli/internal/store"
	"github.com/rs/zerolog/log"
)

// --- Description Endpoints (DDR-036, DDR-050: DynamoDB + async Worker Lambda) ---

// POST /api/description/generate
// Body: {"sessionId": "uuid", "keys": ["uuid/enhanced/file1.jpg", ...], "groupLabel": "...", "tripContext": "..."}
func handleDescriptionGenerate(w http.ResponseWriter, r *http.Request) {
	log.Debug().Str("method", r.Method).Str("path", r.URL.Path).Msg("Handler entry: handleDescriptionGenerate")

	if r.Method != http.MethodPost {
		log.Warn().Str("param", "method").Msg("Method not allowed")
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		SessionID   string   `json:"sessionId"`
		Keys        []string `json:"keys"`
		GroupLabel  string   `json:"groupLabel"`
		TripContext string   `json:"tripContext"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Debug().Err(err).Msg("Request body decoding failed")
		log.Warn().Str("param", "body").Msg("Invalid request body")
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	log.Debug().Str("sessionId", req.SessionID).Str("groupLabel", req.GroupLabel).Int("keyCount", len(req.Keys)).Msg("Request body decoded successfully")

	if err := validateSessionID(req.SessionID); err != nil {
		log.Debug().Err(err).Str("sessionId", req.SessionID).Msg("SessionId validation failed")
		log.Warn().Str("param", "sessionId").Msg("SessionId validation failed")
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	log.Debug().Str("sessionId", req.SessionID).Msg("SessionId validation passed")
	if len(req.Keys) == 0 {
		log.Warn().Str("param", "keys").Msg("Keys are required")
		httpError(w, http.StatusBadRequest, "keys are required")
		return
	}
	for _, key := range req.Keys {
		if err := validateS3Key(key); err != nil {
			log.Debug().Err(err).Str("key", key).Msg("S3 key validation failed")
			log.Warn().Str("param", "keys").Str("key", key).Msg("Invalid S3 key")
			httpError(w, http.StatusBadRequest, fmt.Sprintf("invalid key: %s", key))
			return
		}
	}
	log.Debug().Int("keyCount", len(req.Keys)).Msg("All keys validated successfully")

	jobID := jobs.GenerateID("desc-")

	// Write pending job to DynamoDB (DDR-050).
	if sessionStore != nil {
		pendingJob := &store.DescriptionJob{
			ID:          jobID,
			Status:      "pending",
			GroupLabel:  req.GroupLabel,
			TripContext: req.TripContext,
			MediaKeys:   req.Keys,
		}
		if err := sessionStore.PutDescriptionJob(context.Background(), req.SessionID, pendingJob); err != nil {
			log.Error().Err(err).Str("jobId", jobID).Msg("Failed to persist pending description job")
			httpError(w, http.StatusInternalServerError, "failed to create job")
			return
		}
	}

	// Dispatch to Description Lambda asynchronously (DDR-053).
	payload := map[string]interface{}{
		"type":        "description",
		"sessionId":   req.SessionID,
		"jobId":       jobID,
		"keys":        req.Keys,
		"groupLabel":  req.GroupLabel,
		"tripContext": req.TripContext,
	}
	log.Info().
		Str("jobId", jobID).
		Str("sessionId", req.SessionID).
		Str("groupLabel", req.GroupLabel).
		Msg("Job dispatched to description-lambda")
	if err := invokeAsync(context.Background(), descriptionLambdaArn, payload); err != nil {
		log.Error().Err(err).Str("jobId", jobID).Str("lambdaArn", descriptionLambdaArn).Msg("Failed to invoke description-lambda")
		errDetail := fmt.Sprintf("failed to start processing: %v", err)
		if sessionStore != nil {
			errJob := &store.DescriptionJob{ID: jobID, Status: "error", Error: errDetail}
			sessionStore.PutDescriptionJob(context.Background(), req.SessionID, errJob)
		}
		httpError(w, http.StatusInternalServerError, errDetail)
		return
	}

	respondJSON(w, http.StatusAccepted, map[string]string{
		"id": jobID,
	})
}

func handleDescriptionRoutes(w http.ResponseWriter, r *http.Request) {
	jobID, action, ok := jobs.ParseRoute(r.URL.Path, "/api/description/", "desc-")
	if !ok {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	switch action {
	case "results":
		handleDescriptionResults(w, r, jobID)
	case "feedback":
		handleDescriptionFeedback(w, r, jobID)
	default:
		httpError(w, http.StatusNotFound, "not found")
	}
}

// GET /api/description/{id}/results?sessionId=...
func handleDescriptionResults(w http.ResponseWriter, r *http.Request, jobID string) {
	log.Debug().Str("method", r.Method).Str("path", r.URL.Path).Str("jobId", jobID).Msg("Handler entry: handleDescriptionResults")

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

	job, err := sessionStore.GetDescriptionJob(context.Background(), sessionID, jobID)
	if err != nil {
		log.Error().Err(err).Str("jobId", jobID).Msg("Failed to read description job from DynamoDB")
		httpError(w, http.StatusInternalServerError, "failed to read job status")
		return
	}
	if job == nil {
		log.Debug().Str("jobId", jobID).Str("sessionId", sessionID).Msg("Description job not found in DynamoDB")
		httpError(w, http.StatusNotFound, "not found")
		return
	}
	log.Debug().Str("jobId", jobID).Str("status", job.Status).Msg("Description job found in DynamoDB")

	resp := map[string]interface{}{
		"id":            job.ID,
		"status":        job.Status,
		"feedbackRound": len(job.History),
	}
	if job.Caption != "" {
		resp["caption"] = job.Caption
		resp["hashtags"] = job.Hashtags
		resp["locationTag"] = job.LocationTag
	}
	if job.Error != "" {
		resp["error"] = job.Error
	}
	respondJSON(w, http.StatusOK, resp)
}

// POST /api/description/{id}/feedback
// Body: {"sessionId": "uuid", "feedback": "make it shorter"}
func handleDescriptionFeedback(w http.ResponseWriter, r *http.Request, jobID string) {
	log.Debug().Str("method", r.Method).Str("path", r.URL.Path).Str("jobId", jobID).Msg("Handler entry: handleDescriptionFeedback")

	if r.Method != http.MethodPost {
		log.Warn().Str("param", "method").Msg("Method not allowed")
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		SessionID string `json:"sessionId"`
		Feedback  string `json:"feedback"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Debug().Err(err).Msg("Request body decoding failed")
		log.Warn().Str("param", "body").Msg("Invalid request body")
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	log.Debug().Str("sessionId", req.SessionID).Int("feedbackLength", len(req.Feedback)).Msg("Request body decoded successfully")

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
		job, err := sessionStore.GetDescriptionJob(context.Background(), req.SessionID, jobID)
		if err != nil || job == nil {
			httpError(w, http.StatusNotFound, "not found")
			return
		}
		if job.Status != "complete" {
			httpError(w, http.StatusBadRequest, "description must be complete before providing feedback")
			return
		}

		// Mark as processing in DynamoDB
		job.Status = "processing"
		sessionStore.PutDescriptionJob(context.Background(), req.SessionID, job)
	}

	// Dispatch feedback processing to Description Lambda (DDR-053).
	payload := map[string]interface{}{
		"type":      "description-feedback",
		"sessionId": req.SessionID,
		"jobId":     jobID,
		"feedback":  req.Feedback,
	}
	log.Info().
		Str("jobId", jobID).
		Str("sessionId", req.SessionID).
		Int("feedbackLength", len(req.Feedback)).
		Msg("Job dispatched to description-lambda")
	if err := invokeAsync(context.Background(), descriptionLambdaArn, payload); err != nil {
		log.Error().Err(err).Str("jobId", jobID).Msg("Failed to invoke description-lambda for feedback")
		httpError(w, http.StatusInternalServerError, "failed to start feedback processing")
		return
	}

	respondJSON(w, http.StatusAccepted, map[string]string{
		"status": "processing",
	})
}
