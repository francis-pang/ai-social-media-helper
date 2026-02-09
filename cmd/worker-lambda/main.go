// Package main provides a Worker Lambda entry point for async job processing (DDR-050).
//
// The Worker Lambda handles job types that don't require Step Functions orchestration:
// triage, description, download, and publish. It is invoked asynchronously by the
// API Lambda via lambda:Invoke with InvocationType=Event.
//
// Each job type reads its input from the event, processes the work, and writes
// results to DynamoDB via the session store. The API Lambda polls DynamoDB
// for status updates.
//
// Event format:
//
//	{
//	  "type": "triage"|"description"|"description-feedback"|"download"|"publish"|"enhancement-feedback",
//	  "sessionId": "uuid",
//	  "jobId": "triage-xxx",
//	  ...type-specific fields
//	}
package main

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/klauspost/compress/zstd"
	"github.com/rs/zerolog/log"

	"github.com/fpang/gemini-media-cli/internal/chat"
	"github.com/fpang/gemini-media-cli/internal/filehandler"
	"github.com/fpang/gemini-media-cli/internal/instagram"
	"github.com/fpang/gemini-media-cli/internal/logging"
	"github.com/fpang/gemini-media-cli/internal/metrics"
	"github.com/fpang/gemini-media-cli/internal/store"
)

// AWS clients initialized at cold start.
var (
	s3Client     *s3.Client
	presigner    *s3.PresignClient
	mediaBucket  string
	sessionStore *store.DynamoStore
	igClient     *instagram.Client
)

// WorkerEvent is the top-level event received from the API Lambda.
type WorkerEvent struct {
	Type        string   `json:"type"`
	SessionID   string   `json:"sessionId"`
	JobID       string   `json:"jobId"`
	Model       string   `json:"model,omitempty"`
	Keys        []string `json:"keys,omitempty"`
	GroupLabel  string   `json:"groupLabel,omitempty"`
	TripContext string   `json:"tripContext,omitempty"`
	Feedback    string   `json:"feedback,omitempty"`
	GroupID     string   `json:"groupId,omitempty"`
	Caption     string   `json:"caption,omitempty"`
	Key         string   `json:"key,omitempty"`
}

// zipMethodZstd is the ZIP compression method ID for Zstandard.
const zipMethodZstd uint16 = 93

// maxVideoZipBytes is the maximum size of a single video ZIP bundle (375 MB).
const maxVideoZipBytes int64 = 375 * 1024 * 1024

