// Package main provides a Lambda entry point for AI-powered media selection.
//
// This Lambda is invoked by the Step Functions SelectionPipeline after all
// thumbnails are generated (fan-in). It downloads all media files and
// thumbnails, sends them to Gemini for structured JSON selection analysis,
// and writes the complete results to DynamoDB.
//
// Container: Heavy (Dockerfile.heavy — includes ffmpeg for video compression)
// Memory: 4 GB
// Timeout: 15 minutes
//
// See DDR-035: Multi-Lambda Deployment Architecture
// See DDR-043: Step Functions Lambda Entrypoints
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/fpang/gemini-media-cli/internal/chat"
	"github.com/fpang/gemini-media-cli/internal/filehandler"
	"github.com/fpang/gemini-media-cli/internal/logging"
	"github.com/fpang/gemini-media-cli/internal/store"
	"github.com/rs/zerolog/log"
)

// AWS clients and configuration initialized at cold start.
var (
	s3Client     *s3.Client
	sessionStore store.SessionStore
	mediaBucket  string
)

var coldStart = true

func init() {
	initStart := time.Now()
	logging.Init()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load AWS config")
	}
	log.Debug().Str("region", cfg.Region).Msg("AWS config loaded")

	s3Client = s3.NewFromConfig(cfg)
	mediaBucket = os.Getenv("MEDIA_BUCKET_NAME")
	if mediaBucket == "" {
		log.Fatal().Msg("MEDIA_BUCKET_NAME environment variable is required")
	}

	// Initialize DynamoDB store.
	tableName := os.Getenv("DYNAMO_TABLE_NAME")
	if tableName == "" {
		tableName = "media-selection-sessions"
	}
	ddbClient := dynamodb.NewFromConfig(cfg)
	sessionStore = store.NewDynamoStore(ddbClient, tableName)

	// Load Gemini API key from SSM Parameter Store if not set.
	if os.Getenv("GEMINI_API_KEY") == "" {
		ssmClient := ssm.NewFromConfig(cfg)
		paramName := os.Getenv("SSM_API_KEY_PARAM")
		if paramName == "" {
			paramName = "/ai-social-media/prod/gemini-api-key"
		}
		ssmStart := time.Now()
		result, err := ssmClient.GetParameter(context.Background(), &ssm.GetParameterInput{
			Name:           &paramName,
			WithDecryption: aws.Bool(true),
		})
		if err != nil {
			log.Fatal().Err(err).Str("param", paramName).Msg("Failed to read API key from SSM")
		}
		os.Setenv("GEMINI_API_KEY", *result.Parameter.Value)
		log.Debug().Str("param", paramName).Dur("elapsed", time.Since(ssmStart)).Msg("Gemini API key loaded from SSM")
	}

	// Emit consolidated cold-start log for troubleshooting.
	logging.NewStartupLogger("selection-lambda").
		InitDuration(time.Since(initStart)).
		S3Bucket("mediaBucket", mediaBucket).
		DynamoTable("sessions", tableName).
		SSMParam("geminiApiKey", logging.EnvOrDefault("SSM_API_KEY_PARAM", "/ai-social-media/prod/gemini-api-key")).
		Log()
}

// SelectionEvent is the input payload from Step Functions.
// It is produced by the state machine after the thumbnail Map state completes.
type SelectionEvent struct {
	SessionID     string           `json:"sessionId"`
	JobID         string           `json:"jobId"`
	TripContext   string           `json:"tripContext"`
	Model         string           `json:"model,omitempty"`
	MediaKeys     []string         `json:"mediaKeys"`
	ThumbnailKeys []ThumbnailEntry `json:"thumbnailKeys"`
	Bucket        string           `json:"bucket,omitempty"`
}

// ThumbnailEntry pairs an original media key with its generated thumbnail key.
type ThumbnailEntry struct {
	ThumbnailKey string `json:"thumbnailKey"`
	OriginalKey  string `json:"originalKey"`
}

// SelectionResult is the output returned to Step Functions.
type SelectionResult struct {
	JobID           string `json:"jobId"`
	SelectedCount   int    `json:"selectedCount"`
	ExcludedCount   int    `json:"excludedCount"`
	SceneGroupCount int    `json:"sceneGroupCount"`
	Error           string `json:"error,omitempty"`
}

