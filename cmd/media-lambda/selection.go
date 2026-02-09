package main

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/fpang/gemini-media-cli/internal/chat"
	"github.com/fpang/gemini-media-cli/internal/jobs"
	"github.com/fpang/gemini-media-cli/internal/store"
	"github.com/rs/zerolog/log"
)

// --- Selection Endpoints (DDR-030, DDR-050) ---

// POST /api/selection/start
// Body: {"sessionId": "uuid", "tripContext": "...", "model": "optional-model-name"}
func handleSelectionStart(w http.ResponseWriter, r *http.Request) {
	log.Debug().Str("method", r.Method).Str("path", r.URL.Path).Msg("Handler entry: handleSelectionStart")

	if r.Method != http.MethodPost {
		log.Warn().Str("param", "method").Msg("Method not allowed")
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		SessionID   string `json:"sessionId"`
		TripContext string `json:"tripContext"`
		Model       string `json:"model,omitempty"`
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

	jobID := jobs.GenerateID("sel-")

	// Write pending job to DynamoDB (DDR-050).
	if sessionStore != nil {
		pendingJob := &store.SelectionJob{
			ID:     jobID,
			Status: "pending",
		}
		if err := sessionStore.PutSelectionJob(context.Background(), req.SessionID, pendingJob); err != nil {
			log.Error().Err(err).Str("jobId", jobID).Msg("Failed to persist pending selection job")
			httpError(w, http.StatusInternalServerError, "failed to create job")
			return
		}
	}

	// List S3 objects to build mediaKeys for the Step Functions pipeline.
	prefix := req.SessionID + "/"
	listResult, err := s3Client.ListObjectsV2(context.Background(), &s3.ListObjectsV2Input{
		Bucket: aws.String(mediaBucket),
		Prefix: aws.String(prefix),
	})
	if err != nil {
		log.Error().Err(err).Str("sessionId", req.SessionID).Msg("Failed to list S3 objects for selection")
		httpError(w, http.StatusInternalServerError, "failed to list uploaded media")
		return
	}
	var mediaKeys []string
	for _, obj := range listResult.Contents {
		mediaKeys = append(mediaKeys, *obj.Key)
	}
	log.Debug().Int("keyCount", len(mediaKeys)).Str("sessionId", req.SessionID).Msg("S3 objects listed")
	if len(mediaKeys) == 0 {
		log.Warn().Str("param", "keys").Msg("No files found for session")
		httpError(w, http.StatusBadRequest, "no files found for session — upload files first")
		return
	}
	log.Info().Int("count", len(mediaKeys)).Str("sessionId", req.SessionID).Msg("Found S3 objects for selection pipeline")

	// Start Step Functions execution (DDR-050).
	if sfnClient != nil && selectionSfnArn != "" {
		sfnInput, _ := json.Marshal(map[string]interface{}{
			"sessionId":   req.SessionID,
			"jobId":       jobID,
			"tripContext": req.TripContext,
			"model":       model,
			"mediaKeys":   mediaKeys,
		})
		log.Info().
			Str("jobId", jobID).
			Str("sessionId", req.SessionID).
			Str("model", model).
			Int("keyCount", len(mediaKeys)).
			Str("sfnArn", selectionSfnArn).
			Msg("Job dispatched")
		_, err := sfnClient.StartExecution(context.Background(), &sfn.StartExecutionInput{
			StateMachineArn: aws.String(selectionSfnArn),
			Input:           aws.String(string(sfnInput)),
			Name:            aws.String(jobID),
		})
		if err != nil {
			log.Error().Err(err).Str("jobId", jobID).Msg("Failed to start selection pipeline")
			// Update DynamoDB with error
			if sessionStore != nil {
				errJob := &store.SelectionJob{ID: jobID, Status: "error", Error: "failed to start processing pipeline"}
				sessionStore.PutSelectionJob(context.Background(), req.SessionID, errJob)
			}
			httpError(w, http.StatusInternalServerError, "failed to start processing")
			return
		}
	}

	respondJSON(w, http.StatusAccepted, map[string]string{
		"id": jobID,
	})
}

func handleSelectionRoutes(w http.ResponseWriter, r *http.Request) {
	jobID, action, ok := jobs.ParseRoute(r.URL.Path, "/api/selection/", "sel-")
	if !ok {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	switch action {
	case "results":
		handleSelectionResults(w, r, jobID)
	default:
		httpError(w, http.StatusNotFound, "not found")
	}
}

// GET /api/selection/{id}/results?sessionId=...
func handleSelectionResults(w http.ResponseWriter, r *http.Request, jobID string) {
	log.Debug().Str("method", r.Method).Str("path", r.URL.Path).Str("jobId", jobID).Msg("Handler entry: handleSelectionResults")

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

	// Read from DynamoDB (DDR-050).
	if sessionStore == nil {
		httpError(w, http.StatusServiceUnavailable, "store not configured")
		return
	}

	job, err := sessionStore.GetSelectionJob(context.Background(), sessionID, jobID)
	if err != nil {
		log.Error().Err(err).Str("jobId", jobID).Msg("Failed to read selection job from DynamoDB")
		httpError(w, http.StatusInternalServerError, "failed to read job status")
		return
	}
	if job == nil {
		log.Debug().Str("jobId", jobID).Str("sessionId", sessionID).Msg("Selection job not found in DynamoDB")
		httpError(w, http.StatusNotFound, "not found")
		return
	}
	log.Debug().Str("jobId", jobID).Str("status", job.Status).Msg("Selection job found in DynamoDB")

	resp := map[string]interface{}{
		"id":          job.ID,
		"status":      job.Status,
		"selected":    job.Selected,
		"excluded":    job.Excluded,
		"sceneGroups": job.SceneGroups,
	}
	if job.Error != "" {
		resp["error"] = job.Error
	}
	respondJSON(w, http.StatusOK, resp)
}