func init() {
	logging.Init()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load AWS config")
	}

	s3Client = s3.NewFromConfig(cfg)
	presigner = s3.NewPresignClient(s3Client)
	mediaBucket = os.Getenv("MEDIA_BUCKET_NAME")
	if mediaBucket == "" {
		log.Fatal().Msg("MEDIA_BUCKET_NAME environment variable is required")
	}

	// Initialize DynamoDB session store.
	dynamoTableName := os.Getenv("DYNAMO_TABLE_NAME")
	if dynamoTableName == "" {
		log.Fatal().Msg("DYNAMO_TABLE_NAME environment variable is required")
	}
	ddbClient := dynamodb.NewFromConfig(cfg)
	sessionStore = store.NewDynamoStore(ddbClient, dynamoTableName)

	// Load Gemini API key from SSM Parameter Store.
	ssmClient := ssm.NewFromConfig(cfg)
	if os.Getenv("GEMINI_API_KEY") == "" {
		paramName := os.Getenv("SSM_API_KEY_PARAM")
		if paramName == "" {
			paramName = "/ai-social-media/prod/gemini-api-key"
		}
		result, err := ssmClient.GetParameter(context.Background(), &ssm.GetParameterInput{
			Name:           &paramName,
			WithDecryption: aws.Bool(true),
		})
		if err != nil {
			log.Fatal().Err(err).Str("param", paramName).Msg("Failed to read API key from SSM")
		}
		os.Setenv("GEMINI_API_KEY", *result.Parameter.Value)
		log.Info().Msg("Gemini API key loaded from SSM Parameter Store")
	}

	// Load Instagram credentials (optional — non-fatal).
	igAccessToken := os.Getenv("INSTAGRAM_ACCESS_TOKEN")
	igUserID := os.Getenv("INSTAGRAM_USER_ID")
	if igAccessToken == "" || igUserID == "" {
		tokenParam := os.Getenv("SSM_INSTAGRAM_TOKEN_PARAM")
		if tokenParam == "" {
			tokenParam = "/ai-social-media/prod/instagram-access-token"
		}
		userIDParam := os.Getenv("SSM_INSTAGRAM_USER_ID_PARAM")
		if userIDParam == "" {
			userIDParam = "/ai-social-media/prod/instagram-user-id"
		}
		tokenResult, err := ssmClient.GetParameter(context.Background(), &ssm.GetParameterInput{
			Name:           &tokenParam,
			WithDecryption: aws.Bool(true),
		})
		if err == nil {
			igAccessToken = *tokenResult.Parameter.Value
		}
		userIDResult, err := ssmClient.GetParameter(context.Background(), &ssm.GetParameterInput{
			Name:           &userIDParam,
			WithDecryption: aws.Bool(false),
		})
		if err == nil {
			igUserID = *userIDResult.Parameter.Value
		}
	}
	if igAccessToken != "" && igUserID != "" {
		igClient = instagram.NewClient(igAccessToken, igUserID)
		log.Info().Str("userId", igUserID).Msg("Instagram client initialized")
	}

	// Register Zstandard compressor for ZIP bundles (DDR-034).
	zip.RegisterCompressor(zipMethodZstd, func(w io.Writer) (io.WriteCloser, error) {
		return zstd.NewWriter(w, zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(12)))
	})
}

func main() {
	lambda.Start(handler)
}

func handler(ctx context.Context, event WorkerEvent) error {
	log.Info().
		Str("type", event.Type).
		Str("sessionId", event.SessionID).
		Str("jobId", event.JobID).
		Msg("Worker Lambda invoked")

	switch event.Type {
	case "triage":
		return handleTriage(ctx, event)
	case "description":
		return handleDescription(ctx, event)
	case "description-feedback":
		return handleDescriptionFeedback(ctx, event)
	case "download":
		return handleDownload(ctx, event)
	case "publish":
		return handlePublish(ctx, event)
	case "enhancement-feedback":
		return handleEnhancementFeedback(ctx, event)
	default:
		return fmt.Errorf("unknown event type: %s", event.Type)
	}
}

// ===== Triage Processing =====

