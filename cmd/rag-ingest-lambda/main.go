package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/rdsdata"
	"github.com/rs/zerolog/log"

	"github.com/fpang/gemini-media-cli/internal/logging"
	"github.com/fpang/gemini-media-cli/internal/rag"
)

var (
	bedrockClient   *bedrockruntime.Client
	rdsClient       *rdsdata.Client
	ddbClient       *dynamodb.Client
	dataAPIClient   *rag.DataAPIClient
	embeddingModel  string
	profilesTable   string
)

type EventBridgeEvent struct {
	Detail json.RawMessage `json:"detail"`
}

func handler(ctx context.Context, sqsEvent events.SQSEvent) error {
	if len(sqsEvent.Records) == 0 {
		log.Info().Msg("no SQS records to process")
		return nil
	}

	var lastErr error
	successCount := 0

	for _, record := range sqsEvent.Records {
		if err := processRecord(ctx, record); err != nil {
			log.Error().Err(err).Str("messageId", record.MessageId).Msg("failed to process SQS record")
			lastErr = err
		} else {
			successCount++
			log.Info().Str("messageId", record.MessageId).Msg("processed SQS record")
		}
	}

	if successCount == 0 && lastErr != nil {
		return lastErr
	}
	return nil
}

func processRecord(ctx context.Context, record events.SQSMessage) error {
	var envelope EventBridgeEvent
	if err := json.Unmarshal([]byte(record.Body), &envelope); err != nil {
		return err
	}

	var feedback rag.ContentFeedback
	if err := json.Unmarshal(envelope.Detail, &feedback); err != nil {
		return err
	}

	table := rag.TableForEventType(feedback.EventType)
	if table == "" {
		log.Warn().Str("eventType", feedback.EventType).Msg("unknown event type, skipping")
		return nil
	}

	text := rag.BuildEmbeddingInput(feedback)
	embedding, err := rag.GenerateEmbedding(ctx, bedrockClient, embeddingModel, text)
	if err != nil {
		return err
	}

	createdAt := feedback.Timestamp
	if createdAt == "" {
		createdAt = time.Now().UTC().Format(time.RFC3339)
	}

	switch feedback.EventType {
	case rag.EventTriageFinalized:
		// triage.finalized -> TriageDecision
		saveable := strings.EqualFold(feedback.UserVerdict, "keep") || strings.EqualFold(feedback.UserVerdict, "save")
		if feedback.UserVerdict == "" {
			saveable = strings.EqualFold(feedback.AIVerdict, "keep") || strings.EqualFold(feedback.AIVerdict, "save")
		}
		d := rag.TriageDecision{
			SessionID:     feedback.SessionID,
			UserID:        feedback.UserID,
			MediaKey:      feedback.MediaKey,
			Filename:      getMeta(feedback.Metadata, "filename"),
			MediaType:     feedback.MediaType,
			Saveable:      saveable,
			Reason:        feedback.Reason,
			MediaMetadata: feedback.Metadata,
			Embedding:     embedding,
			CreatedAt:     createdAt,
		}
		if err := dataAPIClient.UpsertTriageDecision(ctx, d); err != nil {
			return err
		}

	case rag.EventSelectionFinalized:
		// selection.finalized -> SelectionDecision
		selected := strings.EqualFold(feedback.UserVerdict, "keep") || strings.EqualFold(feedback.UserVerdict, "select")
		if feedback.UserVerdict == "" {
			selected = strings.EqualFold(feedback.AIVerdict, "keep") || strings.EqualFold(feedback.AIVerdict, "select")
		}
		d := rag.SelectionDecision{
			SessionID:         feedback.SessionID,
			UserID:            feedback.UserID,
			MediaKey:          feedback.MediaKey,
			Filename:          getMeta(feedback.Metadata, "filename"),
			MediaType:         feedback.MediaType,
			Selected:          selected,
			ExclusionCategory: getMeta(feedback.Metadata, "exclusionCategory"),
			ExclusionReason:   getMeta(feedback.Metadata, "exclusionReason"),
			SceneGroup:        getMeta(feedback.Metadata, "sceneGroup"),
			MediaMetadata:     feedback.Metadata,
			Embedding:         embedding,
			CreatedAt:         createdAt,
		}
		if err := dataAPIClient.UpsertSelectionDecision(ctx, d); err != nil {
			return err
		}

	case rag.EventOverrideAction, rag.EventOverridesFinalized:
		// selection.override.action or selection.overrides.finalized -> OverrideDecision
		action := feedback.UserVerdict
		if action == "" {
			action = getMeta(feedback.Metadata, "action")
		}
		d := rag.OverrideDecision{
			SessionID:     feedback.SessionID,
			UserID:        feedback.UserID,
			MediaKey:      feedback.MediaKey,
			Filename:      getMeta(feedback.Metadata, "filename"),
			MediaType:     feedback.MediaType,
			Action:        action,
			AIVerdict:     feedback.AIVerdict,
			AIReason:      feedback.Reason,
			IsFinalized:   feedback.EventType == rag.EventOverridesFinalized,
			MediaMetadata: feedback.Metadata,
			Embedding:     embedding,
			CreatedAt:     createdAt,
		}
		if err := dataAPIClient.UpsertOverrideDecision(ctx, d); err != nil {
			return err
		}

	case rag.EventDescriptionFinalized:
		captionText := feedback.AIVerdict
		if captionText == "" {
			captionText = getMeta(feedback.Metadata, "captionText")
		}
		d := rag.CaptionDecision{
			SessionID:     feedback.SessionID,
			UserID:        feedback.UserID,
			CaptionText:   captionText,
			Hashtags:      parseStringSlice(feedback.Metadata, "hashtags"),
			LocationTag:   getMeta(feedback.Metadata, "locationTag"),
			MediaKeys:     parseStringSlice(feedback.Metadata, "mediaKeys"),
			PostGroupName: getMeta(feedback.Metadata, "postGroupName"),
			MediaMetadata: feedback.Metadata,
			Embedding:     embedding,
			CreatedAt:     createdAt,
		}
		if d.PostGroupName == "" {
			d.PostGroupName = feedback.SessionID + "-caption"
		}
		if err := dataAPIClient.UpsertCaptionDecision(ctx, d); err != nil {
			return err
		}

	case rag.EventPublishFinalized:
		d := rag.PublishDecision{
			SessionID:     feedback.SessionID,
			UserID:        feedback.UserID,
			Platform:      getMeta(feedback.Metadata, "platform"),
			PostGroupName: getMeta(feedback.Metadata, "postGroupName"),
			CaptionText:   getMeta(feedback.Metadata, "captionText"),
			Hashtags:      parseStringSlice(feedback.Metadata, "hashtags"),
			LocationTag:   getMeta(feedback.Metadata, "locationTag"),
			MediaKeys:     parseStringSlice(feedback.Metadata, "mediaKeys"),
			MediaMetadata: feedback.Metadata,
			Embedding:     embedding,
			CreatedAt:     createdAt,
		}
		if d.Platform == "" {
			d.Platform = "instagram"
		}
		if d.PostGroupName == "" {
			d.PostGroupName = feedback.SessionID + "-publish"
		}
		if err := dataAPIClient.UpsertPublishDecision(ctx, d); err != nil {
			return err
		}

	default:
		log.Warn().Str("eventType", feedback.EventType).Msg("unhandled event type")
		return nil
	}

	if profilesTable != "" && feedback.SessionID != "" {
		if err := rag.UpdateLastActivity(ctx, ddbClient, profilesTable, feedback.SessionID); err != nil {
			log.Warn().Err(err).Str("sessionId", feedback.SessionID).Msg("UpdateLastActivity failed")
		}
	}

	return nil
}

func getMeta(m map[string]string, key string) string {
	if m == nil {
		return ""
	}
	return m[key]
}

func parseStringSlice(m map[string]string, key string) []string {
	if m == nil {
		return nil
	}
	s := m[key]
	if s == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(s, ",") {
		if t := strings.TrimSpace(part); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func main() {
	logging.Init()

	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load AWS config")
	}

	bedrockClient = bedrockruntime.NewFromConfig(cfg)
	rdsClient = rdsdata.NewFromConfig(cfg)
	ddbClient = dynamodb.NewFromConfig(cfg)

	clusterARN := os.Getenv("AURORA_CLUSTER_ARN")
	secretARN := os.Getenv("AURORA_SECRET_ARN")
	database := os.Getenv("AURORA_DATABASE_NAME")
	dataAPIClient = rag.NewDataAPIClient(rdsClient, clusterARN, secretARN, database)

	embeddingModel = os.Getenv("BEDROCK_EMBEDDING_MODEL_ID")
	if embeddingModel == "" {
		embeddingModel = "amazon.titan-embed-text-v2:0"
	}

	profilesTable = os.Getenv("RAG_PROFILES_TABLE_NAME")

	lambda.Start(handler)
}
