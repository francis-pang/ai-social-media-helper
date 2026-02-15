// Package jobutil provides shared helpers for Lambda job lifecycle operations.
//
// SetJobError unifies the error-writing pattern found across triage-lambda,
// description-lambda, and other handlers that log an error and persist an
// error status to DynamoDB.
package jobutil

import (
	"context"

	"github.com/rs/zerolog/log"
)

// ErrorWriter is a function that persists a job error to the backing store.
// Each Lambda provides its own implementation (e.g. PutTriageJob, PutDescriptionJob).
type ErrorWriter func(ctx context.Context, sessionID, jobID, errMsg string) error

// SetJobError logs the error and delegates persistence to the provided writer.
// Replaces setTriageError, setDescError, and similar one-shot error handlers.
func SetJobError(ctx context.Context, sessionID, jobID, msg string, write ErrorWriter) error {
	log.Error().
		Str("job", jobID).
		Str("sessionId", sessionID).
		Str("error", msg).
		Msg("Job failed")
	return write(ctx, sessionID, jobID, msg)
}