func handleTriage(ctx context.Context, event WorkerEvent) error {
	jobStart := time.Now()

	// Update status to processing.
	sessionStore.PutTriageJob(ctx, event.SessionID, &store.TriageJob{
		ID: event.JobID, Status: "processing",
	})

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return setTriageError(ctx, event, "GEMINI_API_KEY not configured")
	}

	client, err := chat.NewGeminiClient(ctx, apiKey)
	if err != nil {
		return setTriageError(ctx, event, fmt.Sprintf("Failed to create Gemini client: %v", err))
	}

	// List S3 objects for the session.
	prefix := event.SessionID + "/"
	listResult, err := s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: &mediaBucket, Prefix: &prefix,
	})
	if err != nil {
		return setTriageError(ctx, event, fmt.Sprintf("Failed to list S3 objects: %v", err))
	}
	if len(listResult.Contents) == 0 {
		return setTriageError(ctx, event, "No files found for session")
	}

	// Download each file and create MediaFile objects.
	tmpDir := filepath.Join(os.TempDir(), "triage", event.SessionID)
	os.MkdirAll(tmpDir, 0755)
	defer os.RemoveAll(tmpDir)

	var allMediaFiles []*filehandler.MediaFile
	var s3Keys []string

	for _, obj := range listResult.Contents {
		key := *obj.Key
		filename := filepath.Base(key)
		ext := strings.ToLower(filepath.Ext(filename))

		if !filehandler.IsSupported(ext) {
			continue
		}

		localPath := filepath.Join(tmpDir, filename)
		if err := downloadToFile(ctx, key, localPath); err != nil {
			log.Warn().Err(err).Str("key", key).Msg("Failed to download file")
			continue
		}

		mf, err := filehandler.LoadMediaFile(localPath)
		if err != nil {
			log.Warn().Err(err).Str("key", key).Msg("Failed to load media file")
			continue
		}

		allMediaFiles = append(allMediaFiles, mf)
		s3Keys = append(s3Keys, key)
	}

	if len(allMediaFiles) == 0 {
		return setTriageError(ctx, event, "No supported media files found in the uploaded session")
	}

	model := event.Model
	if model == "" {
		model = chat.DefaultModelName
	}

	triageResults, err := chat.AskMediaTriage(ctx, client, allMediaFiles, model)
	if err != nil {
		return setTriageError(ctx, event, fmt.Sprintf("Triage failed: %v", err))
	}

	// Map results to store items.
	var keep, discard []store.TriageItem
	for _, tr := range triageResults {
		idx := tr.Media - 1
		if idx < 0 || idx >= len(allMediaFiles) {
			continue
		}
		key := s3Keys[idx]
		item := store.TriageItem{
			Media:        tr.Media,
			Filename:     tr.Filename,
			Key:          key,
			Saveable:     tr.Saveable,
			Reason:       tr.Reason,
			ThumbnailURL: fmt.Sprintf("/api/media/thumbnail?key=%s", key),
		}
		if tr.Saveable {
			keep = append(keep, item)
		} else {
			discard = append(discard, item)
		}
	}

	sessionStore.PutTriageJob(ctx, event.SessionID, &store.TriageJob{
		ID: event.JobID, Status: "complete", Keep: keep, Discard: discard,
	})

	log.Info().Int("keep", len(keep)).Int("discard", len(discard)).Msg("Triage complete")

	metrics.New("AiSocialMedia").
		Dimension("JobType", "triage").
		Metric("JobDurationMs", float64(time.Since(jobStart).Milliseconds()), metrics.UnitMilliseconds).
		Metric("JobFilesProcessed", float64(len(allMediaFiles)), metrics.UnitCount).
		Count("JobSuccess").
		Property("jobId", event.JobID).
		Property("sessionId", event.SessionID).
		Flush()

	return nil
}

func setTriageError(ctx context.Context, event WorkerEvent, msg string) error {
	log.Error().Str("job", event.JobID).Str("error", msg).Msg("Triage job failed")
	sessionStore.PutTriageJob(ctx, event.SessionID, &store.TriageJob{
		ID: event.JobID, Status: "error", Error: msg,
	})
	return nil // Return nil — error is stored in DynamoDB, not propagated to Lambda retry
}

// ===== Description Processing =====

func handleDescription(ctx context.Context, event WorkerEvent) error {
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

	log.Info().Str("job", event.JobID).Int("caption_length", len(result.Caption)).Msg("Description generation complete")
	return nil
}

func handleDescriptionFeedback(ctx context.Context, event WorkerEvent) error {
	// Read current job from DynamoDB.
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

	// Add current response to history.
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

	log.Info().Str("job", event.JobID).Int("round", len(storeHistory)).Msg("Description regeneration complete")
	return nil
}

func setDescError(ctx context.Context, event WorkerEvent, msg string) error {
	log.Error().Str("job", event.JobID).Str("error", msg).Msg("Description job failed")
	sessionStore.PutDescriptionJob(ctx, event.SessionID, &store.DescriptionJob{
		ID: event.JobID, Status: "error", Error: msg,
	})
	return nil
}

// ===== Download Processing =====

