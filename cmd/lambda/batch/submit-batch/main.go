package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"google.golang.org/genai"

	"github.com/fpang/ai-social-media-helper/internal/assets"
	"github.com/fpang/ai-social-media-helper/internal/ai"
	"github.com/fpang/ai-social-media-helper/internal/bootstrap"
	"github.com/fpang/ai-social-media-helper/internal/fbprep"
	"github.com/fpang/ai-social-media-helper/internal/logging"
	"github.com/fpang/ai-social-media-helper/internal/store"
	"github.com/rs/zerolog/log"
)

var (
	sessionStore     *store.DynamoStore
	fileProcessStore *store.FileProcessingStore
	s3Client         *s3.Client
	presignClient    *s3.PresignClient
	mediaBucket      string
)

func init() {
	initStart := time.Now()
	logging.Init()

	awsClients := bootstrap.InitAWS()
	s3s := bootstrap.InitS3(awsClients.Config, "MEDIA_BUCKET_NAME")
	s3Client = s3s.Client
	presignClient = s3s.Presigner
	mediaBucket = s3s.Bucket
	sessionStore = bootstrap.InitDynamo(awsClients.Config, "DYNAMO_TABLE_NAME")
	bootstrap.LoadGeminiKey(awsClients.SSM)
	bootstrap.LoadGCPServiceAccountKey(awsClients.SSM)
	_ = ai.LoadGCPServiceAccount()

	fpTableName := os.Getenv("FILE_PROCESSING_TABLE_NAME")
	if fpTableName != "" {
		fileProcessStore = store.NewFileProcessingStore(sessionStore.Client(), fpTableName)
	}

	bootstrap.StartupLog("fb-prep-submit-batch", initStart).
		S3Bucket("mediaBucket", mediaBucket).
		DynamoTable("sessions", os.Getenv("DYNAMO_TABLE_NAME")).
		Log()
}

// SubmitOutput is the Lambda response. JSON keys match Step Functions references.
type SubmitOutput struct {
	SessionID   string   `json:"session_id"`
	Status      string   `json:"status"`
	BatchJobID  string   `json:"batch_job_id,omitempty"`  // single batch
	BatchJobIDs []string `json:"batch_job_ids,omitempty"` // multiple batches
}

