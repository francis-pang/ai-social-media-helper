package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/fpang/gemini-media-cli/internal/jobs"
	"github.com/fpang/gemini-media-cli/internal/store"
	"github.com/rs/zerolog/log"
)

// --- Publish Endpoints (DDR-040, DDR-050, DDR-052: DynamoDB + Step Functions) ---

// POST /api/publish/start
// Body: {"sessionId": "uuid", "groupId": "group-1", "keys": [...], "caption": "...", "hashtags": [...]}
func handlePublishStart(w http.ResponseWriter, r *http.Request) {
	log.Debug().Str("method", r.Method).Str("path", r.URL.Path).Msg("Handler entry: handlePublishStart")

	if r.Method != http.MethodPost {
		log.Warn().Str("param", "method").Msg("Method not allowed")
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if igClient == nil {
		log.Debug().Msg("Instagram client not configured")
		httpError(w, http.StatusServiceUnavailable, "Instagram publishing is not configured — set INSTAGRAM_ACCESS_TOKEN and INSTAGRAM_USER_ID")
		return
	}
	log.Debug().Msg("Instagram client check passed")

	var req struct {
		SessionID string   `json:"sessionId"`
		GroupID   string   `json:"groupId"`
		Keys      []string `json:"keys"`
		Caption   string   `json:"caption"`
		Hashtags  []string `json:"hashtags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Debug().Err(err).Msg("Request body decoding failed")
		log.Warn().Str("param", "body").Msg("Invalid request body")
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	log.Debug().Str("sessionId", req.SessionID).Str("groupId", req.GroupID).Int("keyCount", len(req.Keys)).Msg("Request body decoded successfully")

	if err := validateSessionID(req.SessionID); err != nil {
		log.Debug().Err(err).Str("sessionId", req.SessionID).Msg("SessionId validation failed")
		log.Warn().Str("param", "sessionId").Msg("SessionId validation failed")
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	log.Debug().Str("sessionId", req.SessionID).Msg("SessionId validation passed")
	if req.GroupID == "" {
		log.Warn().Str("param", "groupId").Msg("GroupId is required")
		httpError(w, http.StatusBadRequest, "groupId is required")
		return
	}
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

	// Assemble full caption with hashtags
	fullCaption := req.Caption
	if len(req.Hashtags) > 0 {
		hashtagStrs := make([]string, len(req.Hashtags))
		for i, h := range req.Hashtags {
			if strings.HasPrefix(h, "#") {
				hashtagStrs[i] = h
			} else {
				hashtagStrs[i] = "#" + h
			}
		}
		fullCaption += "\n\n" + strings.Join(hashtagStrs, " ")
	}

	jobID := jobs.GenerateID("pub-")

	// Write pending job to DynamoDB (DDR-050).
	if sessionStore != nil {
		pendingJob := &store.PublishJob{
			ID:         jobID,
			GroupID:    req.GroupID,
			Status:     "pending",
			Phase:      "pending",
			TotalItems: len(req.Keys),
		}
		if err := sessionStore.PutPublishJob(context.Background(), req.SessionID, pendingJob); err != nil {
			log.Error().Err(err).Str("jobId", jobID).Msg("Failed to persist pending publish job")
			httpError(w, http.StatusInternalServerError, "failed to create job")
			return
		}
	}

	// Dispatch to Publish Pipeline Step Functions (DDR-052).
	if sfnClient != nil && publishSfnArn != "" {
		sfnInput, _ := json.Marshal(map[string]interface{}{
			"type":      "publish-create-containers",
			"sessionId": req.SessionID,
			"jobId":     jobID,
			"groupId":   req.GroupID,
			"keys":      req.Keys,
			"caption":   fullCaption,
		})
		log.Info().
			Str("jobId", jobID).
			Str("sessionId", req.SessionID).
			Str("groupId", req.GroupID).
			Int("keyCount", len(req.Keys)).
			Str("sfnArn", publishSfnArn).
			Msg("Job dispatched to Publish Pipeline")
		_, err := sfnClient.StartExecution(context.Background(), &sfn.StartExecutionInput{
			StateMachineArn: aws.String(publishSfnArn),
			Input:           aws.String(string(sfnInput)),
			Name:            aws.String(jobID),
		})
		if err != nil {
			log.Error().Err(err).Str("jobId", jobID).Str("sfnArn", publishSfnArn).Msg("Failed to start publish pipeline")
			errDetail := fmt.Sprintf("failed to start processing: %v", err)
			if sessionStore != nil {
				errJob := &store.PublishJob{ID: jobID, GroupID: req.GroupID, Status: "error", Phase: "error", Error: errDetail}
				sessionStore.PutPublishJob(context.Background(), req.SessionID, errJob)
			}
			httpError(w, http.StatusInternalServerError, errDetail)
			return
		}
	}

	respondJSON(w, http.StatusAccepted, map[string]string{
		"id": jobID,
	})
}

func handlePublishRoutes(w http.ResponseWriter, r *http.Request) {
	jobID, action, ok := jobs.ParseRoute(r.URL.Path, "/api/publish/", "pub-")
	if !ok {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	switch action {
	case "status":
		handlePublishStatus(w, r, jobID)
	default:
		httpError(w, http.StatusNotFound, "not found")
	}
}

// GET /api/publish/{id}/status?sessionId=...
func handlePublishStatus(w http.ResponseWriter, r *http.Request, jobID string) {
	log.Debug().Str("method", r.Method).Str("path", r.URL.Path).Str("jobId", jobID).Msg("Handler entry: handlePublishStatus")

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

	job, err := sessionStore.GetPublishJob(context.Background(), sessionID, jobID)
	if err != nil {
		log.Error().Err(err).Str("jobId", jobID).Msg("Failed to read publish job from DynamoDB")
		httpError(w, http.StatusInternalServerError, "failed to read job status")
		return
	}
	if job == nil {
		log.Debug().Str("jobId", jobID).Str("sessionId", sessionID).Msg("Publish job not found in DynamoDB")
		httpError(w, http.StatusNotFound, "not found")
		return
	}
	log.Debug().Str("jobId", jobID).Str("status", job.Status).Str("phase", job.Phase).Msg("Publish job found in DynamoDB")

	resp := map[string]interface{}{
		"id":     job.ID,
		"status": job.Status,
		"phase":  job.Phase,
		"progress": map[string]int{
			"completed": job.CompletedItems,
			"total":     job.TotalItems,
		},
	}
	if job.InstagramPostID != "" {
		resp["instagramPostId"] = job.InstagramPostID
	}
	if job.Error != "" {
		resp["error"] = job.Error
	}
	respondJSON(w, http.StatusOK, resp)
}