func handleDownload(ctx context.Context, event WorkerEvent) error {
	sessionStore.PutDownloadJob(ctx, event.SessionID, &store.DownloadJob{
		ID: event.JobID, Status: "processing",
	})

	// Step 1: Query file sizes and separate images from videos.
	var images, videos []dlFile

	for _, key := range event.Keys {
		headResult, err := s3Client.HeadObject(ctx, &s3.HeadObjectInput{
			Bucket: &mediaBucket, Key: &key,
		})
		if err != nil {
			log.Warn().Err(err).Str("key", key).Msg("HeadObject failed, skipping file")
			continue
		}
		size := *headResult.ContentLength
		ext := strings.ToLower(filepath.Ext(key))
		if filehandler.IsVideo(ext) {
			videos = append(videos, dlFile{key: key, size: size})
		} else {
			images = append(images, dlFile{key: key, size: size})
		}
	}

	if len(images) == 0 && len(videos) == 0 {
		return setDownloadError(ctx, event, "No downloadable files found")
	}

	// Step 2: Plan bundles.
	var bundles []store.DownloadBundle

	if len(images) > 0 {
		var totalSize int64
		for _, img := range images {
			totalSize += img.size
		}
		bundles = append(bundles, store.DownloadBundle{
			Type: "images", Name: sanitizeZipName(event.GroupLabel, "images", 0),
			FileCount: len(images), TotalSize: totalSize, Status: "pending",
		})
	}

	if len(videos) > 0 {
		videoGroups := dlGroupBySize(videos, maxVideoZipBytes)
		for i, group := range videoGroups {
			var totalSize int64
			for _, v := range group {
				totalSize += v.size
			}
			bundles = append(bundles, store.DownloadBundle{
				Type: "videos", Name: sanitizeZipName(event.GroupLabel, "videos", i+1),
				FileCount: len(group), TotalSize: totalSize, Status: "pending",
			})
		}
	}

	// Step 3: Create each ZIP bundle.
	videoGroupIdx := 0
	videoGroups := dlGroupBySize(videos, maxVideoZipBytes)

	for i := range bundles {
		bundles[i].Status = "processing"

		var filesToZip []dlFile
		if bundles[i].Type == "images" {
			filesToZip = images
		} else {
			filesToZip = videoGroups[videoGroupIdx]
			videoGroupIdx++
		}

		zipKey := fmt.Sprintf("%s/downloads/%s/%s", event.SessionID, event.JobID, bundles[i].Name)
		zipSize, err := dlCreateZip(ctx, filesToZip, zipKey)
		if err != nil {
			bundles[i].Status = "error"
			bundles[i].Error = err.Error()
			continue
		}

		downloadResult, err := presigner.PresignGetObject(ctx, &s3.GetObjectInput{
			Bucket:                     &mediaBucket,
			Key:                        &zipKey,
			ResponseContentDisposition: aws.String(fmt.Sprintf(`attachment; filename="%s"`, bundles[i].Name)),
		}, s3.WithPresignExpires(1*time.Hour))
		if err != nil {
			bundles[i].Status = "error"
			bundles[i].Error = "failed to generate download URL"
			continue
		}

		bundles[i].ZipKey = zipKey
		bundles[i].ZipSize = zipSize
		bundles[i].DownloadURL = downloadResult.URL
		bundles[i].Status = "complete"
	}

	sessionStore.PutDownloadJob(ctx, event.SessionID, &store.DownloadJob{
		ID: event.JobID, Status: "complete", Bundles: bundles,
	})

	log.Info().Str("job", event.JobID).Int("bundles", len(bundles)).Msg("Download job complete")
	return nil
}

func setDownloadError(ctx context.Context, event WorkerEvent, msg string) error {
	log.Error().Str("job", event.JobID).Str("error", msg).Msg("Download job failed")
	sessionStore.PutDownloadJob(ctx, event.SessionID, &store.DownloadJob{
		ID: event.JobID, Status: "error", Error: msg,
	})
	return nil
}

