package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/rs/zerolog/log"

	"github.com/fpang/gemini-media-cli/internal/store"
)

// findTriageJobID finds the triage job ID for a session by querying for TRIAGE# prefix.
func findTriageJobID(ctx context.Context, sessionID string) (string, error) {
	// Use a simple approach: the API Lambda writes the job ID, and we look it up
	items, err := sessionStore.QueryBySKPrefix(ctx, sessionID, "TRIAGE#")
	if err != nil {
		return "", fmt.Errorf("query triage jobs: %w", err)
	}
	if len(items) == 0 {
		return "", fmt.Errorf("no triage job found for session %s", sessionID)
	}

	// Use the most recent triage job (last in the list)
	lastItem := items[len(items)-1]
	if skAttr, ok := lastItem["SK"].(*types.AttributeValueMemberS); ok {
		return strings.TrimPrefix(skAttr.Value, "TRIAGE#"), nil
	}
	return "", fmt.Errorf("could not extract job ID from SK")
}

func writeErrorResult(ctx context.Context, sessionID, filename, originalKey, errMsg string) error {
	log.Warn().Str("sessionId", sessionID).Str("filename", filename).Str("key", originalKey).Str("error", errMsg).Msg("File processing failed")

	jobID, _ := findTriageJobID(ctx, sessionID)
	if jobID == "" {
		log.Warn().Str("sessionId", sessionID).Str("filename", filename).Str("error", errMsg).Msg("Cannot write error result — no triage job found")
		return nil
	}

	result := &store.FileResult{
		Filename:    filename,
		Status:      "invalid",
		OriginalKey: originalKey,
		Error:       errMsg,
	}
	if err := fileProcessStore.PutFileResult(ctx, sessionID, jobID, result); err != nil {
		log.Error().Err(err).Str("filename", filename).Msg("Failed to write error result to DDB")
	}

	// Still increment count so the SFN doesn't wait forever
	if _, err := sessionStore.IncrementTriageProcessedCount(ctx, sessionID, jobID); err != nil {
		log.Error().Err(err).Str("filename", filename).Msg("Failed to increment processedCount for error result")
	}

	return nil
}
