// Package main provides a Lambda entry point for the publish pipeline (DDR-053).
//
// This Lambda handles the 3 steps of the Publish Pipeline Step Function (DDR-052):
//   - publish-create-containers: Create Instagram media containers
//   - publish-check-video: Poll Instagram video container processing status
//   - publish-finalize: Create carousel (if multi-item) and publish to Instagram
//
// Container: Light (Dockerfile.light — no ffmpeg, no Gemini needed)
// Memory: 256 MB
// Timeout: 5 minutes
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/rs/zerolog/log"

	"github.com/fpang/gemini-media-cli/internal/instagram"
	"github.com/fpang/gemini-media-cli/internal/lambdaboot"
	"github.com/fpang/gemini-media-cli/internal/logging"
	"github.com/fpang/gemini-media-cli/internal/store"
)

var coldStart = true

var (
	presigner    *s3.PresignClient
	mediaBucket  string
	sessionStore *store.DynamoStore
	igClient     *instagram.Client
)

func init() {
	initStart := time.Now()
	logging.Init()

	aws := lambdaboot.InitAWS()
	s3s := lambdaboot.InitS3(aws.Config, "MEDIA_BUCKET_NAME")
	presigner = s3s.Presigner
	mediaBucket = s3s.Bucket
	sessionStore = lambdaboot.InitDynamo(aws.Config, "DYNAMO_TABLE_NAME")
	igClient = lambdaboot.LoadInstagramCreds(aws.SSM)

	lambdaboot.StartupLog("publish-lambda", initStart).
		S3Bucket("mediaBucket", mediaBucket).
		DynamoTable("sessions", os.Getenv("DYNAMO_TABLE_NAME")).
		SSMParam("instagramToken", logging.EnvOrDefault("SSM_INSTAGRAM_TOKEN_PARAM", "/ai-social-media/prod/instagram-access-token")).
		SSMParam("instagramUserId", logging.EnvOrDefault("SSM_INSTAGRAM_USER_ID_PARAM", "/ai-social-media/prod/instagram-user-id")).
		Feature("instagram", igClient != nil).
		Log()
}

func main() {
	lambda.Start(handler)
}

// --- Event and Result types ---

type PublishEvent struct {
	Type              string   `json:"type"`
	SessionID         string   `json:"sessionId"`
	JobID             string   `json:"jobId"`
	GroupID           string   `json:"groupId,omitempty"`
	Keys              []string `json:"keys,omitempty"`
	Caption           string   `json:"caption,omitempty"`
	ContainerIDs      []string `json:"containerIDs,omitempty"`
	VideoContainerIDs []string `json:"videoContainerIDs,omitempty"`
	IsCarousel        bool     `json:"isCarousel,omitempty"`
}

type PublishCreateContainersResult struct {
	SessionID         string   `json:"sessionId"`
	JobID             string   `json:"jobId"`
	GroupID           string   `json:"groupId"`
	Caption           string   `json:"caption"`
	ContainerIDs      []string `json:"containerIDs"`
	VideoContainerIDs []string `json:"videoContainerIDs"`
	HasVideos         bool     `json:"hasVideos"`
	IsCarousel        bool     `json:"isCarousel"`
}

type PublishCheckVideoResult struct {
	SessionID         string   `json:"sessionId"`
	JobID             string   `json:"jobId"`
	GroupID           string   `json:"groupId"`
	Caption           string   `json:"caption"`
	ContainerIDs      []string `json:"containerIDs"`
	VideoContainerIDs []string `json:"videoContainerIDs"`
	AllFinished       bool     `json:"allFinished"`
	IsCarousel        bool     `json:"isCarousel"`
}

func handler(ctx context.Context, event PublishEvent) (interface{}, error) {
	if coldStart {
		coldStart = false
		log.Info().Str("function", "publish-lambda").Msg("Cold start — first invocation")
	}
	log.Info().
		Str("type", event.Type).
		Str("sessionId", event.SessionID).
		Str("jobId", event.JobID).
		Msg("Publish Lambda invoked")

	switch event.Type {
	case "publish-create-containers":
		return handlePublishCreateContainers(ctx, event)
	case "publish-check-video":
		return handlePublishCheckVideo(ctx, event)
	case "publish-finalize":
		return nil, handlePublishFinalize(ctx, event)
	default:
		return nil, fmt.Errorf("unknown event type: %s", event.Type)
	}
}