func handler(ctx context.Context, event SelectionEvent) (SelectionResult, error) {
	handlerStart := time.Now()
	if coldStart {
		coldStart = false
		log.Info().Str("function", "selection-lambda").Msg("Cold start — first invocation")
	}
	bucket := mediaBucket
	if event.Bucket != "" {
		bucket = event.Bucket
	}

	logger := log.With().
		Str("sessionId", event.SessionID).
		Str("jobId", event.JobID).
		Int("mediaCount", len(event.MediaKeys)).
		Logger()

	logger.Info().
		Str("tripContext", event.TripContext).
		Str("model", event.Model).
		Int("thumbnailCount", len(event.ThumbnailKeys)).
		Str("bucket", bucket).
		Msg("Starting AI media selection")

	// Validate input.
	logger.Debug().
		Bool("hasSessionID", event.SessionID != "").
		Bool("hasJobID", event.JobID != "").
		Bool("hasMediaKeys", len(event.MediaKeys) > 0).
		Msg("Validating event fields")
	if event.SessionID == "" || event.JobID == "" {
		return SelectionResult{Error: "sessionId and jobId are required"},
			fmt.Errorf("sessionId and jobId are required")
	}
	if len(event.MediaKeys) == 0 {
		return SelectionResult{Error: "no media keys provided"},
			fmt.Errorf("no media keys provided")
	}

	model := chat.DefaultModelName
	if event.Model != "" {
		model = event.Model
	}

	// Update job status to "processing" in DynamoDB.
	selJob := &store.SelectionJob{
		ID:     event.JobID,
		Status: "processing",
	}
	logger.Debug().Str("status", "processing").Msg("Updating DynamoDB job status")
	if err := sessionStore.PutSelectionJob(ctx, event.SessionID, selJob); err != nil {
		logger.Error().Err(err).Msg("Failed to update job status")
		// Non-fatal — continue processing even if status update fails.
	}

	// Download media files and create MediaFile objects.
	tmpDir := filepath.Join(os.TempDir(), "selection", event.SessionID)
	os.MkdirAll(tmpDir, 0755)
	defer os.RemoveAll(tmpDir)

	var allMediaFiles []*filehandler.MediaFile
	var s3Keys []string

	for _, key := range event.MediaKeys {
		filename := filepath.Base(key)
		ext := strings.ToLower(filepath.Ext(filename))

		if !filehandler.IsSupported(ext) {
			logger.Debug().Str("key", key).Msg("Skipping unsupported file type")
			continue
		}

		localPath := filepath.Join(tmpDir, filename)
		if err := downloadToFile(ctx, bucket, key, localPath); err != nil {
			logger.Warn().Err(err).Str("key", key).Msg("Failed to download file, skipping")
			continue
		}

		mf, err := filehandler.LoadMediaFile(localPath)
		if err != nil {
			logger.Warn().Err(err).Str("key", key).Msg("Failed to load media file, skipping")
			continue
		}

		allMediaFiles = append(allMediaFiles, mf)
		s3Keys = append(s3Keys, key)
	}

	if len(allMediaFiles) == 0 {
		errMsg := "no supported media files found"
		selJob.Status = "error"
		selJob.Error = errMsg
		sessionStore.PutSelectionJob(ctx, event.SessionID, selJob)
		return SelectionResult{JobID: event.JobID, Error: errMsg},
			fmt.Errorf("%s", errMsg)
	}

	logger.Info().Int("count", len(allMediaFiles)).Msg("Loaded media files, calling Gemini")

	// Initialize Gemini client and run selection.
	apiKey := os.Getenv("GEMINI_API_KEY")
	logger.Debug().Str("model", model).Msg("Calling Gemini API for media selection")
	client, err := chat.NewGeminiClient(ctx, apiKey)
	if err != nil {
		errMsg := fmt.Sprintf("failed to create Gemini client: %v", err)
		selJob.Status = "error"
		selJob.Error = errMsg
		sessionStore.PutSelectionJob(ctx, event.SessionID, selJob)
		return SelectionResult{JobID: event.JobID, Error: errMsg}, err
	}

	selResult, err := chat.AskMediaSelectionJSON(ctx, client, allMediaFiles, event.TripContext, model)
	if err != nil {
		errMsg := fmt.Sprintf("selection failed: %v", err)
		selJob.Status = "error"
		selJob.Error = errMsg
		sessionStore.PutSelectionJob(ctx, event.SessionID, selJob)
		return SelectionResult{JobID: event.JobID, Error: errMsg}, err
	}

	// Map results to items with S3 keys and thumbnail URLs.
	for _, sel := range selResult.Selected {
		idx := sel.Media - 1
		if idx < 0 || idx >= len(allMediaFiles) {
			logger.Warn().Int("mediaIndex", idx+1).Int("maxIndex", len(allMediaFiles)).Msg("Skipping result with out-of-bounds media index")
			continue
		}
		key := s3Keys[idx]
		thumbKey := fmt.Sprintf("%s/thumbnails/%s.jpg", event.SessionID,
			strings.TrimSuffix(filepath.Base(key), filepath.Ext(key)))
		selJob.Selected = append(selJob.Selected, store.SelectedItem{
			Rank:           sel.Rank,
			Media:          sel.Media,
			Filename:       sel.Filename,
			Key:            key,
			Type:           sel.Type,
			Scene:          sel.Scene,
			Justification:  sel.Justification,
			ComparisonNote: sel.ComparisonNote,
			ThumbnailURL:   fmt.Sprintf("/api/media/thumbnail?key=%s", thumbKey),
		})
	}

	for _, exc := range selResult.Excluded {
		idx := exc.Media - 1
		if idx < 0 || idx >= len(allMediaFiles) {
			logger.Warn().Int("mediaIndex", idx+1).Int("maxIndex", len(allMediaFiles)).Msg("Skipping result with out-of-bounds media index")
			continue
		}
		key := s3Keys[idx]
		thumbKey := fmt.Sprintf("%s/thumbnails/%s.jpg", event.SessionID,
			strings.TrimSuffix(filepath.Base(key), filepath.Ext(key)))
		selJob.Excluded = append(selJob.Excluded, store.ExcludedItem{
			Media:        exc.Media,
			Filename:     exc.Filename,
			Key:          key,
			Reason:       exc.Reason,
			Category:     exc.Category,
			DuplicateOf:  exc.DuplicateOf,
			ThumbnailURL: fmt.Sprintf("/api/media/thumbnail?key=%s", thumbKey),
		})
	}

	for _, sg := range selResult.SceneGroups {
		group := store.SceneGroup{
			Name:      sg.Name,
			GPS:       sg.GPS,
			TimeRange: sg.TimeRange,
		}
		for _, item := range sg.Items {
			idx := item.Media - 1
			if idx < 0 || idx >= len(allMediaFiles) {
				logger.Warn().Int("mediaIndex", idx+1).Int("maxIndex", len(allMediaFiles)).Msg("Skipping result with out-of-bounds media index")
				continue
			}
			key := s3Keys[idx]
			thumbKey := fmt.Sprintf("%s/thumbnails/%s.jpg", event.SessionID,
				strings.TrimSuffix(filepath.Base(key), filepath.Ext(key)))
			group.Items = append(group.Items, store.SceneGroupItem{
				Media:        item.Media,
				Filename:     item.Filename,
				Key:          key,
				Type:         item.Type,
				Selected:     item.Selected,
				Description:  item.Description,
				ThumbnailURL: fmt.Sprintf("/api/media/thumbnail?key=%s", thumbKey),
			})
		}
		selJob.SceneGroups = append(selJob.SceneGroups, group)
	}

	// Write completed results to DynamoDB.
	selJob.Status = "complete"
	if err := sessionStore.PutSelectionJob(ctx, event.SessionID, selJob); err != nil {
		errMsg := fmt.Sprintf("failed to write results to DynamoDB: %v", err)
		logger.Error().Err(err).Msg(errMsg)
		return SelectionResult{JobID: event.JobID, Error: errMsg}, err
	}

	logger.Info().
		Int("selected", len(selJob.Selected)).
		Int("excluded", len(selJob.Excluded)).
		Int("scenes", len(selJob.SceneGroups)).
		Dur("duration", time.Since(handlerStart)).
		Msg("Selection complete, results written to DynamoDB")

	return SelectionResult{
		JobID:           event.JobID,
		SelectedCount:   len(selJob.Selected),
		ExcludedCount:   len(selJob.Excluded),
		SceneGroupCount: len(selJob.SceneGroups),
	}, nil
}

func main() {
	lambda.Start(handler)
}

// --- S3 Helpers ---

// downloadToFile downloads an S3 object to a specific local path.
func downloadToFile(ctx context.Context, bucket, key, localPath string) error {
	log.Debug().Str("bucket", bucket).Str("key", key).Str("localPath", localPath).Msg("Downloading file from S3")
	result, err := s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		return fmt.Errorf("S3 GetObject: %w", err)
	}
	defer result.Body.Close()

	f, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	buf := make([]byte, 32*1024)
	for {
		n, readErr := result.Body.Read(buf)
		if n > 0 {
			if _, writeErr := f.Write(buf[:n]); writeErr != nil {
				return fmt.Errorf("write: %w", writeErr)
			}
		}
		if readErr != nil {
			if readErr.Error() == "EOF" {
				break
			}
			return fmt.Errorf("read: %w", readErr)
		}
	}
	return nil
}
