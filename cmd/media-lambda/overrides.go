package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/fpang/gemini-media-cli/internal/rag"
	"github.com/rs/zerolog/log"
)

// handleOverrideRoutes dispatches to handleOverrideAction or handleOverrideFinalize
// based on the path: /api/overrides/{sessionID} or /api/overrides/{sessionID}/finalize
func handleOverrideRoutes(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	prefix := "/api/overrides/"
	if !strings.HasPrefix(path, prefix) {
		httpError(w, http.StatusNotFound, "not found")
		return
	}
	rest := strings.TrimPrefix(path, prefix)
	rest = strings.Trim(rest, "/")
	parts := strings.SplitN(rest, "/", 2)

	sessionID := parts[0]
	if sessionID == "" {
		httpError(w, http.StatusBadRequest, "sessionId is required")
		return
	}
	if err := validateSessionID(sessionID); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}

	if !ensureSessionOwner(w, r, sessionID) {
		return
	}

	if len(parts) > 1 && parts[1] == "finalize" {
		handleOverrideFinalize(w, r, sessionID)
		return
	}
	if len(parts) == 1 {
		handleOverrideAction(w, r, sessionID)
		return
	}
	httpError(w, http.StatusNotFound, "not found")
}

// handleOverrideAction handles real-time override POSTs.
// POST /api/overrides/{sessionID}
// Body: {"action": "added_back"|"removed", "mediaKey": "...", "filename": "...", "mediaType": "Photo"|"Video", "aiReason": "..."}
func handleOverrideAction(w http.ResponseWriter, r *http.Request, sessionID string) {
	log.Debug().Str("method", r.Method).Str("path", r.URL.Path).Str("sessionId", sessionID).Msg("Handler entry: handleOverrideAction")

	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		Action    string `json:"action"`
		MediaKey  string `json:"mediaKey"`
		Filename  string `json:"filename"`
		MediaType string `json:"mediaType"`
		AIReason  string `json:"aiReason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Action != "added_back" && req.Action != "removed" {
		httpError(w, http.StatusBadRequest, "action must be added_back or removed")
		return
	}
	if req.MediaKey == "" {
		httpError(w, http.StatusBadRequest, "mediaKey is required")
		return
	}

	userID := getUserSub(r)
	aiVerdict := "excluded"
	if req.Action == "removed" {
		aiVerdict = "selected"
	}

	feedback := rag.ContentFeedback{
		EventType:   rag.EventOverrideAction,
		SessionID:   sessionID,
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
		UserID:      userID,
		MediaKey:    req.MediaKey,
		MediaType:   req.MediaType,
		UserVerdict: req.Action,
		AIVerdict:   aiVerdict,
		Reason:      req.AIReason,
		IsOverride:  true,
		Metadata: map[string]string{
			"filename": req.Filename,
		},
	}

	if ebClient != nil {
		if err := rag.EmitContentFeedback(r.Context(), ebClient, feedback); err != nil {
			log.Warn().Err(err).Str("sessionId", sessionID).Str("mediaKey", req.MediaKey).Msg("Failed to emit override action to EventBridge (best effort)")
		}
	}

	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleOverrideFinalize handles final delta POSTs when user proceeds.
// POST /api/overrides/{sessionID}/finalize
// Body: {"added": [{"mediaKey": "...", "filename": "...", "aiReason": "..."}], "removed": [...]}
func handleOverrideFinalize(w http.ResponseWriter, r *http.Request, sessionID string) {
	log.Debug().Str("method", r.Method).Str("path", r.URL.Path).Str("sessionId", sessionID).Msg("Handler entry: handleOverrideFinalize")

	if r.Method != http.MethodPost {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		Added   []struct {
			MediaKey string `json:"mediaKey"`
			Filename string `json:"filename"`
			AIReason string `json:"aiReason"`
		} `json:"added"`
		Removed []struct {
			MediaKey string `json:"mediaKey"`
			Filename string `json:"filename"`
			AIReason string `json:"aiReason"`
		} `json:"removed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	userID := getUserSub(r)
	ctx := r.Context()

	if ebClient != nil {
		for _, item := range req.Added {
			feedback := rag.ContentFeedback{
				EventType:   rag.EventOverridesFinalized,
				SessionID:   sessionID,
				Timestamp:   time.Now().UTC().Format(time.RFC3339),
				UserID:      userID,
				MediaKey:    item.MediaKey,
				UserVerdict: "added_back",
				AIVerdict:   "excluded",
				Reason:      item.AIReason,
				IsOverride:  true,
				Metadata: map[string]string{
					"filename":  item.Filename,
					"action":    "added_back",
					"finalized": "true",
				},
			}
			if err := rag.EmitContentFeedback(ctx, ebClient, feedback); err != nil {
				log.Warn().Err(err).Str("sessionId", sessionID).Str("mediaKey", item.MediaKey).Msg("Failed to emit override finalize (added) to EventBridge")
			}
		}
		for _, item := range req.Removed {
			feedback := rag.ContentFeedback{
				EventType:   rag.EventOverridesFinalized,
				SessionID:   sessionID,
				Timestamp:   time.Now().UTC().Format(time.RFC3339),
				UserID:      userID,
				MediaKey:    item.MediaKey,
				UserVerdict: "removed",
				AIVerdict:   "selected",
				Reason:      item.AIReason,
				IsOverride:  true,
				Metadata: map[string]string{
					"filename":  item.Filename,
					"action":    "removed",
					"finalized": "true",
				},
			}
			if err := rag.EmitContentFeedback(ctx, ebClient, feedback); err != nil {
				log.Warn().Err(err).Str("sessionId", sessionID).Str("mediaKey", item.MediaKey).Msg("Failed to emit override finalize (removed) to EventBridge")
			}
		}
	}

	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
