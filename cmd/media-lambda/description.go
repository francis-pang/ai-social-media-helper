package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/fpang/gemini-media-cli/internal/chat"
	"github.com/fpang/gemini-media-cli/internal/jobs"
	"github.com/rs/zerolog/log"
)

// --- Description Endpoints (DDR-036) ---

type descriptionJob struct {
	mu        sync.Mutex
	id        string
	sessionID string
	status    string // "pending", "processing", "complete", "error"
	result    *chat.DescriptionResult
	// rawResponse stores the raw JSON response for multi-turn history
	rawResponse string
	history     []chat.DescriptionConversationEntry
	// groupLabel and tripContext are stored for regeneration
	groupLabel  string
	tripContext string
	// mediaItems are stored for regeneration (thumbnails only — small)
	mediaItems []chat.DescriptionMediaItem
	errMsg     string
}

var (
	descJobsMu sync.Mutex
	descJobs   = make(map[string]*descriptionJob)
)

func newDescriptionJob(sessionID string) *descriptionJob {
	descJobsMu.Lock()
	defer descJobsMu.Unlock()
	id := jobs.GenerateID("desc-")
	j := &descriptionJob{
		id:        id,
		sessionID: sessionID,
		status:    "pending",
	}
	descJobs[id] = j
	return j
}

func getDescriptionJob(id string) *descriptionJob {
	descJobsMu.Lock()
	defer descJobsMu.Unlock()
	return descJobs[id]
}

func setDescriptionJobError(job *descriptionJob, msg string) {
	job.mu.Lock()
	defer job.mu.Unlock()
	job.status = "error"
	job.errMsg = msg
	log.Error().Str("job", job.id).Str("error", msg).Msg("Description job failed")
}

// POST /api/description/generate
// Body: {"sessionId": "uuid", "keys": ["uuid/enhanced/file1.jpg", ...], "groupLabel": "...", "tripContext": "..."}
func handleDescriptionGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
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
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := validateSessionID(req.SessionID); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.Keys) == 0 {
		httpError(w, http.StatusBadRequest, "keys are required")
		return
	}
	for _, key := range req.Keys {
		if err := validateS3Key(key); err != nil {
			httpError(w, http.StatusBadRequest, fmt.Sprintf("invalid key: %s", key))
			return
		}
	}

	job := newDescriptionJob(req.SessionID)
	job.groupLabel = req.GroupLabel
	job.tripContext = req.TripContext

	go runDescriptionJob(job, req.Keys)

	respondJSON(w, http.StatusAccepted, map[string]string{
		"id": job.id,
	})
}

func handleDescriptionRoutes(w http.ResponseWriter, r *http.Request) {
	jobID, action, ok := jobs.ParseRoute(r.URL.Path, "/api/description/", "desc-")
	if !ok {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	job := getDescriptionJob(jobID)
	if job == nil {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	switch action {
	case "results":
		handleDescriptionResults(w, r, job)
	case "feedback":
		handleDescriptionFeedback(w, r, job)
	default:
		httpError(w, http.StatusNotFound, "not found")
	}
}

// GET /api/description/{id}/results?sessionId=...
func handleDescriptionResults(w http.ResponseWriter, r *http.Request, job *descriptionJob) {
	if r.Method != http.MethodGet {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Ownership check (DDR-028)
	if !jobs.CheckOwnership(r, job.sessionID) {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	job.mu.Lock()
	defer job.mu.Unlock()

	resp := map[string]interface{}{
		"id":            job.id,
		"status":        job.status,
		"feedbackRound": len(job.history),
	}
	if job.result != nil {
		resp["caption"] = job.result.Caption
		resp["hashtags"] = job.result.Hashtags
		resp["locationTag"] = job.result.LocationTag
	}
	if job.errMsg != "" {
		resp["error"] = job.errMsg
	}
	respondJSON(w, http.StatusOK, resp)
}

// POST /api/description/{id}/feedback
// Body: {"sessionId": "uuid", "feedback": "make it shorter"}
func handleDescriptionFeedback(w http.ResponseWriter, r *http.Request, job *descriptionJob) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		SessionID string `json:"sessionId"`
		Feedback  string `json:"feedback"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Ownership check (DDR-028)
	if req.SessionID == "" || req.SessionID != job.sessionID {
		httpError(w, http.StatusNotFound, "not found")
		return
	}
	if req.Feedback == "" {
		httpError(w, http.StatusBadRequest, "feedback is required")
		return
	}

	// Mark as processing
	job.mu.Lock()
	if job.status != "complete" {
		job.mu.Unlock()
		httpError(w, http.StatusBadRequest, "description must be complete before providing feedback")
		return
	}
	// Record the current response in history before regenerating
	job.history = append(job.history, chat.DescriptionConversationEntry{
		UserFeedback:  req.Feedback,
		ModelResponse: job.rawResponse,
	})
	job.status = "processing"
	job.mu.Unlock()

	go runDescriptionFeedback(job, req.Feedback)

	respondJSON(w, http.StatusAccepted, map[string]string{
		"status": "processing",
	})
}