// ===== Publish Processing =====

func handlePublish(ctx context.Context, event WorkerEvent) error {
	if igClient == nil {
		return setPublishError(ctx, event, "Instagram client not configured")
	}

	sessionStore.PutPublishJob(ctx, event.SessionID, &store.PublishJob{
		ID: event.JobID, GroupID: event.GroupID, Status: "creating_containers",
		Phase: "creating_containers", TotalItems: len(event.Keys),
	})

	// Step 1: Create media containers.
	containerIDs := make([]string, 0, len(event.Keys))
	videoContainerIDs := make([]string, 0)
	isCarousel := len(event.Keys) > 1

	for i, key := range event.Keys {
		presignResult, err := presigner.PresignGetObject(ctx, &s3.GetObjectInput{
			Bucket: &mediaBucket, Key: &key,
		}, s3.WithPresignExpires(1*time.Hour))
		if err != nil {
			return setPublishError(ctx, event, fmt.Sprintf("failed to generate presigned URL for %s", key))
		}

		mediaURL := presignResult.URL
		isVideo := isVideoKey(key)

		var containerID string
		if isCarousel {
			if isVideo {
				containerID, err = igClient.CreateVideoContainer(ctx, mediaURL, true)
			} else {
				containerID, err = igClient.CreateImageContainer(ctx, mediaURL, true)
			}
		} else {
			if isVideo {
				containerID, err = igClient.CreateSingleReelPost(ctx, mediaURL, event.Caption)
			} else {
				containerID, err = igClient.CreateSingleImagePost(ctx, mediaURL, event.Caption)
			}
		}
		if err != nil {
			return setPublishError(ctx, event, fmt.Sprintf("failed to create container for item %d: %v", i+1, err))
		}

		containerIDs = append(containerIDs, containerID)
		if isVideo {
			videoContainerIDs = append(videoContainerIDs, containerID)
		}

		// Update progress in DynamoDB.
		sessionStore.PutPublishJob(ctx, event.SessionID, &store.PublishJob{
			ID: event.JobID, GroupID: event.GroupID, Status: "creating_containers",
			Phase: "creating_containers", TotalItems: len(event.Keys),
			CompletedItems: i + 1, ContainerIDs: containerIDs,
		})
	}

	// Step 2: Wait for video processing.
	if len(videoContainerIDs) > 0 {
		sessionStore.PutPublishJob(ctx, event.SessionID, &store.PublishJob{
			ID: event.JobID, GroupID: event.GroupID, Status: "processing_videos",
			Phase: "processing_videos", TotalItems: len(event.Keys),
			CompletedItems: len(event.Keys), ContainerIDs: containerIDs,
		})

		for _, vid := range videoContainerIDs {
			if err := igClient.WaitForContainer(ctx, vid, 5*time.Minute); err != nil {
				return setPublishError(ctx, event, fmt.Sprintf("video processing failed: %v", err))
			}
		}
	}

	// Step 3: Create carousel container or use single container.
	var publishContainerID string
	if isCarousel {
		sessionStore.PutPublishJob(ctx, event.SessionID, &store.PublishJob{
			ID: event.JobID, GroupID: event.GroupID, Status: "creating_carousel",
			Phase: "creating_carousel", TotalItems: len(event.Keys),
			CompletedItems: len(event.Keys), ContainerIDs: containerIDs,
		})

		var err error
		publishContainerID, err = igClient.CreateCarouselContainer(ctx, containerIDs, event.Caption)
		if err != nil {
			return setPublishError(ctx, event, fmt.Sprintf("failed to create carousel: %v", err))
		}
	} else {
		publishContainerID = containerIDs[0]
	}

	// Step 4: Publish!
	sessionStore.PutPublishJob(ctx, event.SessionID, &store.PublishJob{
		ID: event.JobID, GroupID: event.GroupID, Status: "publishing",
		Phase: "publishing", TotalItems: len(event.Keys),
		CompletedItems: len(event.Keys), ContainerIDs: containerIDs,
	})

	instagramPostID, err := igClient.Publish(ctx, publishContainerID)
	if err != nil {
		return setPublishError(ctx, event, fmt.Sprintf("publish failed: %v", err))
	}

	sessionStore.PutPublishJob(ctx, event.SessionID, &store.PublishJob{
		ID: event.JobID, GroupID: event.GroupID, Status: "published",
		Phase: "published", TotalItems: len(event.Keys),
		CompletedItems: len(event.Keys), ContainerIDs: containerIDs,
		InstagramPostID: instagramPostID,
	})

	log.Info().Str("instagramPostId", instagramPostID).Int("items", len(event.Keys)).Msg("Published to Instagram")
	return nil
}

