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
	log.Debug().Str("method", r.Method).Str("path", r.URL.Path).Msg("Handler entry: handleEnhanceStart")

	if r.Method != http.MethodPost {
		log.Warn().Str("param", "method").Msg("Method not allowed")
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		SessionID string   `json:"sessionId"`
		Keys      []string `json:"keys"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Debug().Err(err).Msg("Request body decoding failed")
		log.Warn().Str("param", "body").Msg("Invalid request body")
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	log.Debug().Str("sessionId", req.SessionID).Int("keyCount", len(req.Keys)).Msg("Request body decoded successfully")

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
	if len(req.Keys) == 0 {
		log.Warn().Str("param", "keys").Msg("At least one key is required")
		httpError(w, http.StatusBadRequest, "at least one key is required")
		return
	}

	// Validate all keys belong to the session
	for _, key := range req.Keys {
		if err := validateS3Key(key); err != nil {
			log.Debug().Err(err).Str("key", key).Msg("S3 key validation failed")
			log.Warn().Str("param", "keys").Str("key", key).Msg("Invalid S3 key")
			httpError(w, http.StatusBadRequest, fmt.Sprintf("invalid key: %s", err.Error()))
			return
		}
		if !strings.HasPrefix(key, req.SessionID+"/") {
			log.Debug().Str("key", key).Str("sessionId", req.SessionID).Msg("Key does not belong to session")
			log.Warn().Str("param", "keys").Str("key", key).Msg("Key does not belong to session")
			httpError(w, http.StatusBadRequest, "key does not belong to session")
			return
		}
	}
	log.Debug().Int("keyCount", len(req.Keys)).Msg("All keys validated successfully")

	// Separate photos and videos for the enhancement pipeline.
	// Use initialized slices (not nil) so JSON marshal produces [] not null,
	// which Step Functions Map states require for ItemsPath.
	photoKeys := make([]string, 0)
	videoKeys := make([]string, 0)
	for _, key := range req.Keys {
		ext := strings.ToLower(filepath.Ext(key))
		if filehandler.IsImage(ext) {
			photoKeys = append(photoKeys, key)
		} else if filehandler.IsVideo(ext) {
			videoKeys = append(videoKeys, key)
		}
	}
	log.Debug().Int("photoCount", len(photoKeys)).Int("videoCount", len(videoKeys)).Msg("Media separated into photos and videos")

	if len(photoKeys) == 0 && len(videoKeys) == 0 {
		log.Warn().Str("param", "keys").Msg("No media files in the provided keys")
		httpError(w, http.StatusBadRequest, "no media files in the provided keys")
		return
	}

	jobID := jobs.GenerateID("enh-")

	// Write pending job to DynamoDB (DDR-050).
	if sessionStore != nil {
		// Pre-populate Items so the enhance-lambda can update by index.
		allKeys := append(photoKeys, videoKeys...)
		items := make([]store.EnhancementItem, len(allKeys))
		for i, k := range allKeys {
			items[i] = store.EnhancementItem{
				Key:         k,
				OriginalKey: k,
				Filename:    filepath.Base(k),
				Phase:       "pending",
			}
		}
		pendingJob := &store.EnhancementJob{
			ID:         jobID,
			Status:     "pending",
			TotalCount: len(photoKeys) + len(videoKeys),
			Items:      items,
		}
		if err := sessionStore.PutEnhancementJob(context.Background(), req.SessionID, pendingJob); err != nil {
			log.Error().Err(err).Str("jobId", jobID).Msg("Failed to persist pending enhancement job")
			httpError(w, http.StatusInternalServerError, "failed to create job")
			return
		}
	}

	// Start Step Functions execution (DDR-050).
	if sfnClient == nil || enhancementSfnArn == "" {
		log.Error().Str("jobId", jobID).Msg("Enhancement pipeline not configured — cannot process")
		errDetail := "enhancement processing is not available (pipeline not configured)"
		if sessionStore != nil {
			errJob := &store.EnhancementJob{ID: jobID, Status: "error", Error: errDetail}
			sessionStore.PutEnhancementJob(context.Background(), req.SessionID, errJob)
		}
		httpError(w, http.StatusServiceUnavailable, errDetail)
		return
	}
	sfnInput, _ := json.Marshal(map[string]interface{}{
		"sessionId": req.SessionID,
		"jobId":     jobID,
		"photos":    photoKeys,
		"videos":    videoKeys,
	})
	log.Info().
		Str("jobId", jobID).
		Str("sessionId", req.SessionID).
		Int("photos", len(photoKeys)).
		Int("videos", len(videoKeys)).
		Str("sfnArn", enhancementSfnArn).
		Msg("Job dispatched")
	_, err := sfnClient.StartExecution(context.Background(), &sfn.StartExecutionInput{
		StateMachineArn: aws.String(enhancementSfnArn),
		Input:           aws.String(string(sfnInput)),
		Name:            aws.String(jobID),
	})
	if err != nil {
		log.Error().Err(err).Str("jobId", jobID).Str("sfnArn", enhancementSfnArn).Msg("Failed to start enhancement pipeline")
		errDetail := fmt.Sprintf("failed to start processing: %v", err)
		if sessionStore != nil {
			errJob := &store.EnhancementJob{ID: jobID, Status: "error", Error: errDetail}
			sessionStore.PutEnhancementJob(context.Background(), req.SessionID, errJob)
		}
		httpError(w, http.StatusInternalServerError, errDetail)
		return
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
	log.Debug().Str("method", r.Method).Str("path", r.URL.Path).Str("jobId", jobID).Msg("Handler entry: handleEnhanceResults")

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

	job, err := sessionStore.GetEnhancementJob(context.Background(), sessionID, jobID)
	if err != nil {
		log.Error().Err(err).Str("jobId", jobID).Msg("Failed to read enhancement job from DynamoDB")
		httpError(w, http.StatusInternalServerError, "failed to read job status")
		return
	}
	if job == nil {
		log.Debug().Str("jobId", jobID).Str("sessionId", sessionID).Msg("Enhancement job not found in DynamoDB")
		httpError(w, http.StatusNotFound, "not found")
		return
	}
	log.Debug().Str("jobId", jobID).Str("status", job.Status).Msg("Enhancement job found in DynamoDB")

	// Self-healing reconciliation: count items where Phase != "pending" and
	// compare with CompletedCount. Fixes any counter drift from past races.
	trueCompleted := 0
	for _, item := range job.Items {
		if item.Phase != "" && item.Phase != "pending" {
			trueCompleted++
		}
	}
	if trueCompleted != job.CompletedCount {
		log.Warn().
			Str("jobId", jobID).Str("sessionId", sessionID).
			Int("storedCount", job.CompletedCount).Int("trueCount", trueCompleted).
			Msg("Enhancement completedCount mismatch — reconciling")
		job.CompletedCount = trueCompleted
	}
	if trueCompleted >= job.TotalCount && job.Status != "complete" {
		log.Warn().
			Str("jobId", jobID).Str("sessionId", sessionID).
			Int("trueCompleted", trueCompleted).Int("totalCount", job.TotalCount).
			Msg("All items done but status not complete — reconciling")
		job.Status = "complete"
		if err := sessionStore.UpdateEnhancementStatus(context.Background(), sessionID, jobID, "complete"); err != nil {
			log.Warn().Err(err).Msg("Failed to reconcile enhancement status")
		}
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
	log.Debug().Str("method", r.Method).Str("path", r.URL.Path).Str("jobId", jobID).Msg("Handler entry: handleEnhanceFeedback")

	if r.Method != http.MethodPost {
		log.Warn().Str("param", "method").Msg("Method not allowed")
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		SessionID string `json:"sessionId"`
		Key       string `json:"key"`
		Feedback  string `json:"feedback"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Debug().Err(err).Msg("Request body decoding failed")
		log.Warn().Str("param", "body").Msg("Invalid request body")
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	log.Debug().Str("sessionId", req.SessionID).Str("key", req.Key).Int("feedbackLength", len(req.Feedback)).Msg("Request body decoded successfully")

	if req.SessionID == "" || req.Key == "" || req.Feedback == "" {
		log.Warn().Str("param", "sessionId/key/feedback").Msg("SessionId, key, and feedback are required")
		httpError(w, http.StatusBadRequest, "sessionId, key, and feedback are required")
		return
	}

	// Dispatch enhancement feedback to Enhance Lambda (DDR-053).
	payload := map[string]interface{}{
		"type":      "enhancement-feedback",
		"sessionId": req.SessionID,
		"jobId":     jobID,
		"key":       req.Key,
		"feedback":  req.Feedback,
	}
	log.Info().
		Str("jobId", jobID).
		Str("sessionId", req.SessionID).
		Str("key", req.Key).
		Msg("Job dispatched to enhance-lambda")
	if err := invokeAsync(context.Background(), enhanceLambdaArn, payload); err != nil {
		log.Error().Err(err).Str("jobId", jobID).Msg("Failed to invoke enhance-lambda for feedback")
		httpError(w, http.StatusInternalServerError, "failed to start feedback processing")
		return
	}

	respondJSON(w, http.StatusAccepted, map[string]string{
		"status": "processing",
	})
}
