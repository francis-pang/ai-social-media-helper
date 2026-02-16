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
	"time"

	"github.com/rs/zerolog/log"
)

// --- Triage Job Management ---

type triageJob struct {
	mu        sync.Mutex
	id        string
	status    string // "pending", "processing", "complete", "error"
	keep      []triageResultItem
	discard   []triageResultItem
	errMsg    string
	paths     []string  // original input paths
	createdAt time.Time // for TTL-based eviction
}

type triageResultItem struct {
	Media        int    `json:"media"`
	Filename     string `json:"filename"`
	Path         string `json:"path"`
	Saveable     bool   `json:"saveable"`
	Reason       string `json:"reason"`
	ThumbnailURL string `json:"thumbnailUrl"`
}

const (
	// jobTTL is how long completed/errored jobs are retained before eviction.
	jobTTL = 1 * time.Hour
	// maxJobs is the hard cap on in-memory jobs. When exceeded the oldest
	// completed job is evicted regardless of TTL.
	maxJobs = 100
)

var (
	jobsMu sync.Mutex
	jobs   = make(map[string]*triageJob)
)

func init() {
	// Background goroutine that evicts expired jobs every 5 minutes.
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			evictExpiredJobs()
		}
	}()
}

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

	// Evict oldest completed job if we're at the hard cap.
	if len(jobs) >= maxJobs {
		evictOldestCompleted()
	}

	id := newJobID()
	j := &triageJob{
		id:        id,
		status:    "pending",
		paths:     paths,
		createdAt: time.Now(),
	}
	jobs[id] = j
	return j
}

func getJob(id string) *triageJob {
	jobsMu.Lock()
	defer jobsMu.Unlock()
	return jobs[id]
}

// evictExpiredJobs removes completed/errored jobs older than jobTTL.
// Must NOT hold jobsMu — acquires it internally.
func evictExpiredJobs() {
	jobsMu.Lock()
	defer jobsMu.Unlock()
	cutoff := time.Now().Add(-jobTTL)
	for id, j := range jobs {
		j.mu.Lock()
		done := j.status == "complete" || j.status == "error"
		old := j.createdAt.Before(cutoff)
		j.mu.Unlock()
		if done && old {
			delete(jobs, id)
			log.Debug().Str("job", id).Msg("Evicted expired triage job")
		}
	}
}

// evictOldestCompleted removes the single oldest completed/errored job.
// Caller must hold jobsMu.
func evictOldestCompleted() {
	var oldestID string
	var oldestTime time.Time
	for id, j := range jobs {
		j.mu.Lock()
		done := j.status == "complete" || j.status == "error"
		created := j.createdAt
		j.mu.Unlock()
		if done && (oldestID == "" || created.Before(oldestTime)) {
			oldestID = id
			oldestTime = created
		}
	}
	if oldestID != "" {
		delete(jobs, oldestID)
		log.Debug().Str("job", oldestID).Msg("Evicted oldest triage job (at capacity)")
	}
}

// --- Triage HTTP Handlers ---

// maxRequestBodyBytes caps POST request body size to prevent abuse.
const maxRequestBodyBytes = 1 << 20 // 1 MiB

// POST /api/triage/start
func handleTriageStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)

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
	// Job IDs are stored as "triage-<hex>" (e.g. "triage-f9be...c9").
	// The URL may include the full ID or just the hex portion, so add the
	// prefix if missing.
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

	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)

	var req struct {
		DeletePaths []string `json:"deletePaths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var (
		deleted       int
		skipped       int
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
			if os.IsNotExist(err) {
				// File was already deleted (e.g. duplicate confirm request).
				skipped++
				log.Debug().Str("path", p).Msg("File already deleted, skipping")
				continue
			}
			errMsgs = append(errMsgs, fmt.Sprintf("cannot access file for deletion: %v", err))
			continue
		}
		size := info.Size()

		if err := os.Remove(p); err != nil {
			errMsgs = append(errMsgs, fmt.Sprintf("failed to delete file: %v", err))
			continue
		}

		deleted++
		reclaimedSize += size
		log.Info().Str("path", p).Msg("Deleted file")
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"deleted":        deleted,
		"skipped":        skipped,
		"errors":         errMsgs,
		"reclaimedBytes": reclaimedSize,
	})
}