func setPublishError(ctx context.Context, event WorkerEvent, msg string) error {
	log.Error().Str("job", event.JobID).Str("error", msg).Msg("Publish job failed")
	sessionStore.PutPublishJob(ctx, event.SessionID, &store.PublishJob{
		ID: event.JobID, GroupID: event.GroupID, Status: "error",
		Phase: "error", Error: msg,
	})
	return nil
}

// ===== Enhancement Feedback Processing =====

func handleEnhancementFeedback(ctx context.Context, event WorkerEvent) error {
	job, err := sessionStore.GetEnhancementJob(ctx, event.SessionID, event.JobID)
	if err != nil || job == nil {
		log.Error().Err(err).Str("jobId", event.JobID).Msg("Enhancement job not found for feedback")
		return nil
	}

	// Find the target item.
	var targetIdx int = -1
	for i, item := range job.Items {
		if item.Key == event.Key || item.EnhancedKey == event.Key {
			targetIdx = i
			break
		}
	}
	if targetIdx == -1 {
		log.Error().Str("key", event.Key).Msg("Item not found in enhancement job")
		return nil
	}
	item := job.Items[targetIdx]

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return nil
	}

	genaiClient, err := chat.NewGeminiClient(ctx, apiKey)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create Gemini client for feedback")
		return nil
	}
	geminiImageClient := chat.NewGeminiImageClient(genaiClient)

	enhancedKey := item.EnhancedKey
	if enhancedKey == "" {
		enhancedKey = item.Key
	}

	tmpPath, cleanup, err := downloadFromS3(ctx, enhancedKey)
	if err != nil {
		log.Error().Err(err).Str("key", enhancedKey).Msg("Failed to download enhanced image for feedback")
		return nil
	}
	defer cleanup()

	imageData, err := os.ReadFile(tmpPath)
	if err != nil {
		return nil
	}

	ext := strings.ToLower(filepath.Ext(enhancedKey))
	mime := "image/jpeg"
	if m, ok := filehandler.SupportedImageExtensions[ext]; ok {
		mime = m
	}

	imgConfig, _, err := image.DecodeConfig(bytes.NewReader(imageData))
	imageWidth, imageHeight := 1024, 1024
	if err == nil {
		imageWidth = imgConfig.Width
		imageHeight = imgConfig.Height
	}

	var imagenClient *chat.ImagenClient
	vertexProject := os.Getenv("VERTEX_AI_PROJECT")
	vertexRegion := os.Getenv("VERTEX_AI_REGION")
	vertexToken := os.Getenv("VERTEX_AI_TOKEN")
	if vertexProject != "" && vertexRegion != "" && vertexToken != "" {
		imagenClient = chat.NewImagenClient(vertexProject, vertexRegion, vertexToken)
	}

	// Convert store feedback history to chat format.
	var feedbackHistory []chat.FeedbackEntry
	for _, fe := range item.FeedbackHistory {
		feedbackHistory = append(feedbackHistory, chat.FeedbackEntry{
			UserFeedback:  fe.UserFeedback,
			ModelResponse: fe.ModelResponse,
			Method:        fe.Method,
			Success:       fe.Success,
		})
	}

	resultData, resultMIME, feedbackEntry, err := chat.ProcessFeedback(
		ctx, geminiImageClient, imagenClient,
		imageData, mime, event.Feedback,
		feedbackHistory, imageWidth, imageHeight,
	)
	if err != nil {
		log.Warn().Err(err).Msg("Feedback processing failed")
	}

	if resultData != nil && len(resultData) > 0 {
		feedbackKey := fmt.Sprintf("%s/enhanced/%s", event.SessionID, filepath.Base(item.Key))
		contentType := resultMIME
		_, uploadErr := s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: &mediaBucket, Key: &feedbackKey,
			Body: bytes.NewReader(resultData), ContentType: &contentType,
		})
		if uploadErr != nil {
			log.Error().Err(uploadErr).Str("key", feedbackKey).Msg("Failed to upload feedback result")
			return nil
		}

		// Generate and upload thumbnail.
		thumbKey := fmt.Sprintf("%s/thumbnails/enhanced-%s.jpg", event.SessionID,
			strings.TrimSuffix(filepath.Base(item.Key), filepath.Ext(item.Key)))
		thumbData, _, thumbErr := generateThumbnailFromBytes(resultData, resultMIME, 400)
		if thumbErr == nil {
			thumbContentType := "image/jpeg"
			s3Client.PutObject(ctx, &s3.PutObjectInput{
				Bucket: &mediaBucket, Key: &thumbKey,
				Body: bytes.NewReader(thumbData), ContentType: &thumbContentType,
			})
		}

		// Update DynamoDB.
		job.Items[targetIdx].EnhancedKey = feedbackKey
		job.Items[targetIdx].EnhancedThumbKey = thumbKey
		job.Items[targetIdx].Phase = chat.PhaseFeedback
		if feedbackEntry != nil {
			job.Items[targetIdx].FeedbackHistory = append(job.Items[targetIdx].FeedbackHistory, store.FeedbackEntry{
				UserFeedback:  feedbackEntry.UserFeedback,
				ModelResponse: feedbackEntry.ModelResponse,
				Method:        feedbackEntry.Method,
				Success:       feedbackEntry.Success,
			})
		}
		sessionStore.PutEnhancementJob(ctx, event.SessionID, job)
	}

	return nil
}

