package jobs

import (
	"net/http"
	"strings"
)

// ParseRoute extracts the job ID and action from a URL path like /api/triage/{id}/{action}.
// apiPrefix should be like "/api/triage/", idPrefix should be like "triage-".
// Returns the normalized job ID and action, or empty strings if the path is invalid.
func ParseRoute(path, apiPrefix, idPrefix string) (jobID, action string, ok bool) {
	parts := strings.Split(strings.TrimPrefix(path, apiPrefix), "/")
	if len(parts) < 2 {
		return "", "", false
	}

	jobID = parts[0]
	if !strings.HasPrefix(jobID, idPrefix) {
		jobID = idPrefix + jobID
	}
	return jobID, parts[1], true
}

// CheckOwnership verifies the sessionId query param matches the job's session ID.
// Returns true if the check passes. If it fails, writes a 404 response using errFn.
func CheckOwnership(r *http.Request, jobSessionID string) bool {
	sessionID := r.URL.Query().Get("sessionId")
	return sessionID != "" && sessionID == jobSessionID
}
