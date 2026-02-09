package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/fpang/gemini-media-cli/internal/filehandler"
	"github.com/fpang/gemini-media-cli/internal/jobs"
	"github.com/fpang/gemini-media-cli/internal/store"
	"github.com/rs/zerolog/log"
)

// --- Enhancement Endpoints (DDR-031, DDR-050) ---

// POST /api/enhance/start
// Body: {"sessionId": "uuid", "keys": ["uuid/file1.jpg", ...]}
func handleEnhanceStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		SessionID string   `json:"sessionId"`
		Keys      []string `json:"keys"`
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

	// Separate photos and videos for the enhancement pipeline
	var photoKeys []string
	var videoKeys []string
	for _, key := range req.Keys {
		ext := strings.ToLower(filepath.Ext(key))
		if filehandler.IsImage(ext) {
			photoKeys = append(photoKeys, key)
		} else if filehandler.IsVideo(ext) {
			videoKeys = append(videoKeys, key)
		}
	}

	if len(photoKeys) == 0 && len(videoKeys) == 0 {
		httpError(w, http.StatusBadRequest, "no media files in the provided keys")
		return
	}

	jobID := jobs.GenerateID("enh-")

	// Write pending job to DynamoDB (DDR-050).
	if sessionStore != nil {
		pendingJob := &store.EnhancementJob{
			ID:         jobID,
			Status:     "pending",
			TotalCount: len(photoKeys) + len(videoKeys),
		}
		if err := sessionStore.PutEnhancementJob(context.Background(), req.SessionID, pendingJob); err != nil {
			log.Error().Err(err).Str("jobId", jobID).Msg("Failed to persist pending enhancement job")
			httpError(w, http.StatusInternalServerError, "failed to create job")
			return
		}
	}

	// Start Step Functions execution (DDR-050).
	if sfnClient != nil && enhancementSfnArn != "" {
		sfnInput, _ := json.Marshal(map[string]interface{}{
			"sessionId": req.SessionID,
			"jobId":     jobID,
			"photos":    photoKeys,
			"videos":    videoKeys,
		})
		_, err := sfnClient.StartExecution(context.Background(), &sfn.StartExecutionInput{
			StateMachineArn: aws.String(enhancementSfnArn),
			Input:           aws.String(string(sfnInput)),
			Name:            aws.String(jobID),
		})
		if err != nil {
			log.Error().Err(err).Str("jobId", jobID).Msg("Failed to start enhancement pipeline")
			if sessionStore != nil {
				errJob := &store.EnhancementJob{ID: jobID, Status: "error", Error: "failed to start processing pipeline"}
				sessionStore.PutEnhancementJob(context.Background(), req.SessionID, errJob)
			}
			httpError(w, http.StatusInternalServerError, "failed to start processing")
			return
		}

		log.Info().
			Str("jobId", jobID).
			Str("sessionId", req.SessionID).
			Int("photos", len(photoKeys)).
			Int("videos", len(videoKeys)).
			Str("sfnArn", enhancementSfnArn).
			Msg("Enhancement pipeline started via Step Functions")
	}

	respondJSON(w, http.StatusAccepted, map[string]string{
		"id": jobID,
	})
}

func handleEnhanceRoutes(w http.ResponseWriter, r *http.Request) {
	jobID, action, ok := jobs.ParseRoute(r.URL.Path, "/api/enhance/", "enh-")
	if !ok {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	switch action {
	case "results":
		handleEnhanceResults(w, r, jobID)
	case "feedback":
		handleEnhanceFeedback(w, r, jobID)
	default:
		httpError(w, http.StatusNotFound, "not found")
	}
}

// GET /api/enhance/{id}/results?sessionId=...
func handleEnhanceResults(w http.ResponseWriter, r *http.Request, jobID string) {
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

	job, err := sessionStore.GetEnhancementJob(context.Background(), sessionID, jobID)
	if err != nil {
		log.Error().Err(err).Str("jobId", jobID).Msg("Failed to read enhancement job from DynamoDB")
		httpError(w, http.StatusInternalServerError, "failed to read job status")
		return
	}
	if job == nil {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	resp := map[string]interface{}{
		"id":             job.ID,
		"status":         job.Status,
		"items":          job.Items,
		"totalCount":     job.TotalCount,
		"completedCount": job.CompletedCount,
	}
	if job.Error != "" {
		resp["error"] = job.Error
	}
	respondJSON(w, http.StatusOK, resp)
}

// POST /api/enhance/{id}/feedback
// Body: {"sessionId": "uuid", "key": "uuid/file.jpg", "feedback": "make it brighter"}
func handleEnhanceFeedback(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		SessionID string `json:"sessionId"`
		Key       string `json:"key"`
		Feedback  string `json:"feedback"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.SessionID == "" || req.Key == "" || req.Feedback == "" {
		httpError(w, http.StatusBadRequest, "sessionId, key, and feedback are required")
		return
	}

	// Dispatch enhancement feedback to Worker Lambda (DDR-050).
	if err := invokeWorkerAsync(context.Background(), map[string]interface{}{
		"type":      "enhancement-feedback",
		"sessionId": req.SessionID,
		"jobId":     jobID,
		"key":       req.Key,
		"feedback":  req.Feedback,
	}); err != nil {
		log.Error().Err(err).Str("jobId", jobID).Msg("Failed to invoke worker for enhancement feedback")
		httpError(w, http.StatusInternalServerError, "failed to start feedback processing")
		return
	}

	respondJSON(w, http.StatusAccepted, map[string]string{
		"status": "processing",
	})
}
