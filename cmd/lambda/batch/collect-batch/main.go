package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"

	"github.com/fpang/ai-social-media-helper/internal/ai"
	"github.com/fpang/ai-social-media-helper/internal/bootstrap"
	"github.com/fpang/ai-social-media-helper/internal/fbprep"
	"github.com/fpang/ai-social-media-helper/internal/logging"
	"github.com/fpang/ai-social-media-helper/internal/metrics"
	"github.com/fpang/ai-social-media-helper/internal/store"
	"github.com/rs/zerolog/log"
)

var sessionStore *store.DynamoStore

func init() {
	initStart := time.Now()
	logging.Init()

	awsClients := bootstrap.InitAWS()
	sessionStore = bootstrap.InitDynamo(awsClients.Config, "DYNAMO_TABLE_NAME")
	bootstrap.LoadGeminiKey(awsClients.SSM)
	bootstrap.LoadGCPServiceAccountKey(awsClients.SSM)
	_ = ai.LoadGCPServiceAccount()

	bootstrap.StartupLog("fb-prep-collect-batch", initStart).
		DynamoTable("sessions", os.Getenv("DYNAMO_TABLE_NAME")).
		Log()
}

// CollectOutput is the Lambda response.
type CollectOutput struct {
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
}

func handler(ctx context.Context, event interface{}) (*CollectOutput, error) {
	m, ok := event.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("collect-batch: expected map input")
	}
	sessionID, _ := m["sessionId"].(string)
	jobID, _ := m["jobId"].(string)
	batchJobID, _ := m["batchJobId"].(string)
	var batchJobIDs []string
	if ids, ok := m["batchJobIds"].([]interface{}); ok {
		for _, v := range ids {
			if s, _ := v.(string); s != "" {
				batchJobIDs = append(batchJobIDs, s)
			}
		}
	}
	if batchJobID != "" && len(batchJobIDs) == 0 {
		batchJobIDs = []string{batchJobID}
	}

	if sessionID == "" || jobID == "" || len(batchJobIDs) == 0 {
		return nil, fmt.Errorf("collect-batch: sessionId, jobId, and batchJobId/batchJobIds are required")
	}
	if sessionStore == nil {
		return nil, fmt.Errorf("collect-batch: session store not configured")
	}

	job, err := sessionStore.GetFBPrepJob(ctx, sessionID, jobID)
	if err != nil || job == nil {
		return nil, fmt.Errorf("collect-batch: job not found: %w", err)
	}

	collectClient, err := ai.NewAIClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("collect-batch: failed to initialize AI client: %w", err)
	}

	var allItems []store.FBPrepItem
	var inputTokens, outputTokens int
	var firstError string
	baseIndex := 0

	for _, bid := range batchJobIDs {
		batchStatus, err := ai.CheckGeminiBatch(ctx, collectClient, bid)
		if err != nil {
			return nil, fmt.Errorf("collect-batch: failed to check batch %s: %w", bid, err)
		}
		if batchStatus.State != "JOB_STATE_SUCCEEDED" {
			return nil, fmt.Errorf("collect-batch: unexpected batch state %s for job %s", batchStatus.State, bid)
		}

		for i, result := range batchStatus.Results {
			globalIdx := baseIndex + i
			s3Key := ""
			if globalIdx >= 0 && globalIdx < len(job.MediaKeys) {
				s3Key = job.MediaKeys[globalIdx]
			}

			if result.Error != "" {
				if firstError == "" {
					firstError = result.Error
				}
				allItems = append(allItems, store.FBPrepItem{
					ItemIndex: globalIdx,
					S3Key:     s3Key,
					Key:       s3Key,
					Error:     result.Error,
				})
				continue
			}
			if result.Response == nil {
				if firstError == "" {
					firstError = "nil response from batch"
				}
				allItems = append(allItems, store.FBPrepItem{
					ItemIndex: globalIdx,
					S3Key:     s3Key,
					Key:       s3Key,
					Error:     "nil response from batch",
				})
				continue
			}
			responseText := result.Response.Text()
			if responseText == "" {
				continue
			}
			if result.Response.UsageMetadata != nil {
				inputTokens += int(result.Response.UsageMetadata.PromptTokenCount)
				outputTokens += int(result.Response.UsageMetadata.CandidatesTokenCount)
			}
			parsed, err := fbprep.ParseResponse(responseText, job.MediaKeys)
			if err != nil {
				if firstError == "" {
					firstError = err.Error()
				}
				allItems = append(allItems, store.FBPrepItem{
					ItemIndex: globalIdx,
					S3Key:     s3Key,
					Key:       s3Key,
					Error:     err.Error(),
				})
				continue
			}
			allItems = append(allItems, parsed...)
		}
		baseIndex += len(batchStatus.Results)
	}

	// Deduplicate by item_index (keep first occurrence) and sort.
	seen := make(map[int]bool)
	var items []store.FBPrepItem
	for _, it := range allItems {
		if seen[it.ItemIndex] {
			continue
		}
		seen[it.ItemIndex] = true
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ItemIndex < items[j].ItemIndex })

	// Cleanup GCS videos (success or failure).
	if len(job.GCSPathsForCleanup) > 0 {
		ai.DeleteGCSObjects(ctx, job.GCSPathsForCleanup)
	}

	if firstError != "" {
		now := time.Now().UTC().Format(time.RFC3339)
		_ = sessionStore.PutFBPrepJob(ctx, sessionID, &store.FBPrepJob{
			ID:           jobID,
			Status:       "error",
			Items:        items,
			MediaKeys:    job.MediaKeys,
			Error:        firstError,
			EconomyMode:  true,
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
			CreatedAt:    job.CreatedAt,
			UpdatedAt:    now,
		})
		return nil, fmt.Errorf("collect-batch: %s", firstError)
	}

	if len(items) == 0 {
		return nil, fmt.Errorf("collect-batch: no items parsed from batch result(s)")
	}

	// DDR-088: Emit token metrics for cost analysis.
	if inputTokens > 0 || outputTokens > 0 {
		metrics.New("AiSocialMedia").
			Dimension("Operation", "fbPrepBatch").
			Metric("GeminiInputTokens", float64(inputTokens), metrics.UnitCount).
			Metric("GeminiOutputTokens", float64(outputTokens), metrics.UnitCount).
			Flush()
	}

	// Compare batch model location tags against pre-enrichment values (DDR-085).
	if len(job.PreEnrichLocations) > 0 {
		matchCount, mismatchCount := 0, 0
		for _, item := range items {
			if item.Error != "" {
				continue
			}
			preEnrich := job.PreEnrichLocations[strconv.Itoa(item.ItemIndex)]
			if preEnrich == "" {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(preEnrich), strings.TrimSpace(item.LocationTag)) {
				matchCount++
			} else {
				mismatchCount++
				log.Info().
					Int("itemIndex", item.ItemIndex).
					Str("preEnrichLocation", preEnrich).
					Str("batchLocation", item.LocationTag).
					Msg("Location tag differs between pre-enrichment and batch model")
			}
		}
		total := matchCount + mismatchCount
		if total > 0 {
			agreementRate := float64(matchCount) / float64(total) * 100
			metrics.New("AiSocialMedia").
				Dimension("Operation", "fbPrepLocationComparison").
				Metric("LocationTagMatchCount", float64(matchCount), metrics.UnitCount).
				Metric("LocationTagMismatchCount", float64(mismatchCount), metrics.UnitCount).
				Metric("LocationTagAgreementRate", agreementRate, metrics.UnitNone).
				Property("sessionId", sessionID).
				Property("jobId", jobID).
				Flush()
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_ = sessionStore.PutFBPrepJob(ctx, sessionID, &store.FBPrepJob{
		ID:           jobID,
		Status:       "complete",
		Items:        items,
		MediaKeys:    job.MediaKeys,
		EconomyMode:  true,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		CreatedAt:    job.CreatedAt,
		UpdatedAt:    now,
	})

	log.Info().
		Str("sessionId", sessionID).
		Str("jobId", jobID).
		Int("itemCount", len(items)).
		Int("inputTokens", inputTokens).
		Int("outputTokens", outputTokens).
		Msg("FB prep batch collection complete")

	return &CollectOutput{SessionID: sessionID, Status: "complete"}, nil
}

func main() {
	lambda.Start(handler)
}
