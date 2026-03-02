package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"os"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/fpang/ai-social-media-helper/internal/ai"
	"github.com/fpang/ai-social-media-helper/internal/jobs"
	"github.com/fpang/ai-social-media-helper/internal/store"
	"github.com/rs/zerolog/log"
)

// --- Triage Endpoints (DDR-050, DDR-052: DynamoDB + Step Functions) ---

// POST /api/triage/init
// Body: {"sessionId": "uuid", "expectedFileCount": 36, "model": "optional-model-name"}
// Returns: {"id": "triage-xxx", "sessionId": "uuid"}
func handleTriageInit(w http.ResponseWriter, r *http.Request) {
	log.Debug().Str("method", r.Method).Str("path", r.URL.Path).Msg("Handler entry: handleTriageInit")

	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		SessionID         string `json:"sessionId"`
		ExpectedFileCount int    `json:"expectedFileCount"`
		Model             string `json:"model,omitempty"`
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
	if req.ExpectedFileCount <= 0 {
		httpError(w, http.StatusBadRequest, "expectedFileCount must be > 0")
		return
	}

	// Risk 15: Verify or establish session ownership before any processing.
	if !ensureSessionOwner(w, r, req.SessionID) {
		return
	}

	model := ai.DefaultModelName
	if req.Model != "" {
		model = req.Model
	}

	jobID := jobs.GenerateID("triage-")

	// DDR-067: Write pending job to DynamoDB only — SF execution is deferred to
	// handleTriageFinalize so the 30-min timeout starts after uploads complete.
	if sessionStore != nil {
		pendingJob := &store.TriageJob{
			ID:                jobID,
			Status:            "pending",
			Model:             model,
			ExpectedFileCount: req.ExpectedFileCount,
		}
		if err := sessionStore.PutTriageJob(context.Background(), req.SessionID, pendingJob); err != nil {
			log.Error().Err(err).Str("jobId", jobID).Msg("Failed to persist pending triage job")
			httpError(w, http.StatusInternalServerError, "failed to create job")
			return
		}
	}

	log.Info().
		Str("jobId", jobID).
		Str("sessionId", req.SessionID).
		Int("expectedFileCount", req.ExpectedFileCount).
		Msg("Triage init: DDB job created, SF deferred to finalize (DDR-067)")

	respondJSON(w, http.StatusAccepted, map[string]string{
		"id":        jobID,
		"sessionId": req.SessionID,
	})
}