func handlePublishCreateContainers(ctx context.Context, event PublishEvent) (*PublishCreateContainersResult, error) {
	if igClient == nil {
		setPublishError(ctx, event, "Instagram client not configured")
		return nil, fmt.Errorf("Instagram client not configured")
	}

	sessionStore.PutPublishJob(ctx, event.SessionID, &store.PublishJob{
		ID: event.JobID, GroupID: event.GroupID, Status: "creating_containers",
		Phase: "creating_containers", TotalItems: len(event.Keys),
	})

	containerIDs := make([]string, 0, len(event.Keys))
	videoContainerIDs := make([]string, 0)
	isCarousel := len(event.Keys) > 1

	for i, key := range event.Keys {
		presignResult, err := presigner.PresignGetObject(ctx, &s3.GetObjectInput{
			Bucket: &mediaBucket, Key: &key,
		}, s3.WithPresignExpires(1*time.Hour))
		if err != nil {
			setPublishError(ctx, event, fmt.Sprintf("failed to generate presigned URL for %s", key))
			return nil, fmt.Errorf("presign %s: %w", key, err)
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
			setPublishError(ctx, event, fmt.Sprintf("failed to create container for item %d: %v", i+1, err))
			return nil, fmt.Errorf("create container %d: %w", i+1, err)
		}

		log.Debug().Str("containerId", containerID).Int("item", i+1).Str("key", key).Msg("Container created")
		containerIDs = append(containerIDs, containerID)
		if isVideo {
			videoContainerIDs = append(videoContainerIDs, containerID)
		}

		sessionStore.PutPublishJob(ctx, event.SessionID, &store.PublishJob{
			ID: event.JobID, GroupID: event.GroupID, Status: "creating_containers",
			Phase: "creating_containers", TotalItems: len(event.Keys),
			CompletedItems: i + 1, ContainerIDs: containerIDs,
		})
	}

	log.Info().Int("containers", len(containerIDs)).Int("videoContainers", len(videoContainerIDs)).Msg("All containers created")

	return &PublishCreateContainersResult{
		SessionID:         event.SessionID,
		JobID:             event.JobID,
		GroupID:           event.GroupID,
		Caption:           event.Caption,
		ContainerIDs:      containerIDs,
		VideoContainerIDs: videoContainerIDs,
		HasVideos:         len(videoContainerIDs) > 0,
		IsCarousel:        isCarousel,
	}, nil
}

func handlePublishCheckVideo(ctx context.Context, event PublishEvent) (*PublishCheckVideoResult, error) {
	if igClient == nil {
		return nil, fmt.Errorf("Instagram client not configured")
	}

	sessionStore.PutPublishJob(ctx, event.SessionID, &store.PublishJob{
		ID: event.JobID, GroupID: event.GroupID, Status: "processing_videos",
		Phase: "processing_videos", TotalItems: len(event.ContainerIDs),
		CompletedItems: len(event.ContainerIDs), ContainerIDs: event.ContainerIDs,
	})

	allFinished := true
	for _, vid := range event.VideoContainerIDs {
		status, err := igClient.ContainerStatus(ctx, vid)
		if err != nil {
			log.Warn().Err(err).Str("containerId", vid).Msg("Failed to check container status")
			return nil, fmt.Errorf("check container %s: %w", vid, err)
		}
		log.Debug().Str("containerId", vid).Str("status", status).Msg("Video container status")
		if status == "ERROR" {
			setPublishError(ctx, event, fmt.Sprintf("video processing failed for container %s", vid))
			return nil, fmt.Errorf("video processing failed: container %s", vid)
		}
		if status != "FINISHED" {
			allFinished = false
		}
	}

	log.Info().Bool("allFinished", allFinished).Int("videoCount", len(event.VideoContainerIDs)).Msg("Video status check complete")

	return &PublishCheckVideoResult{
		SessionID:         event.SessionID,
		JobID:             event.JobID,
		GroupID:           event.GroupID,
		Caption:           event.Caption,
		ContainerIDs:      event.ContainerIDs,
		VideoContainerIDs: event.VideoContainerIDs,
		AllFinished:       allFinished,
		IsCarousel:        event.IsCarousel,
	}, nil
}

func handlePublishFinalize(ctx context.Context, event PublishEvent) error {
	jobStart := time.Now()
	if igClient == nil {
		return setPublishError(ctx, event, "Instagram client not configured")
	}

	var publishContainerID string
	if event.IsCarousel {
		sessionStore.PutPublishJob(ctx, event.SessionID, &store.PublishJob{
			ID: event.JobID, GroupID: event.GroupID, Status: "creating_carousel",
			Phase: "creating_carousel", TotalItems: len(event.ContainerIDs),
			CompletedItems: len(event.ContainerIDs), ContainerIDs: event.ContainerIDs,
		})

		var err error
		publishContainerID, err = igClient.CreateCarouselContainer(ctx, event.ContainerIDs, event.Caption)
		if err != nil {
			return setPublishError(ctx, event, fmt.Sprintf("failed to create carousel: %v", err))
		}
	} else {
		if len(event.ContainerIDs) == 0 {
			return setPublishError(ctx, event, "no container IDs provided")
		}
		publishContainerID = event.ContainerIDs[0]
	}

	sessionStore.PutPublishJob(ctx, event.SessionID, &store.PublishJob{
		ID: event.JobID, GroupID: event.GroupID, Status: "publishing",
		Phase: "publishing", TotalItems: len(event.ContainerIDs),
		CompletedItems: len(event.ContainerIDs), ContainerIDs: event.ContainerIDs,
	})

	instagramPostID, err := igClient.Publish(ctx, publishContainerID)
	if err != nil {
		return setPublishError(ctx, event, fmt.Sprintf("publish failed: %v", err))
	}

	sessionStore.PutPublishJob(ctx, event.SessionID, &store.PublishJob{
		ID: event.JobID, GroupID: event.GroupID, Status: "published",
		Phase: "published", TotalItems: len(event.ContainerIDs),
		CompletedItems: len(event.ContainerIDs), ContainerIDs: event.ContainerIDs,
		InstagramPostID: instagramPostID,
	})

	log.Info().Str("instagramPostId", instagramPostID).Int("items", len(event.ContainerIDs)).Dur("duration", time.Since(jobStart)).Msg("Published to Instagram")
	return nil
}

func setPublishError(ctx context.Context, event PublishEvent, msg string) error {
	log.Error().Str("job", event.JobID).Str("error", msg).Msg("Publish job failed")
	sessionStore.PutPublishJob(ctx, event.SessionID, &store.PublishJob{
		ID: event.JobID, GroupID: event.GroupID, Status: "error",
		Phase: "error", Error: msg,
	})
	return nil
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
