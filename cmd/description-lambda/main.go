// Package main provides a Lambda entry point for description generation (DDR-053).
//
// This Lambda handles AI-powered Instagram caption generation:
//   - description: Generate a caption from media thumbnails
//   - description-feedback: Regenerate a caption with user feedback
//
// Invoked asynchronously by the API Lambda via lambda:Invoke (Event type).
//
// Container: Light (Dockerfile.light — no ffmpeg needed)
// Memory: 2 GB
// Timeout: 5 minutes
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/rs/zerolog/log"

	"github.com/fpang/gemini-media-cli/internal/chat"
	"github.com/fpang/gemini-media-cli/internal/filehandler"
	"github.com/fpang/gemini-media-cli/internal/lambdaboot"
	"github.com/fpang/gemini-media-cli/internal/logging"
	"github.com/fpang/gemini-media-cli/internal/store"
)

var coldStart = true

var (
	s3Client     *s3.Client
	mediaBucket  string
	sessionStore *store.DynamoStore
)

func init() {
	initStart := time.Now()
	logging.Init()

	aws := lambdaboot.InitAWS()
	s3s := lambdaboot.InitS3(aws.Config, "MEDIA_BUCKET_NAME")
	s3Client = s3s.Client
	mediaBucket = s3s.Bucket
	sessionStore = lambdaboot.InitDynamo(aws.Config, "DYNAMO_TABLE_NAME")
	lambdaboot.LoadGeminiKey(aws.SSM)

	lambdaboot.StartupLog("description-lambda", initStart).
		S3Bucket("mediaBucket", mediaBucket).
		DynamoTable("sessions", os.Getenv("DYNAMO_TABLE_NAME")).
		SSMParam("geminiApiKey", logging.EnvOrDefault("SSM_API_KEY_PARAM", "/ai-social-media/prod/gemini-api-key")).
		Log()
}

func main() {
	lambda.Start(handler)
}

// DescriptionEvent is the input from the API Lambda.
type DescriptionEvent struct {
	Type        string   `json:"type"`
	SessionID   string   `json:"sessionId"`
	JobID       string   `json:"jobId"`
	Keys        []string `json:"keys,omitempty"`
	GroupLabel  string   `json:"groupLabel,omitempty"`
	TripContext string   `json:"tripContext,omitempty"`
	Feedback    string   `json:"feedback,omitempty"`
}

func handler(ctx context.Context, event DescriptionEvent) error {
	if coldStart {
		coldStart = false
		log.Info().Str("function", "description-lambda").Msg("Cold start — first invocation")
	}
	log.Info().
		Str("type", event.Type).
		Str("sessionId", event.SessionID).
		Str("jobId", event.JobID).
		Msg("Description Lambda invoked")

	switch event.Type {
	case "description":
		return handleDescription(ctx, event)
	case "description-feedback":
		return handleDescriptionFeedback(ctx, event)
	default:
		return fmt.Errorf("unknown event type: %s", event.Type)
	}
}

func handleDescription(ctx context.Context, event DescriptionEvent) error {
	jobStart := time.Now()
	sessionStore.PutDescriptionJob(ctx, event.SessionID, &store.DescriptionJob{
		ID: event.JobID, Status: "processing", GroupLabel: event.GroupLabel,
		TripContext: event.TripContext, MediaKeys: event.Keys,
	})

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return setDescError(ctx, event, "API key not configured")
	}

	genaiClient, err := chat.NewGeminiClient(ctx, apiKey)
	if err != nil {
		return setDescError(ctx, event, "failed to initialize AI client")
	}

	mediaItems, err := buildDescriptionMediaItems(ctx, event.Keys)
	if err != nil {
		return setDescError(ctx, event, "failed to prepare media")
	}

	result, rawResponse, err := chat.GenerateDescription(
		ctx, genaiClient, event.GroupLabel, event.TripContext, mediaItems,
	)
	if err != nil {
		return setDescError(ctx, event, "caption generation failed")
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

func handleDescriptionFeedback(ctx context.Context, event DescriptionEvent) error {
	jobStart := time.Now()
	job, err := sessionStore.GetDescriptionJob(ctx, event.SessionID, event.JobID)
	if err != nil || job == nil {
		return setDescError(ctx, event, "job not found")
	}

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return setDescError(ctx, event, "API key not configured")
	}

	genaiClient, err := chat.NewGeminiClient(ctx, apiKey)
	if err != nil {
		return setDescError(ctx, event, "failed to initialize AI client")
	}

	mediaItems, err := buildDescriptionMediaItems(ctx, job.MediaKeys)
	if err != nil {
		return setDescError(ctx, event, "failed to prepare media")
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
		return setDescError(ctx, event, "caption regeneration failed")
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

func setDescError(ctx context.Context, event DescriptionEvent, msg string) error {
	log.Error().Str("job", event.JobID).Str("error", msg).Msg("Description job failed")
	sessionStore.PutDescriptionJob(ctx, event.SessionID, &store.DescriptionJob{
		ID: event.JobID, Status: "error", Error: msg,
	})
	return nil
}

// --- S3 Helpers ---

func downloadFromS3(ctx context.Context, key string) (string, func(), error) {
	tmpFile, err := os.CreateTemp("", "media-*"+filepath.Ext(key))
	if err != nil {
		return "", nil, fmt.Errorf("create temp file: %w", err)
	}

	result, err := s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &mediaBucket, Key: &key,
	})
	if err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return "", nil, fmt.Errorf("S3 GetObject: %w", err)
	}
	defer result.Body.Close()

	buf := make([]byte, 32*1024)
	for {
		n, readErr := result.Body.Read(buf)
		if n > 0 {
			if _, writeErr := tmpFile.Write(buf[:n]); writeErr != nil {
				tmpFile.Close()
				os.Remove(tmpFile.Name())
				return "", nil, fmt.Errorf("write: %w", writeErr)
			}
		}
		if readErr != nil {
			if readErr.Error() == "EOF" {
				break
			}
			tmpFile.Close()
			os.Remove(tmpFile.Name())
			return "", nil, fmt.Errorf("download: %w", readErr)
		}
	}
	tmpFile.Close()

	cleanup := func() { os.Remove(tmpFile.Name()) }
	return tmpFile.Name(), cleanup, nil
}