// POST /api/triage/finalize (DDR-067)
// Body: {"sessionId": "uuid", "jobId": "triage-xxx"}
// Starts the Step Functions execution after all uploads are complete.
func handleTriageFinalize(w http.ResponseWriter, r *http.Request) {
	log.Debug().Str("method", r.Method).Str("path", r.URL.Path).Msg("Handler entry: handleTriageFinalize")

	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		SessionID string `json:"sessionId"`
		JobID     string `json:"jobId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.SessionID == "" || req.JobID == "" {
		httpError(w, http.StatusBadRequest, "sessionId and jobId are required")
		return
	}
	if err := validateSessionID(req.SessionID); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}

	if !ensureSessionOwner(w, r, req.SessionID) {
		return
	}

	// Read the existing triage job to get model and expectedFileCount
	if sessionStore == nil {
		httpError(w, http.StatusServiceUnavailable, "store not configured")
		return
	}
	job, err := sessionStore.GetTriageJob(context.Background(), req.SessionID, req.JobID)
	if err != nil {
		log.Error().Err(err).Str("jobId", req.JobID).Msg("Failed to read triage job")
		httpError(w, http.StatusInternalServerError, "failed to read job")
		return
	}
	if job == nil {
		httpError(w, http.StatusNotFound, "triage job not found")
		return
	}

	// Compute unique SFN execution name: for retries (status=error), append -r<N>
	var executionName string
	if job.Status == "error" {
		retryN := job.RetryCount + 1
		executionName = req.JobID + "-r" + strconv.Itoa(retryN)
		// Reset job for retry: status=processing, clear error, increment retry count
		job.RetryCount = retryN
		job.Status = "processing"
		job.Error = ""
		if err := sessionStore.PutTriageJob(context.Background(), req.SessionID, job); err != nil {
			log.Error().Err(err).Str("jobId", req.JobID).Msg("Failed to update job for retry")
			httpError(w, http.StatusInternalServerError, "failed to prepare retry")
			return
		}
	} else {
		executionName = req.JobID
	}

	model := job.Model
	if model == "" {
		model = ai.DefaultModelName
	}

	// Start TriagePipeline Step Function — timeout starts NOW (DDR-067)
	if sfnClient == nil || triageSfnArn == "" {
		log.Error().Str("jobId", req.JobID).Msg("Triage pipeline not configured")
		httpError(w, http.StatusServiceUnavailable, "triage processing is not available")
		return
	}

	sfnInput, _ := json.Marshal(map[string]interface{}{
		"type":              "triage-init-session",
		"sessionId":         req.SessionID,
		"jobId":             req.JobID,
		"model":             model,
		"expectedFileCount": job.ExpectedFileCount,
	})
	_, err = sfnClient.StartExecution(context.Background(), &sfn.StartExecutionInput{
		StateMachineArn: aws.String(triageSfnArn),
		Input:           aws.String(string(sfnInput)),
		Name:            aws.String(executionName),
	})
	if err != nil {
		log.Error().Err(err).Str("jobId", req.JobID).Msg("Failed to start triage pipeline")
		httpError(w, http.StatusInternalServerError, fmt.Sprintf("failed to start processing: %v", err))
		return
	}

	log.Info().
		Str("jobId", req.JobID).
		Str("sessionId", req.SessionID).
		Int("expectedFileCount", job.ExpectedFileCount).
		Msg("Triage finalize: SFN started after uploads complete (DDR-067)")

	respondJSON(w, http.StatusOK, map[string]string{
		"jobId":     req.JobID,
		"sessionId": req.SessionID,
	})
}

// POST /api/triage/update-files
// Body: {"sessionId": "uuid", "jobId": "triage-xxx", "expectedFileCount": 42}
func handleTriageUpdateFiles(w http.ResponseWriter, r *http.Request) {
	log.Debug().Str("method", r.Method).Str("path", r.URL.Path).Msg("Handler entry: handleTriageUpdateFiles")

	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		SessionID         string `json:"sessionId"`
		JobID             string `json:"jobId"`
		ExpectedFileCount int    `json:"expectedFileCount"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.SessionID == "" || req.JobID == "" || req.ExpectedFileCount <= 0 {
		httpError(w, http.StatusBadRequest, "sessionId, jobId, and expectedFileCount > 0 are required")
		return
	}

	if sessionStore != nil {
		if err := sessionStore.UpdateTriageExpectedCount(context.Background(), req.SessionID, req.JobID, req.ExpectedFileCount); err != nil {
			log.Error().Err(err).Msg("Failed to update expectedFileCount")
			httpError(w, http.StatusInternalServerError, "failed to update file count")
			return
		}
	}

	log.Info().
		Str("sessionId", req.SessionID).
		Str("jobId", req.JobID).
		Int("expectedFileCount", req.ExpectedFileCount).
		Msg("Triage file count updated (DDR-061)")

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"expectedFileCount": req.ExpectedFileCount,
	})
}

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

	// Risk 15: Verify session ownership.
	if !ensureSessionOwner(w, r, req.SessionID) {
		return
	}

	model := ai.DefaultModelName
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
	if sfnClient == nil || triageSfnArn == "" {
		log.Error().Str("jobId", jobID).Msg("Triage pipeline not configured — cannot process")
		errDetail := "triage processing is not available (pipeline not configured)"
		if sessionStore != nil {
			errJob := &store.TriageJob{ID: jobID, Status: "error", Error: errDetail}
			sessionStore.PutTriageJob(context.Background(), req.SessionID, errJob)
		}
		httpError(w, http.StatusServiceUnavailable, errDetail)
		return
	}
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
		log.Error().Err(err).Str("jobId", jobID).Str("sfnArn", triageSfnArn).Msg("Failed to start triage pipeline")
		errDetail := fmt.Sprintf("failed to start processing: %v", err)
		if sessionStore != nil {
			errJob := &store.TriageJob{ID: jobID, Status: "error", Error: errDetail}
			sessionStore.PutTriageJob(context.Background(), req.SessionID, errJob)
		}
		httpError(w, http.StatusInternalServerError, errDetail)
		return
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
	case "logs":
		handleTriageLogs(w, r, jobID)
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

	// Ensure arrays are never null in JSON (Go nil slices marshal as null).
	keepItems := job.Keep
	if keepItems == nil {
		keepItems = []store.TriageItem{}
	}
	discardItems := job.Discard
	if discardItems == nil {
		discardItems = []store.TriageItem{}
	}
	resp := map[string]interface{}{
		"id":      job.ID,
		"status":  job.Status,
		"keep":    keepItems,
		"discard": discardItems,
	}
	if job.Phase != "" {
		resp["phase"] = job.Phase
	}
	if job.TotalFiles > 0 {
		resp["totalFiles"] = job.TotalFiles
	}
	if job.UploadedFiles > 0 {
		resp["uploadedFiles"] = job.UploadedFiles
	}
	if job.TriageBatch > 0 {
		resp["triageBatch"] = job.TriageBatch
	}
	if job.TriageBatchTotal > 0 {
		resp["triageBatchTotal"] = job.TriageBatchTotal
	}
	if job.Error != "" {
		resp["error"] = job.Error
	}

	// DDR-061, DDR-063: Include per-file statuses during pending and processing phases
	if (job.Status == "pending" || job.Status == "processing") && fileProcessStore != nil {
		fileResults, err := fileProcessStore.GetFileResults(context.Background(), sessionID, jobID)
		if err == nil && len(fileResults) > 0 {
			fileStatuses := make([]map[string]interface{}, 0, len(fileResults))
			for _, fr := range fileResults {
				status := map[string]interface{}{
					"key":       fr.OriginalKey,
					"filename":  fr.Filename,
					"status":    fr.Status,
					"converted": fr.Converted,
				}
				if fr.ThumbnailKey != "" {
					status["thumbnailUrl"] = fmt.Sprintf("/api/media/thumbnail?key=%s", fr.ThumbnailKey)
				}
				if fr.Error != "" {
					status["error"] = fr.Error
				}
				fileStatuses = append(fileStatuses, status)
			}
			resp["fileStatuses"] = fileStatuses
			resp["expectedFileCount"] = job.ExpectedFileCount
			resp["processedCount"] = job.ProcessedCount
		}
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
	errMsgs := make([]string, 0)

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

	// DDR-059: Clean up all remaining S3 artifacts for this session (thumbnails,
	// compressed videos, any stragglers). Best-effort in a goroutine — same
	// pattern as session invalidation (DDR-037).
	go cleanupS3Prefix(req.SessionID, "")

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"deleted":        deleted,
		"errors":         errMsgs,
		"reclaimedBytes": 0,
	})
}

