package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/fpang/gemini-media-cli/internal/chat"
	"github.com/fpang/gemini-media-cli/internal/jobs"
	"github.com/fpang/gemini-media-cli/internal/store"
	"github.com/rs/zerolog/log"
)

// --- Triage Endpoints (DDR-050, DDR-052: DynamoDB + Step Functions) ---

// POST /api/triage/start
// Body: {"sessionId": "uuid", "model": "optional-model-name"}
func handleTriageStart(w http.ResponseWriter, r *http.Request) {
	log.Debug().Str("method", r.Method).Str("path", r.URL.Path).Msg("Handler entry: handleTriageStart")

	if r.Method != http.MethodPost {
		log.Warn().Str("param", "method").Msg("Method not allowed")
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		SessionID string `json:"sessionId"`
		Model     string `json:"model,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Debug().Err(err).Msg("Request body decoding failed")
		log.Warn().Str("param", "body").Msg("Invalid request body")
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	log.Debug().Str("sessionId", req.SessionID).Str("model", req.Model).Msg("Request body decoded successfully")

	if req.SessionID == "" {
		log.Warn().Str("param", "sessionId").Msg("SessionId is required")
		httpError(w, http.StatusBadRequest, "sessionId is required")
		return
	}
	if err := validateSessionID(req.SessionID); err != nil {
		log.Debug().Err(err).Str("sessionId", req.SessionID).Msg("SessionId validation failed")
		log.Warn().Str("param", "sessionId").Msg("SessionId validation failed")
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	log.Debug().Str("sessionId", req.SessionID).Msg("SessionId validation passed")

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

	// Dispatch to Triage Pipeline Step Functions (DDR-052).
	if sfnClient != nil && triageSfnArn != "" {
		sfnInput, _ := json.Marshal(map[string]interface{}{
			"type":      "triage-prepare",
			"sessionId": req.SessionID,
			"jobId":     jobID,
			"model":     model,
		})
		log.Info().
			Str("jobId", jobID).
			Str("sessionId", req.SessionID).
			Str("model", model).
			Str("sfnArn", triageSfnArn).
			Msg("Job dispatched to Triage Pipeline")
		_, err := sfnClient.StartExecution(context.Background(), &sfn.StartExecutionInput{
			StateMachineArn: aws.String(triageSfnArn),
			Input:           aws.String(string(sfnInput)),
			Name:            aws.String(jobID),
		})
		if err != nil {
			log.Error().Err(err).Str("jobId", jobID).Msg("Failed to start triage pipeline")
			if sessionStore != nil {
				errJob := &store.TriageJob{ID: jobID, Status: "error", Error: "failed to start processing"}
				sessionStore.PutTriageJob(context.Background(), req.SessionID, errJob)
			}
			httpError(w, http.StatusInternalServerError, "failed to start processing")
			return
		}
	}

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
	log.Debug().Str("method", r.Method).Str("path", r.URL.Path).Str("jobId", jobID).Msg("Handler entry: handleTriageResults")

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

	job, err := sessionStore.GetTriageJob(context.Background(), sessionID, jobID)
	if err != nil {
		log.Error().Err(err).Str("jobId", jobID).Msg("Failed to read triage job from DynamoDB")
		httpError(w, http.StatusInternalServerError, "failed to read job status")
		return
	}
	if job == nil {
		log.Debug().Str("jobId", jobID).Str("sessionId", sessionID).Msg("Triage job not found in DynamoDB")
		httpError(w, http.StatusNotFound, "not found")
		return
	}
	log.Debug().Str("jobId", jobID).Str("status", job.Status).Msg("Triage job found in DynamoDB")

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
	log.Debug().Str("method", r.Method).Str("path", r.URL.Path).Str("jobId", jobID).Msg("Handler entry: handleTriageConfirm")

	if r.Method != http.MethodPost {
		log.Warn().Str("param", "method").Msg("Method not allowed")
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		SessionID  string   `json:"sessionId"`
		DeleteKeys []string `json:"deleteKeys"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Debug().Err(err).Msg("Request body decoding failed")
		log.Warn().Str("param", "body").Msg("Invalid request body")
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	log.Debug().Int("deleteKeysCount", len(req.DeleteKeys)).Msg("Request body decoded successfully")

	if req.SessionID == "" {
		log.Warn().Str("param", "sessionId").Msg("SessionId is required")
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

	log.Info().Int("deleted", deleted).Int("totalRequested", len(req.DeleteKeys)).Msg("Triage confirm completed")

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"deleted":        deleted,
		"errors":         errMsgs,
		"reclaimedBytes": 0,
	})
}
