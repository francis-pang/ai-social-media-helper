package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/fpang/gemini-media-cli/internal/jobs"
	"github.com/rs/zerolog/log"
)

// --- Download Job Management (DDR-034) ---

type downloadJob struct {
	mu        sync.Mutex
	id        string
	sessionID string
	status    string // "pending", "processing", "complete", "error"
	bundles   []downloadBundle
	errMsg    string
}

type downloadBundle struct {
	Type        string `json:"type"`                  // "images" or "videos"
	Name        string `json:"name"`                  // display name: "images.zip" or "videos-1.zip"
	ZipKey      string `json:"zipKey"`                // S3 key of the created ZIP
	DownloadURL string `json:"downloadUrl,omitempty"` // presigned GET URL (populated on complete)
	FileCount   int    `json:"fileCount"`
	TotalSize   int64  `json:"totalSize"` // total original file size in bytes
	ZipSize     int64  `json:"zipSize"`   // ZIP file size in bytes (populated on complete)
	Status      string `json:"status"`    // "pending", "processing", "complete", "error"
	Error       string `json:"error,omitempty"`
}

// fileWithSize holds an S3 key and its object size (from HeadObject).
type fileWithSize struct {
	key  string
	size int64
}

var (
	dlJobsMu sync.Mutex
	dlJobs   = make(map[string]*downloadJob)
)

func newDownloadJob(sessionID string) *downloadJob {
	dlJobsMu.Lock()
	defer dlJobsMu.Unlock()
	id := jobs.GenerateID("dl-")
	j := &downloadJob{
		id:        id,
		sessionID: sessionID,
		status:    "pending",
	}
	dlJobs[id] = j
	return j
}

func getDownloadJob(id string) *downloadJob {
	dlJobsMu.Lock()
	defer dlJobsMu.Unlock()
	return dlJobs[id]
}

func setDownloadJobError(job *downloadJob, msg string) {
	job.mu.Lock()
	defer job.mu.Unlock()
	job.status = "error"
	job.errMsg = msg
	log.Error().Str("job", job.id).Str("error", msg).Msg("Download job failed")
}

// --- Download Endpoints (DDR-034) ---

// POST /api/download/start
// Body: {"sessionId": "uuid", "keys": ["uuid/enhanced/file1.jpg", ...], "groupLabel": "Tokyo Day 1"}
func handleDownloadStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		SessionID  string   `json:"sessionId"`
		Keys       []string `json:"keys"`
		GroupLabel string   `json:"groupLabel"`
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

	job := newDownloadJob(req.SessionID)
	go runDownloadJob(job, req.Keys, req.GroupLabel)

	respondJSON(w, http.StatusAccepted, map[string]string{
		"id": job.id,
	})
}

func handleDownloadRoutes(w http.ResponseWriter, r *http.Request) {
	jobID, action, ok := jobs.ParseRoute(r.URL.Path, "/api/download/", "dl-")
	if !ok {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	job := getDownloadJob(jobID)
	if job == nil {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	switch action {
	case "results":
		handleDownloadResults(w, r, job)
	default:
		httpError(w, http.StatusNotFound, "not found")
	}
}

// GET /api/download/{id}/results?sessionId=...
func handleDownloadResults(w http.ResponseWriter, r *http.Request, job *downloadJob) {
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
		"id":      job.id,
		"status":  job.status,
		"bundles": job.bundles,
	}
	if job.errMsg != "" {
		resp["error"] = job.errMsg
	}
	respondJSON(w, http.StatusOK, resp)
}
