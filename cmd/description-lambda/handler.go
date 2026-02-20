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

func handleDescription(ctx context.Context, event DescriptionEvent) error {
	jobStart := time.Now()
	sessionStore.PutDescriptionJob(ctx, event.SessionID, &store.DescriptionJob{
		ID: event.JobID, Status: "processing", GroupLabel: event.GroupLabel,
		TripContext: event.TripContext, MediaKeys: event.Keys,
	})

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

	mediaItems, err := buildDescriptionMediaItems(ctx, event.Keys)
	if err != nil {
		return jobutil.SetJobError(ctx, event.SessionID, event.JobID, "failed to prepare media", func(ctx context.Context, sessionID, jobID, errMsg string) error {
			sessionStore.PutDescriptionJob(ctx, sessionID, &store.DescriptionJob{ID: jobID, Status: "error", Error: errMsg})
			return nil
		})
	}

	// DDR-065: Create CacheManager for context caching within description.
	cacheMgr := chat.NewCacheManager(genaiClient)
	defer cacheMgr.DeleteAll(ctx, event.SessionID)

	result, rawResponse, err := chat.GenerateDescription(
		ctx, genaiClient, event.GroupLabel, event.TripContext, mediaItems,
		cacheMgr, event.SessionID,
	)
	if err != nil {
		return jobutil.SetJobError(ctx, event.SessionID, event.JobID, "caption generation failed", func(ctx context.Context, sessionID, jobID, errMsg string) error {
			sessionStore.PutDescriptionJob(ctx, sessionID, &store.DescriptionJob{ID: jobID, Status: "error", Error: errMsg})
			return nil
		})
	}

	sessionStore.PutDescriptionJob(ctx, event.SessionID, &store.DescriptionJob{
		ID: event.JobID, Status: "complete", GroupLabel: event.GroupLabel,
		TripContext: event.TripContext, MediaKeys: event.Keys,
		Caption: result.Caption, Hashtags: result.Hashtags,
		LocationTag: result.LocationTag, RawResponse: rawResponse,
	})

	log.Info().Str("job", event.JobID).Int("caption_length", len(result.Caption)).Dur("duration", time.Since(jobStart)).Msg("Description generation complete")
	return nil
}
