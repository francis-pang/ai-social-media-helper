package main

import (
	"context"
	"os"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/fpang/gemini-media-cli/internal/chat"
	"github.com/fpang/gemini-media-cli/internal/jobutil"
	"github.com/fpang/gemini-media-cli/internal/store"
)

func handleDescriptionFeedback(ctx context.Context, event DescriptionEvent) error {
	jobStart := time.Now()
	job, err := sessionStore.GetDescriptionJob(ctx, event.SessionID, event.JobID)
	if err != nil || job == nil {
		return jobutil.SetJobError(ctx, event.SessionID, event.JobID, "job not found", func(ctx context.Context, sessionID, jobID, errMsg string) error {
			sessionStore.PutDescriptionJob(ctx, sessionID, &store.DescriptionJob{ID: jobID, Status: "error", Error: errMsg})
			return nil
		})
	}

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return jobutil.SetJobError(ctx, event.SessionID, event.JobID, "API key not configured", func(ctx context.Context, sessionID, jobID, errMsg string) error {
			sessionStore.PutDescriptionJob(ctx, sessionID, &store.DescriptionJob{ID: jobID, Status: "error", Error: errMsg})
			return nil
		})
	}

	genaiClient, err := chat.NewGeminiClient(ctx, apiKey)
	if err != nil {
		return jobutil.SetJobError(ctx, event.SessionID, event.JobID, "failed to initialize AI client", func(ctx context.Context, sessionID, jobID, errMsg string) error {
			sessionStore.PutDescriptionJob(ctx, sessionID, &store.DescriptionJob{ID: jobID, Status: "error", Error: errMsg})
			return nil
		})
	}

	mediaItems, err := buildDescriptionMediaItems(ctx, job.MediaKeys)
	if err != nil {
		return jobutil.SetJobError(ctx, event.SessionID, event.JobID, "failed to prepare media", func(ctx context.Context, sessionID, jobID, errMsg string) error {
			sessionStore.PutDescriptionJob(ctx, sessionID, &store.DescriptionJob{ID: jobID, Status: "error", Error: errMsg})
			return nil
		})
	}

	// Build history from current job state.
	var history []chat.DescriptionConversationEntry
	for _, h := range job.History {
		history = append(history, chat.DescriptionConversationEntry{
			UserFeedback:  h.UserFeedback,
			ModelResponse: h.ModelResponse,
		})
	}
	history = append(history, chat.DescriptionConversationEntry{
		UserFeedback:  event.Feedback,
		ModelResponse: job.RawResponse,
	})

	result, rawResponse, err := chat.RegenerateDescription(
		ctx, genaiClient, job.GroupLabel, job.TripContext, mediaItems,
		event.Feedback, history,
	)
	if err != nil {
		return jobutil.SetJobError(ctx, event.SessionID, event.JobID, "caption regeneration failed", func(ctx context.Context, sessionID, jobID, errMsg string) error {
			sessionStore.PutDescriptionJob(ctx, sessionID, &store.DescriptionJob{ID: jobID, Status: "error", Error: errMsg})
			return nil
		})
	}

	// Persist updated history.
	var storeHistory []store.ConversationEntry
	for _, h := range history {
		storeHistory = append(storeHistory, store.ConversationEntry{
			UserFeedback:  h.UserFeedback,
			ModelResponse: h.ModelResponse,
		})
	}

	sessionStore.PutDescriptionJob(ctx, event.SessionID, &store.DescriptionJob{
		ID: event.JobID, Status: "complete", GroupLabel: job.GroupLabel,
		TripContext: job.TripContext, MediaKeys: job.MediaKeys,
		Caption: result.Caption, Hashtags: result.Hashtags,
		LocationTag: result.LocationTag, RawResponse: rawResponse,
		History: storeHistory,
	})

	log.Info().Str("job", event.JobID).Int("round", len(storeHistory)).Dur("duration", time.Since(jobStart)).Msg("Description regeneration complete")
	return nil
}