func handler(ctx context.Context, event interface{}) (*SubmitOutput, error) {
	m, ok := event.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("submit-batch: expected map input")
	}

	sessionID, _ := m["sessionId"].(string)
	jobID, _ := m["jobId"].(string)
	batchesMetaRaw, _ := m["batchesMeta"].([]interface{})
	locationTagsRaw, _ := m["locationTags"].(map[string]interface{})
	gcsUploadResultsRaw, _ := m["gcsUploadResults"].([]interface{})

	if sessionID == "" || jobID == "" {
		return nil, fmt.Errorf("submit-batch: sessionId and jobId are required")
	}

	gcsURIsByBatch := make(map[int]map[int]string)
	for _, r := range gcsUploadResultsRaw {
		rm, _ := r.(map[string]interface{})
		payload := rm
		if p, ok := rm["Payload"].(map[string]interface{}); ok {
			payload = p
		}
		gsURI, _ := payload["gs_uri"].(string)
		if gsURI == "" {
			gsURI, _ = payload["gsUri"].(string)
		}
		bi, _ := payload["batch_index"].(float64)
		if bi == 0 {
			bi, _ = payload["batchIndex"].(float64)
		}
		ii, _ := payload["item_index_in_batch"].(float64)
		if ii == 0 {
			ii, _ = payload["itemIndexInBatch"].(float64)
		}
		if gsURI == "" {
			continue
		}
		if gcsURIsByBatch[int(bi)] == nil {
			gcsURIsByBatch[int(bi)] = make(map[int]string)
		}
		gcsURIsByBatch[int(bi)][int(ii)] = gsURI
	}

	locationTags := make(map[int]string)
	for k, v := range locationTagsRaw {
		i, _ := strconv.Atoi(k)
		if s, ok := v.(string); ok {
			locationTags[i] = s
		}
	}

	var batchesMeta []fbprep.BatchMeta
	for _, b := range batchesMetaRaw {
		bm, _ := b.(map[string]interface{})
		bi, _ := bm["batch_index"].(float64)
		metaCtx, _ := bm["metadata_ctx"].(string)
		baseIdx, _ := bm["base_index"].(float64)
		var mediaItems []fbprep.MediaItem
		if items, ok := bm["media_items"].([]interface{}); ok {
			for _, it := range items {
				im, _ := it.(map[string]interface{})
				mediaItems = append(mediaItems, fbprep.MediaItemFromMap(im))
			}
		}
		var s3Keys []string
		if keys, ok := bm["s3_keys"].([]interface{}); ok {
			for _, k := range keys {
				if s, ok := k.(string); ok {
					s3Keys = append(s3Keys, s)
				}
			}
		}
		batchesMeta = append(batchesMeta, fbprep.BatchMeta{
			BatchIndex:  int(bi),
			MediaItems:  mediaItems,
			MetadataCtx: metaCtx,
			BaseIndex:   int(baseIdx),
			S3Keys:      s3Keys,
		})
	}

	genaiClient, err := ai.NewAIClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("submit-batch: failed to init AI client: %w", err)
	}

	deps := fbprep.SubmitDeps{
		FileProcessStore: fileProcessStore,
		S3Client:         s3Client,
		PresignClient:    presignClient,
		MediaBucket:      mediaBucket,
	}

	modelName := ai.GetBatchModelName()
	var batchJobIDs []string
	var allGCSPaths []string

	for _, meta := range batchesMeta {
		parts, err := fbprep.BuildMediaPartsWithGCSURIs(ctx, sessionID, meta, gcsURIsByBatch[meta.BatchIndex], deps)
		if err != nil {
			return nil, fmt.Errorf("submit-batch: failed to build parts: %w", err)
		}
		batchLocTags := fbprep.FilterLocationTagsForBatch(locationTags, meta.BaseIndex, len(meta.MediaItems))
		prompt := fbprep.BuildPrompt(meta.MetadataCtx, batchLocTags)
		parts = append(parts, &genai.Part{Text: prompt})
		config := &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{
				Parts: []*genai.Part{{Text: assets.FBPrepSystemPrompt}},
			},
		}
		req := &genai.InlinedRequest{
			Model:    modelName,
			Contents: []*genai.Content{{Role: "user", Parts: parts}},
			Config:   config,
		}
		batchJobID, err := ai.SubmitGeminiBatch(ctx, genaiClient, modelName, []*genai.InlinedRequest{req})
		if err != nil {
			return nil, fmt.Errorf("submit-batch: failed to submit: %w", err)
		}
		batchJobIDs = append(batchJobIDs, batchJobID)
		for _, gsURI := range gcsURIsByBatch[meta.BatchIndex] {
			allGCSPaths = append(allGCSPaths, gsURI)
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	job, _ := sessionStore.GetFBPrepJob(ctx, sessionID, jobID)
	if sessionStore != nil && job != nil {
		updated := &store.FBPrepJob{
			ID:                 jobID,
			Status:             "pending",
			MediaKeys:          job.MediaKeys,
			EconomyMode:        true,
			PreEnrichLocations: job.PreEnrichLocations,
			GCSPathsForCleanup: allGCSPaths,
			CreatedAt:          job.CreatedAt,
			UpdatedAt:          now,
		}
		if len(batchJobIDs) == 1 {
			updated.BatchJobID = batchJobIDs[0]
		} else {
			updated.BatchJobIDs = batchJobIDs
		}
		_ = sessionStore.PutFBPrepJob(ctx, sessionID, updated)
	}

	log.Info().
		Str("sessionId", sessionID).
		Str("jobId", jobID).
		Strs("batchJobIds", batchJobIDs).
		Msg("FB prep batch job(s) submitted")

	out := &SubmitOutput{SessionID: sessionID, Status: "pending"}
	if len(batchJobIDs) == 1 {
		out.BatchJobID = batchJobIDs[0]
	} else {
		out.BatchJobIDs = batchJobIDs
	}
	return out, nil
}

func main() {
	lambda.Start(handler)
}
