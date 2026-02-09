package main

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
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
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		SessionID   string `json:"sessionId"`
		TripContext string `json:"tripContext"`
		Model       string `json:"model,omitempty"`
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

	// Start Step Functions execution (DDR-050).
	if sfnClient != nil && selectionSfnArn != "" {
		sfnInput, _ := json.Marshal(map[string]interface{}{
			"sessionId":   req.SessionID,
			"jobId":       jobID,
			"tripContext": req.TripContext,
			"model":       model,
		})
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

		log.Info().
			Str("jobId", jobID).
			Str("sessionId", req.SessionID).
			Str("sfnArn", selectionSfnArn).
			Msg("Selection pipeline started via Step Functions")
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
	if r.Method != http.MethodGet {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	sessionID := r.URL.Query().Get("sessionId")
	if sessionID == "" {
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
		httpError(w, http.StatusNotFound, "not found")
		return
	}

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
