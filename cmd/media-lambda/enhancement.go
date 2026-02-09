package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fpang/gemini-media-cli/internal/chat"
	"github.com/fpang/gemini-media-cli/internal/filehandler"
	"github.com/fpang/gemini-media-cli/internal/jobs"
	"github.com/rs/zerolog/log"
)

// --- Enhancement Job Management (DDR-031) ---

type enhancementJob struct {
	mu             sync.Mutex
	id             string
	sessionID      string
	status         string // "pending", "processing", "complete", "error"
	items          []enhancementResultItem
	totalCount     int
	completedCount int
	errMsg         string
}

type enhancementResultItem struct {
	Key              string               `json:"key"`
	Filename         string               `json:"filename"`
	Phase            string               `json:"phase"`
	OriginalKey      string               `json:"originalKey"`
	EnhancedKey      string               `json:"enhancedKey"`
	OriginalThumbKey string               `json:"originalThumbKey"`
	EnhancedThumbKey string               `json:"enhancedThumbKey"`
	Phase1Text       string               `json:"phase1Text"`
	Analysis         *chat.AnalysisResult `json:"analysis,omitempty"`
	ImagenEdits      int                  `json:"imagenEdits"`
	FeedbackHistory  []chat.FeedbackEntry `json:"feedbackHistory"`
	Error            string               `json:"error,omitempty"`
}

var (
	enhJobsMu sync.Mutex
	enhJobs   = make(map[string]*enhancementJob)
)

func newEnhancementJob(sessionID string) *enhancementJob {
	enhJobsMu.Lock()
	defer enhJobsMu.Unlock()
	id := jobs.GenerateID("enh-")
	j := &enhancementJob{
		id:        id,
		sessionID: sessionID,
		status:    "pending",
	}
	enhJobs[id] = j
	return j
}

func getEnhancementJob(id string) *enhancementJob {
	enhJobsMu.Lock()
	defer enhJobsMu.Unlock()
	return enhJobs[id]
}

func setEnhancementJobError(job *enhancementJob, msg string) {
	job.mu.Lock()
	defer job.mu.Unlock()
	job.status = "error"
	job.errMsg = msg
	log.Error().Str("job", job.id).Str("error", msg).Msg("Enhancement job failed")
}

// --- Enhancement Endpoints (DDR-031) ---

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

	// Filter to photos only (enhancement is for photos)
	var photoKeys []string
	for _, key := range req.Keys {
		ext := strings.ToLower(filepath.Ext(key))
		if filehandler.IsImage(ext) {
			photoKeys = append(photoKeys, key)
		}
	}

	if len(photoKeys) == 0 {
		httpError(w, http.StatusBadRequest, "no photo files in the provided keys")
		return
	}

	job := newEnhancementJob(req.SessionID)
	go runEnhancementJob(job, photoKeys)

	respondJSON(w, http.StatusAccepted, map[string]string{
		"id": job.id,
	})
}

func handleEnhanceRoutes(w http.ResponseWriter, r *http.Request) {
	jobID, action, ok := jobs.ParseRoute(r.URL.Path, "/api/enhance/", "enh-")
	if !ok {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	job := getEnhancementJob(jobID)
	if job == nil {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	switch action {
	case "results":
		handleEnhanceResults(w, r, job)
	case "feedback":
		handleEnhanceFeedback(w, r, job)
	default:
		httpError(w, http.StatusNotFound, "not found")
	}
}

// GET /api/enhance/{id}/results?sessionId=...
func handleEnhanceResults(w http.ResponseWriter, r *http.Request, job *enhancementJob) {
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
		"id":             job.id,
		"status":         job.status,
		"items":          job.items,
		"totalCount":     job.totalCount,
		"completedCount": job.completedCount,
	}
	if job.errMsg != "" {
		resp["error"] = job.errMsg
	}
	respondJSON(w, http.StatusOK, resp)
}
