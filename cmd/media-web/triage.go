package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"
)

// --- Triage Job Management ---

type triageJob struct {
	mu      sync.Mutex
	id      string
	status  string // "pending", "processing", "complete", "error"
	keep    []triageResultItem
	discard []triageResultItem
	errMsg  string
	paths   []string // original input paths
}

type triageResultItem struct {
	Media        int    `json:"media"`
	Filename     string `json:"filename"`
	Path         string `json:"path"`
	Saveable     bool   `json:"saveable"`
	Reason       string `json:"reason"`
	ThumbnailURL string `json:"thumbnailUrl"`
}

var (
	jobsMu sync.Mutex
	jobs   = make(map[string]*triageJob)
)

// newJobID generates a cryptographically random job ID to prevent
// sequential enumeration. (DDR-028 Problem 8)
func newJobID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		log.Fatal().Err(err).Msg("Failed to generate random job ID")
	}
	return "triage-" + hex.EncodeToString(b)
}

func newJob(paths []string) *triageJob {
	jobsMu.Lock()
	defer jobsMu.Unlock()
	id := newJobID()
	j := &triageJob{
		id:     id,
		status: "pending",
		paths:  paths,
	}
	jobs[id] = j
	return j
}

func getJob(id string) *triageJob {
	jobsMu.Lock()
	defer jobsMu.Unlock()
	return jobs[id]
}

// --- Triage HTTP Handlers ---

// POST /api/triage/start
func handleTriageStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		Paths []string `json:"paths"`
		Model string   `json:"model,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Paths) == 0 {
		httpError(w, http.StatusBadRequest, "no paths provided")
		return
	}

	model := modelFlag
	if req.Model != "" {
		model = req.Model
	}

	job := newJob(req.Paths)

	go runTriageJob(job, model)

	respondJSON(w, http.StatusAccepted, map[string]string{
		"id": job.id,
	})
}

// Routes under /api/triage/{id}/...
func handleTriageRoutes(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/triage/"), "/")
	if len(parts) < 2 {
		httpError(w, http.StatusNotFound, "not found")
		return
	}

	jobID := parts[0]
	// The job IDs are stored as "triage-N", and the URL path is /api/triage/{N}/...
	// so we need to reconstruct the full ID
	if !strings.HasPrefix(jobID, "triage-") {
		jobID = "triage-" + jobID
	}
	action := parts[1]

	job := getJob(jobID)
	if job == nil {
		httpError(w, http.StatusNotFound, "job not found")
		return
	}

	switch action {
	case "results":
		handleTriageResults(w, r, job)
	case "confirm":
		handleTriageConfirm(w, r, job)
	default:
		httpError(w, http.StatusNotFound, "not found")
	}
}

// GET /api/triage/{id}/results
func handleTriageResults(w http.ResponseWriter, r *http.Request, job *triageJob) {
	if r.Method != http.MethodGet {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	job.mu.Lock()
	defer job.mu.Unlock()

	resp := map[string]interface{}{
		"id":      job.id,
		"status":  job.status,
		"keep":    job.keep,
		"discard": job.discard,
	}
	if job.errMsg != "" {
		resp["error"] = job.errMsg
	}

	respondJSON(w, http.StatusOK, resp)
}

// POST /api/triage/{id}/confirm
func handleTriageConfirm(w http.ResponseWriter, r *http.Request, job *triageJob) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		DeletePaths []string `json:"deletePaths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var (
		deleted       int
		errMsgs       = make([]string, 0)
		reclaimedSize int64
	)

	for _, p := range req.DeletePaths {
		if !isValidDeletePath(job, p) {
			errMsgs = append(errMsgs, fmt.Sprintf("path not in triage results: %s", p))
			continue
		}

		info, err := os.Stat(p)
		if err != nil {
			errMsgs = append(errMsgs, fmt.Sprintf("cannot stat %s: %v", p, err))
			continue
		}
		size := info.Size()

		if err := os.Remove(p); err != nil {
			errMsgs = append(errMsgs, fmt.Sprintf("failed to delete %s: %v", p, err))
			continue
		}

		deleted++
		reclaimedSize += size
		log.Info().Str("path", p).Msg("Deleted file")
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"deleted":        deleted,
		"errors":         errMsgs,
		"reclaimedBytes": reclaimedSize,
	})
}
