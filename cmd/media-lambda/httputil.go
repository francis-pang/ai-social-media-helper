package main

import (
	"encoding/json"
	"net/http"

	"github.com/rs/zerolog/log"
)

// --- JSON Helpers ---

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// httpError sends a JSON error response. The clientMsg is returned to the caller.
// Optional internalDetails are logged server-side but never sent to the client.
// This prevents leaking sensitive info (S3 paths, ARNs, stack traces) while
// keeping client messages useful for debugging. (DDR-028 Problem 16)
func httpError(w http.ResponseWriter, status int, clientMsg string, internalDetails ...string) {
	if len(internalDetails) > 0 {
		log.Error().
			Int("status", status).
			Str("clientMsg", clientMsg).
			Strs("internalDetails", internalDetails).
			Msg("HTTP error with internal details")
	}
	respondJSON(w, status, map[string]string{"error": clientMsg})
}
