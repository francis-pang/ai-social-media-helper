package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	lambdasvc "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/rs/zerolog/log"

	"github.com/fpang/gemini-media-cli/internal/chat"
	"github.com/fpang/gemini-media-cli/internal/filehandler"
	"github.com/fpang/gemini-media-cli/internal/jobutil"
	"github.com/fpang/gemini-media-cli/internal/rag"
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

	// RAG retrieval — best effort
	ragContext := ""
	if ragQueryArn != "" {
		sessionContext := event.GroupLabel
		if event.TripContext != "" {
			sessionContext = event.TripContext + "\n" + event.GroupLabel
		}
		ragCtx, ragErr := invokeRAGQuery(ctx, "caption", event.SessionID, sessionContext)
		if ragErr != nil {
			log.Warn().Err(ragErr).Msg("RAG query failed, proceeding without context")
		} else {
			ragContext = ragCtx
		}
	}

	// DDR-065: Create CacheManager for context caching within description.
	cacheMgr := chat.NewCacheManager(genaiClient)
	defer cacheMgr.DeleteAll(ctx, event.SessionID)

	result, rawResponse, err := chat.GenerateDescription(
		ctx, genaiClient, event.GroupLabel, event.TripContext, mediaItems,
		cacheMgr, event.SessionID, ragContext,
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

	// Emit description decisions to EventBridge — best effort
	if ebClient != nil && len(event.Keys) > 0 {
		metadata := map[string]string{
			"caption":      result.Caption,
			"locationTag": result.LocationTag,
		}
		if len(result.Hashtags) > 0 {
			metadata["hashtags"] = strings.Join(result.Hashtags, ",")
		}
		for _, key := range event.Keys {
			mediaType := "Photo"
			if ext := strings.ToLower(filepath.Ext(key)); filehandler.IsVideo(ext) {
				mediaType = "Video"
			}
			feedback := rag.ContentFeedback{
				EventType:   rag.EventDescriptionFinalized,
				SessionID:   event.SessionID,
				JobID:       event.JobID,
				Timestamp:   time.Now().UTC().Format(time.RFC3339),
				UserID:      event.SessionID,
				MediaKey:    key,
				MediaType:   mediaType,
				AIVerdict:   "captioned",
				UserVerdict: "captioned",
				Reason:      result.Caption,
				Model:       "gemini",
				Metadata:    metadata,
			}
			if err := rag.EmitContentFeedback(ctx, ebClient, feedback); err != nil {
				log.Warn().Err(err).Str("key", key).Msg("failed to emit description feedback")
			}
		}
	}

	log.Info().Str("job", event.JobID).Int("caption_length", len(result.Caption)).Dur("duration", time.Since(jobStart)).Msg("Description generation complete")
	return nil
}

func invokeRAGQuery(ctx context.Context, queryType, userID, sessionContext string) (string, error) {
	if lambdaClient == nil || ragQueryArn == "" {
		return "", nil
	}
	payload, _ := json.Marshal(map[string]string{
		"queryType":      queryType,
		"userId":         userID,
		"sessionContext": sessionContext,
	})
	result, err := lambdaClient.Invoke(ctx, &lambdasvc.InvokeInput{
		FunctionName: &ragQueryArn,
		Payload:      payload,
	})
	if err != nil {
		return "", err
	}
	var resp struct {
		RAGContext string `json:"ragContext"`
	}
	if err := json.Unmarshal(result.Payload, &resp); err != nil {
		return "", err
	}
	return resp.RAGContext, nil
}