// ===== Shared Helpers =====

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

	if _, err := io.Copy(tmpFile, result.Body); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return "", nil, fmt.Errorf("download: %w", err)
	}
	tmpFile.Close()

	cleanup := func() { os.Remove(tmpFile.Name()) }
	return tmpFile.Name(), cleanup, nil
}

func downloadToFile(ctx context.Context, key, localPath string) error {
	result, err := s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &mediaBucket, Key: &key,
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

	if _, err := io.Copy(f, result.Body); err != nil {
		return fmt.Errorf("download: %w", err)
	}
	return nil
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
					continue
				}
				defer origCleanup()

				origData, readErr := os.ReadFile(origPath)
				if readErr != nil {
					continue
				}

				mime := "image/jpeg"
				if m, ok := filehandler.SupportedImageExtensions[ext]; ok {
					mime = m
				}

				thumbData, thumbMIME, thumbErr := generateThumbnailFromBytes(origData, mime, filehandler.DefaultThumbnailMaxDimension)
				if thumbErr != nil {
					continue
				}
				item.ThumbnailData = thumbData
				item.ThumbnailMIMEType = thumbMIME
			} else {
				defer cleanup()
				thumbData, err := os.ReadFile(tmpPath)
				if err != nil {
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
				continue
			}
			defer cleanup()

			thumbData, err := os.ReadFile(tmpPath)
			if err != nil {
				continue
			}
			item.ThumbnailData = thumbData
			item.ThumbnailMIMEType = "image/jpeg"
		} else {
			continue
		}

		items = append(items, item)
	}

	if len(items) == 0 {
		return nil, fmt.Errorf("no media items could be prepared for description")
	}
	return items, nil
}