func generateThumbnailFromBytes(imageData []byte, mimeType string, maxDimension int) ([]byte, string, error) {
	tmpFile, err := os.CreateTemp("", "thumb-*")
	if err != nil {
		return nil, "", err
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.Write(imageData); err != nil {
		tmpFile.Close()
		return nil, "", err
	}
	tmpFile.Close()

	info, _ := os.Stat(tmpPath)
	mf := &filehandler.MediaFile{
		Path: tmpPath, MIMEType: mimeType, Size: info.Size(),
	}
	return filehandler.GenerateThumbnail(mf, maxDimension)
}

func buildDescriptionMediaItems(ctx context.Context, keys []string) ([]chat.DescriptionMediaItem, error) {
	log.Debug().Int("keyCount", len(keys)).Msg("Building description media items")
	var items []chat.DescriptionMediaItem

	for _, key := range keys {
		filename := filepath.Base(key)
		ext := strings.ToLower(filepath.Ext(key))

		item := chat.DescriptionMediaItem{Filename: filename}

		if filehandler.IsImage(ext) {
			item.Type = "Photo"
			parts := strings.SplitN(key, "/", 2)
			thumbKey := fmt.Sprintf("%s/thumbnails/%s.jpg", parts[0], strings.TrimSuffix(filename, ext))

			tmpPath, cleanup, err := downloadFromS3(ctx, thumbKey)
			if err != nil {
				origPath, origCleanup, origErr := downloadFromS3(ctx, key)
				if origErr != nil {
					log.Warn().Str("key", key).Err(origErr).Msg("Skipping: failed to download original")
					continue
				}
				defer origCleanup()

				origData, readErr := os.ReadFile(origPath)
				if readErr != nil {
					log.Warn().Str("key", key).Err(readErr).Msg("Skipping: failed to read original")
					continue
				}

				mime := "image/jpeg"
				if m, ok := filehandler.SupportedImageExtensions[ext]; ok {
					mime = m
				}

				thumbData, thumbMIME, thumbErr := generateThumbnailFromBytes(origData, mime, filehandler.DefaultThumbnailMaxDimension)
				if thumbErr != nil {
					log.Warn().Str("key", key).Err(thumbErr).Msg("Skipping: failed to generate thumbnail")
					continue
				}
				item.ThumbnailData = thumbData
				item.ThumbnailMIMEType = thumbMIME
			} else {
				defer cleanup()
				thumbData, err := os.ReadFile(tmpPath)
				if err != nil {
					log.Warn().Str("key", key).Err(err).Msg("Skipping: failed to read thumbnail")
					continue
				}
				item.ThumbnailData = thumbData
				item.ThumbnailMIMEType = "image/jpeg"
			}
		} else if filehandler.IsVideo(ext) {
			item.Type = "Video"
			parts := strings.SplitN(key, "/", 2)
			thumbKey := fmt.Sprintf("%s/thumbnails/%s.jpg", parts[0], strings.TrimSuffix(filename, ext))

			tmpPath, cleanup, err := downloadFromS3(ctx, thumbKey)
			if err != nil {
				log.Warn().Str("key", key).Err(err).Msg("Skipping: failed to download video thumbnail")
				continue
			}
			defer cleanup()

			thumbData, err := os.ReadFile(tmpPath)
			if err != nil {
				log.Warn().Str("key", key).Err(err).Msg("Skipping: failed to read video thumbnail")
				continue
			}
			item.ThumbnailData = thumbData
			item.ThumbnailMIMEType = "image/jpeg"
		} else {
			log.Warn().Str("key", key).Str("ext", ext).Msg("Skipping: unsupported file type")
			continue
		}

		items = append(items, item)
	}

	if len(items) == 0 {
		return nil, fmt.Errorf("no media items could be prepared for description")
	}
	return items, nil
}
