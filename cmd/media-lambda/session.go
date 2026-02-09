package main

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/rs/zerolog/log"
)

// --- Session Invalidation (DDR-037, DDR-050: DynamoDB-backed) ---

// POST /api/session/invalidate
// Body: {"sessionId": "uuid", "fromStep": "triage"|"selection"|"enhancement"|"grouping"|"download"|"description"|"publish"}
//
// Invalidates all downstream state in DynamoDB from the given step onward.
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

	if sessionStore == nil {
		httpError(w, http.StatusServiceUnavailable, "store not configured")
		return
	}

	// Delegate to DynamoDB InvalidateDownstream (DDR-050).
	deletedSKs, err := sessionStore.InvalidateDownstream(context.Background(), req.SessionID, req.FromStep)
	if err != nil {
		log.Error().Err(err).
			Str("sessionId", req.SessionID).
			Str("fromStep", req.FromStep).
			Msg("Failed to invalidate downstream state")
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Best-effort: delete S3 enhanced/ and thumbnails/ artifacts if enhancement was invalidated
	if req.FromStep == "enhancement" || req.FromStep == "selection" || req.FromStep == "triage" {
		go cleanupS3Prefix(req.SessionID, "enhanced/")
		go cleanupS3Prefix(req.SessionID, "thumbnails/")
	}

	log.Info().
		Str("sessionId", req.SessionID).
		Str("fromStep", req.FromStep).
		Int("count", len(deletedSKs)).
		Msg("Session state invalidated via DynamoDB")

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"invalidated": deletedSKs,
	})
}