func isVideoKey(key string) bool {
	lower := strings.ToLower(key)
	for _, ext := range []string{".mp4", ".mov", ".avi", ".mkv", ".webm", ".m4v", ".3gp"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// ===== Download Helpers =====

// dlFile holds an S3 key and its object size.
type dlFile struct {
	key  string
	size int64
}

// dlGroupBySize groups files into bundles where each bundle's total size <= maxBytes.
func dlGroupBySize(files []dlFile, maxBytes int64) [][]dlFile {
	if len(files) == 0 {
		return nil
	}

	sorted := make([]dlFile, len(files))
	copy(sorted, files)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].size > sorted[j].size
	})

	var groups [][]dlFile
	groupSizes := []int64{}

	for _, file := range sorted {
		if file.size > maxBytes {
			groups = append(groups, []dlFile{file})
			groupSizes = append(groupSizes, file.size)
			continue
		}
		placed := false
		for i, currentSize := range groupSizes {
			if currentSize+file.size <= maxBytes {
				groups[i] = append(groups[i], file)
				groupSizes[i] += file.size
				placed = true
				break
			}
		}
		if !placed {
			groups = append(groups, []dlFile{file})
			groupSizes = append(groupSizes, file.size)
		}
	}
	return groups
}

// dlCreateZip creates a zstd-compressed ZIP from S3 objects and uploads it to S3.
func dlCreateZip(ctx context.Context, files []dlFile, zipKey string) (int64, error) {
	tmpFile, err := os.CreateTemp("", "download-*.zip")
	if err != nil {
		return 0, fmt.Errorf("create temp ZIP: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	zipWriter := zip.NewWriter(tmpFile)

	for _, file := range files {
		filename := filepath.Base(file.key)
		getResult, err := s3Client.GetObject(ctx, &s3.GetObjectInput{
			Bucket: &mediaBucket, Key: &file.key,
		})
		if err != nil {
			log.Warn().Err(err).Str("key", file.key).Msg("Failed to download file for ZIP, skipping")
			continue
		}

		header := &zip.FileHeader{
			Name:   filename,
			Method: zipMethodZstd,
		}
		header.SetModTime(time.Now())

		writer, err := zipWriter.CreateHeader(header)
		if err != nil {
			getResult.Body.Close()
			return 0, fmt.Errorf("create ZIP entry for %s: %w", filename, err)
		}
		if _, err := io.Copy(writer, getResult.Body); err != nil {
			getResult.Body.Close()
			return 0, fmt.Errorf("write to ZIP for %s: %w", filename, err)
		}
		getResult.Body.Close()
	}

	if err := zipWriter.Close(); err != nil {
		tmpFile.Close()
		return 0, fmt.Errorf("close ZIP writer: %w", err)
	}
	tmpFile.Close()

	info, err := os.Stat(tmpPath)
	if err != nil {
		return 0, fmt.Errorf("stat ZIP file: %w", err)
	}
	zipSize := info.Size()

	zipFile, err := os.Open(tmpPath)
	if err != nil {
		return 0, fmt.Errorf("open ZIP for upload: %w", err)
	}
	defer zipFile.Close()

	contentType := "application/zip"
	_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: &mediaBucket, Key: &zipKey,
		Body: zipFile, ContentType: &contentType,
	})
	if err != nil {
		return 0, fmt.Errorf("upload ZIP to S3: %w", err)
	}

	return zipSize, nil
}

func sanitizeZipName(groupLabel, bundleType string, index int) string {
	name := groupLabel
	if name == "" {
		name = "media"
	}
	name = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == ' ' {
			return r
		}
		return '-'
	}, name)
	name = strings.TrimSpace(name)
	if len(name) > 50 {
		name = name[:50]
	}
	if bundleType == "images" {
		return fmt.Sprintf("%s-images.zip", name)
	}
	return fmt.Sprintf("%s-videos-%d.zip", name, index)
}