// GET /api/triage/{id}/logs?sessionId=...&since=...
func handleTriageLogs(w http.ResponseWriter, r *http.Request, _ string) {
	if r.Method != http.MethodGet {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	sessionID := r.URL.Query().Get("sessionId")
	if sessionID == "" {
		httpError(w, http.StatusBadRequest, "sessionId is required")
		return
	}

	if !ensureSessionOwner(w, r, sessionID) {
		return
	}

	sinceStr := r.URL.Query().Get("since")
	sinceMs := int64(0)
	if sinceStr != "" {
		parsed, err := strconv.ParseInt(sinceStr, 10, 64)
		if err == nil {
			sinceMs = parsed
		}
	}

	logGroupName := os.Getenv("TRIAGE_LOG_GROUP_NAME")
	if logGroupName == "" {
		respondJSON(w, http.StatusOK, map[string]interface{}{"entries": []interface{}{}, "nextSince": sinceMs})
		return
	}

	if cwlClient == nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{"entries": []interface{}{}, "nextSince": sinceMs})
		return
	}

	filterPattern := fmt.Sprintf("\"%s\"", sessionID)
	input := &cloudwatchlogs.FilterLogEventsInput{
		LogGroupName:  &logGroupName,
		FilterPattern: &filterPattern,
		Limit:         aws.Int32(50),
	}
	if sinceMs > 0 {
		input.StartTime = &sinceMs
	}

	result, err := cwlClient.FilterLogEvents(context.Background(), input)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to query CloudWatch Logs for triage")
		respondJSON(w, http.StatusOK, map[string]interface{}{"entries": []interface{}{}, "nextSince": sinceMs})
		return
	}

	type logEntry struct {
		Timestamp int64  `json:"timestamp"`
		Message   string `json:"message"`
	}

	entries := make([]logEntry, 0, len(result.Events))
	var maxTimestamp int64
	for _, evt := range result.Events {
		ts := int64(0)
		if evt.Timestamp != nil {
			ts = *evt.Timestamp
		}
		msg := ""
		if evt.Message != nil {
			msg = *evt.Message
		}
		entries = append(entries, logEntry{Timestamp: ts, Message: msg})
		if ts > maxTimestamp {
			maxTimestamp = ts
		}
	}

	nextSince := sinceMs
	if maxTimestamp > 0 {
		nextSince = maxTimestamp + 1
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"entries":   entries,
		"nextSince": nextSince,
	})
}

func handleSessionRoutes(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) < 2 || parts[0] == "" {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	sessionID := parts[0]
	action := parts[1]

	switch action {
	case "file-status":
		handleSessionFileStatus(w, r, sessionID)
	default:
		httpError(w, http.StatusNotFound, "not found")
	}
}

func handleSessionFileStatus(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodGet {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if fileProcessStore == nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"fileStatuses": []map[string]interface{}{},
		})
		return
	}

	fileResults, err := fileProcessStore.GetSessionFileResults(context.Background(), sessionID)
	if err != nil {
		log.Error().Err(err).Str("sessionId", sessionID).Msg("Failed to get session file results")
		httpError(w, http.StatusInternalServerError, "failed to get file statuses")
		return
	}

	fileStatuses := make([]map[string]interface{}, 0, len(fileResults))
	for _, fr := range fileResults {
		status := map[string]interface{}{
			"key":       fr.OriginalKey,
			"filename":  fr.Filename,
			"status":    fr.Status,
			"converted": fr.Converted,
		}
		if fr.ThumbnailKey != "" {
			status["thumbnailUrl"] = fmt.Sprintf("/api/media/thumbnail?key=%s", fr.ThumbnailKey)
		}
		if fr.Error != "" {
			status["error"] = fr.Error
		}
		fileStatuses = append(fileStatuses, status)
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"fileStatuses": fileStatuses,
	})
}
