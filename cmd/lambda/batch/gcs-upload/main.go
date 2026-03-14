package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/fpang/ai-social-media-helper/internal/ai"
	"github.com/fpang/ai-social-media-helper/internal/bootstrap"
	"github.com/fpang/ai-social-media-helper/internal/httputil"
	"github.com/fpang/ai-social-media-helper/internal/logging"
	"github.com/fpang/ai-social-media-helper/internal/s3util"
)

var (
	presignClient *s3.PresignClient
	mediaBucket   string
)

func init() {
	initStart := time.Now()
	logging.Init()

	awsClients := bootstrap.InitAWS()
	s3s := bootstrap.InitS3(awsClients.Config, "MEDIA_BUCKET_NAME")
	presignClient = s3s.Presigner
	mediaBucket = s3s.Bucket
	bootstrap.LoadGCPServiceAccountKey(awsClients.SSM)
	if err := ai.LoadGCPServiceAccount(); err != nil {
		log.Fatal().Err(err).Msg("Failed to load GCP service account")
	}

	logging.NewStartupLogger("fb-prep-gcs-upload").InitDuration(time.Since(initStart)).Log()
}

// UploadOutput is the Lambda response for the GCS upload task.
type UploadOutput struct {
	GsURI            string `json:"gs_uri"`
	BatchIndex       int    `json:"batch_index"`
	ItemIndexInBatch int    `json:"item_index_in_batch"`
	S3Key            string `json:"s3_key"`
}

func handler(ctx context.Context, input map[string]interface{}) (*UploadOutput, error) {
	useKey, _ := input["use_key"].(string)
	if useKey == "" {
		useKey, _ = input["useKey"].(string)
	}
	jobID, _ := input["job_id"].(string)
	if jobID == "" {
		jobID, _ = input["jobId"].(string)
	}
	s3Key, _ := input["s3_key"].(string)
	if s3Key == "" {
		s3Key, _ = input["s3Key"].(string)
	}
	batchIndex := int(getFloat(input, "batch_index", "batchIndex"))
	itemIndexInBatch := int(getFloat(input, "item_index_in_batch", "itemIndexInBatch"))

	if useKey == "" || jobID == "" {
		return nil, fmt.Errorf("use_key and job_id are required")
	}
	gcsBucket := os.Getenv("GCS_BATCH_BUCKET")
	if gcsBucket == "" {
		return nil, fmt.Errorf("GCS_BATCH_BUCKET not set")
	}

	url, err := s3util.GeneratePresignedURL(ctx, presignClient, mediaBucket, useKey, 15*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("presign: %w", err)
	}
	tmpPath, cleanup, err := httputil.FetchURLToFile(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	defer cleanup()
	data, err := os.ReadFile(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}

	objectPath := fmt.Sprintf("fb-prep-videos/%s/%s.webm", jobID, uuid.New().String())
	gsURI, err := ai.UploadVideoToGCS(ctx, gcsBucket, objectPath, data, "video/webm")
	if err != nil {
		return nil, fmt.Errorf("upload to GCS: %w", err)
	}

	return &UploadOutput{
		GsURI:            gsURI,
		BatchIndex:       batchIndex,
		ItemIndexInBatch: itemIndexInBatch,
		S3Key:            s3Key,
	}, nil
}

func getFloat(m map[string]interface{}, keys ...string) float64 {
	for _, k := range keys {
		if v, ok := m[k].(float64); ok {
			return v
		}
	}
	return 0
}

func main() {
	lambda.Start(handler)
}
