package main

import (
	"encoding/json"
	"net/http"

	"github.com/rs/zerolog/log"
)

// --- Session Invalidation (DDR-037) ---

// POST /api/session/invalidate
// Body: {"sessionId": "uuid", "fromStep": "selection"|"enhancement"|"grouping"|"download"|"description"}
//
// Invalidates all downstream in-memory job state from the given step onward.
// Called by the frontend when a user navigates back and re-triggers processing,
// ensuring stale job results are not returned on subsequent polls.
func handleSessionInvalidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		SessionID string `json:"sessionId"`
		FromStep  string `json:"fromStep"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validateSessionID(req.SessionID); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Determine which job types to invalidate based on the cascade.
	// Order: selection -> enhancement -> grouping (no backend state) -> download -> description
	// "fromStep" means: invalidate this step and everything after it.
	type stepDef struct {
		name string
		// index in the cascade (lower = earlier)
	}
	stepOrder := []string{"selection", "enhancement", "grouping", "download", "description"}
	fromIndex := -1
	for i, s := range stepOrder {
		if s == req.FromStep {
			fromIndex = i
			break
		}
	}
	if fromIndex < 0 {
		httpError(w, http.StatusBadRequest, "invalid fromStep: must be one of selection, enhancement, grouping, download, description")
		return
	}

	invalidated := []string{}

	// Invalidate steps from fromIndex onward
	for _, step := range stepOrder[fromIndex:] {
		switch step {
		case "selection":
			selJobsMu.Lock()
			for id, job := range selJobs {
				if job.sessionID == req.SessionID {
					delete(selJobs, id)
					invalidated = append(invalidated, "selection:"+id)
				}
			}
			selJobsMu.Unlock()

		case "enhancement":
			enhJobsMu.Lock()
			for id, job := range enhJobs {
				if job.sessionID == req.SessionID {
					delete(enhJobs, id)
					invalidated = append(invalidated, "enhancement:"+id)
				}
			}
			enhJobsMu.Unlock()

			// Best-effort: delete S3 enhanced/ and thumbnails/ artifacts
			go cleanupS3Prefix(req.SessionID, "enhanced/")
			go cleanupS3Prefix(req.SessionID, "thumbnails/")

		case "download":
			dlJobsMu.Lock()
			for id, job := range dlJobs {
				if job.sessionID == req.SessionID {
					delete(dlJobs, id)
					invalidated = append(invalidated, "download:"+id)
				}
			}
			dlJobsMu.Unlock()

		case "description":
			descJobsMu.Lock()
			for id, job := range descJobs {
				if job.sessionID == req.SessionID {
					delete(descJobs, id)
					invalidated = append(invalidated, "description:"+id)
				}
			}
			descJobsMu.Unlock()

		case "grouping":
			// Grouping is client-side only — no backend state to invalidate.
			invalidated = append(invalidated, "grouping:client-only")
		}
	}

	log.Info().
		Str("sessionId", req.SessionID).
		Str("fromStep", req.FromStep).
		Int("count", len(invalidated)).
		Msg("Session state invalidated")

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"invalidated": invalidated,
	})
}
