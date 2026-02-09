package main

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/rs/zerolog/log"
)

// --- Presigned Upload URL ---

// GET /api/upload-url?sessionId=...&filename=...&contentType=...
// Returns a presigned S3 PUT URL so the browser can upload directly to S3.
//
// Security (DDR-028):
//   - sessionId must be a valid UUID
//   - filename is sanitized and validated against safe character set
//   - contentType must be in the allowed media type list
//   - Content-Type is included in the presigned signature
//   - Size limits are enforced at processing time (triage/selection start)
func handleUploadURL(w http.ResponseWriter, r *http.Request) {
	log.Debug().Str("method", r.Method).Str("path", r.URL.Path).Msg("Handler entry: handleUploadURL")

	if r.Method != http.MethodGet {
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	sessionID := r.URL.Query().Get("sessionId")
	filename := r.URL.Query().Get("filename")
	contentType := r.URL.Query().Get("contentType")

	log.Debug().
		Str("sessionId", sessionID).
		Str("filename", filename).
		Str("contentType", contentType).
		Msg("Upload URL request received")

	if sessionID == "" || filename == "" || contentType == "" {
		log.Warn().Msg("Missing required query parameters: sessionId, filename, or contentType")
		httpError(w, http.StatusBadRequest, "sessionId, filename, and contentType are required")
		return
	}

	// Validate sessionId is a proper UUID (DDR-028 Problem 3)
	if err := validateSessionID(sessionID); err != nil {
		log.Warn().Err(err).Str("sessionId", sessionID).Msg("Session ID validation failed")
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Sanitize and validate filename (DDR-028 Problem 4)
	filename = filepath.Base(filename) // strip directory components
	if err := validateFilename(filename); err != nil {
		log.Warn().Err(err).Str("filename", filename).Msg("Filename validation failed")
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Validate content type against allowlist (DDR-028 Problem 7)
	if !allowedContentTypes[contentType] {
		log.Warn().Str("contentType", contentType).Msg("Unsupported content type")
		httpError(w, http.StatusBadRequest, fmt.Sprintf("unsupported content type: %s", contentType))
		return
	}

	key := sessionID + "/" + filename

	result, err := presigner.PresignPutObject(context.Background(), &s3.PutObjectInput{
		Bucket:      &mediaBucket,
		Key:         &key,
		ContentType: &contentType,
	}, s3.WithPresignExpires(15*time.Minute))
	if err != nil {
		log.Error().Err(err).Str("key", key).Msg("Failed to generate presigned URL")
		httpError(w, http.StatusInternalServerError, "failed to generate upload URL")
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{
		"uploadUrl": result.URL,
		"key":       key,
	})
}
