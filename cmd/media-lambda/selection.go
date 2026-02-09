package main

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/fpang/gemini-media-cli/internal/chat"
	"github.com/fpang/gemini-media-cli/internal/jobs"
	"github.com/rs/zerolog/log"
)

// --- Selection Job Management (DDR-030) ---

type selectionJob struct {
	mu          sync.Mutex
	id          string
	sessionID   string
	status      string // "pending", "processing", "complete", "error"
	selected    []selectionResultItem
	excluded    []selectionExcludedItem
	sceneGroups []selectionSceneGroup
	errMsg      string
}

type selectionResultItem struct {
	Rank           int    `json:"rank"`
	Media          int    `json:"media"`
	Filename       string `json:"filename"`
	Key            string `json:"key"`
	Type           string `json:"type"`
	Scene          string `json:"scene"`
	Justification  string `json:"justification"`
	ComparisonNote string `json:"comparisonNote,omitempty"`
	ThumbnailURL   string `json:"thumbnailUrl"`
}

type selectionExcludedItem struct {
	Media        int    `json:"media"`
	Filename     string `json:"filename"`
	Key          string `json:"key"`
	Reason       string `json:"reason"`
	Category     string `json:"category"`
	DuplicateOf  string `json:"duplicateOf,omitempty"`
	ThumbnailURL string `json:"thumbnailUrl"`
}

type selectionSceneGroup struct {
	Name      string                    `json:"name"`
	GPS       string                    `json:"gps,omitempty"`
	TimeRange string                    `json:"timeRange,omitempty"`
	Items     []selectionSceneGroupItem `json:"items"`
}

type selectionSceneGroupItem struct {
	Media        int    `json:"media"`
	Filename     string `json:"filename"`
	Key          string `json:"key"`
	Type         string `json:"type"`
	Selected     bool   `json:"selected"`
	Description  string `json:"description"`
	ThumbnailURL string `json:"thumbnailUrl"`
}

var (
	selJobsMu sync.Mutex
	selJobs   = make(map[string]*selectionJob)
)

func newSelectionJob(sessionID string) *selectionJob {
	selJobsMu.Lock()
	defer selJobsMu.Unlock()
	id := jobs.GenerateID("sel-")
	j := &selectionJob{
		id:        id,
		sessionID: sessionID,
		status:    "pending",
	}
	selJobs[id] = j
	return j
}

func getSelectionJob(id string) *selectionJob {
	selJobsMu.Lock()
	defer selJobsMu.Unlock()
	return selJobs[id]
}

func setSelectionJobError(job *selectionJob, msg string) {
	job.mu.Lock()
	defer job.mu.Unlock()
	job.status = "error"
	job.errMsg = msg
	log.Error().Str("job", job.id).Str("error", msg).Msg("Selection job failed")
}

// --- Selection Endpoints (DDR-030) ---

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

	job := newSelectionJob(req.SessionID)
	go runSelectionJob(job, req.TripContext, model)

	respondJSON(w, http.StatusAccepted, map[string]string{
		"id": job.id,
	})
}

func handleSelectionRoutes(w http.ResponseWriter, r *http.Request) {
	jobID, action, ok := jobs.ParseRoute(r.URL.Path, "/api/selection/", "sel-")
	if !ok {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	job := getSelectionJob(jobID)
	if job == nil {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	switch action {
	case "results":
		handleSelectionResults(w, r, job)
	default:
		httpError(w, http.StatusNotFound, "not found")
	}
}

// GET /api/selection/{id}/results?sessionId=...
func handleSelectionResults(w http.ResponseWriter, r *http.Request, job *selectionJob) {
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
		"id":          job.id,
		"status":      job.status,
		"selected":    job.selected,
		"excluded":    job.excluded,
		"sceneGroups": job.sceneGroups,
	}
	if job.errMsg != "" {
		resp["error"] = job.errMsg
	}
	respondJSON(w, http.StatusOK, resp)
}
