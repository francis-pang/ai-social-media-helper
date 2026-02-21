package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/rdsdata"
	"github.com/rs/zerolog/log"

	"github.com/fpang/gemini-media-cli/internal/logging"
	"github.com/fpang/gemini-media-cli/internal/rag"
)

var (
	bedrockClient  *bedrockruntime.Client
	rdsClient      *rdsdata.Client
	ddbClient      *dynamodb.Client
	dataAPIClient  *rag.DataAPIClient
	embeddingModel string
	profilesTable  string
)

type QueryEvent struct {
	QueryType      string            `json:"queryType"`
	UserID         string            `json:"userId"`
	SessionContext string            `json:"sessionContext"`
	MediaMetadata  map[string]string `json:"mediaMetadata"`
}

type QueryResponse struct {
	RAGContext string `json:"ragContext"`
	Source     string `json:"source"`
}

type profileItem struct {
	PK                  string `dynamodbav:"PK"`
	SK                  string `dynamodbav:"SK"`
	ProfileText         string `dynamodbav:"profile_text"`
	CaptionExamplesText string `dynamodbav:"caption_examples_text"`
}

func handler(ctx context.Context, event QueryEvent) (QueryResponse, error) {
	switch event.QueryType {
	case "triage", "selection":
		return handleTriageOrSelection(ctx, event)
	case "caption":
		return handleCaption(ctx, event)
	default:
		log.Warn().Str("queryType", event.QueryType).Msg("unknown query type")
		return QueryResponse{RAGContext: "", Source: "empty"}, nil
	}
}

func handleTriageOrSelection(ctx context.Context, event QueryEvent) (QueryResponse, error) {
	profile, err := readProfile(ctx, ddbClient, profilesTable)
	if err != nil {
		log.Warn().Err(err).Msg("readProfile failed")
	}
	if profile != nil && profile.ProfileText != "" {
		updateLastActivity(ctx, event)
		return QueryResponse{RAGContext: profile.ProfileText, Source: "cache"}, nil
	}

	table := rag.TableTriageDecisions
	if event.QueryType == "selection" {
		table = rag.TableSelectionDecisions
	}

	embedding, err := rag.GenerateEmbedding(ctx, bedrockClient, embeddingModel, event.SessionContext)
	if err != nil {
		log.Warn().Err(err).Msg("GenerateEmbedding failed")
		updateLastActivity(ctx, event)
		return QueryResponse{RAGContext: "", Source: "empty"}, nil
	}

	if dataAPIClient == nil {
		updateLastActivity(ctx, event)
		return QueryResponse{RAGContext: "", Source: "empty"}, nil
	}

	rows, err := dataAPIClient.QuerySimilar(ctx, table, embedding, 10)
	if err != nil {
		log.Warn().Err(err).Str("table", table).Msg("QuerySimilar failed (Aurora unavailable)")
		updateLastActivity(ctx, event)
		return QueryResponse{RAGContext: "", Source: "empty"}, nil
	}

	rows = filterByUserID(rows, event.UserID)
	ragContext := formatDecisionsForLLM(rows)
	updateLastActivity(ctx, event)
	if ragContext == "" {
		return QueryResponse{RAGContext: "", Source: "empty"}, nil
	}
	return QueryResponse{RAGContext: ragContext, Source: "live"}, nil
}

func handleCaption(ctx context.Context, event QueryEvent) (QueryResponse, error) {
	var cached string
	profile, err := readProfile(ctx, ddbClient, profilesTable)
	if err != nil {
		log.Warn().Err(err).Msg("readProfile failed")
	}
	if profile != nil && profile.CaptionExamplesText != "" {
		cached = profile.CaptionExamplesText
	}

	embedding, err := rag.GenerateEmbedding(ctx, bedrockClient, embeddingModel, event.SessionContext)
	if err != nil {
		log.Warn().Err(err).Msg("GenerateEmbedding failed")
		updateLastActivity(ctx, event)
		if cached != "" {
			return QueryResponse{RAGContext: cached, Source: "cache"}, nil
		}
		return QueryResponse{RAGContext: "", Source: "empty"}, nil
	}

	if dataAPIClient == nil {
		updateLastActivity(ctx, event)
		if cached != "" {
			return QueryResponse{RAGContext: cached, Source: "cache"}, nil
		}
		return QueryResponse{RAGContext: "", Source: "empty"}, nil
	}

	rows, err := dataAPIClient.QuerySimilar(ctx, rag.TableCaptionDecisions, embedding, 10)
	if err != nil {
		log.Warn().Err(err).Msg("QuerySimilar failed (Aurora unavailable)")
		updateLastActivity(ctx, event)
		if cached != "" {
			return QueryResponse{RAGContext: cached, Source: "cache"}, nil
		}
		return QueryResponse{RAGContext: "", Source: "empty"}, nil
	}

	rows = filterByUserID(rows, event.UserID)
	liveContext := formatCaptionDecisionsForLLM(rows)
	combined := strings.TrimSpace(cached + "\n\n" + liveContext)
	combined = strings.TrimSpace(combined)

	updateLastActivity(ctx, event)

	source := "live"
	if cached != "" && liveContext != "" {
		source = "live"
	} else if cached != "" {
		source = "cache"
	} else if liveContext != "" {
		source = "live"
	} else {
		source = "empty"
	}

	return QueryResponse{RAGContext: combined, Source: source}, nil
}

func readProfile(ctx context.Context, ddbClient *dynamodb.Client, tableName string) (*rag.PreferenceProfile, error) {
	if tableName == "" || ddbClient == nil {
		return nil, nil
	}

	pk := "PROFILE#preference"
	sk := "latest"

	result, err := ddbClient.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: pk},
			"SK": &types.AttributeValueMemberS{Value: sk},
		},
	})
	if err != nil {
		return nil, err
	}
	if result.Item == nil {
		return nil, nil
	}

	var item profileItem
	if err := attributevalue.UnmarshalMap(result.Item, &item); err != nil {
		return nil, err
	}

	return &rag.PreferenceProfile{
		PK:                  item.PK,
		SK:                  item.SK,
		ProfileText:         item.ProfileText,
		CaptionExamplesText: item.CaptionExamplesText,
	}, nil
}

func filterByUserID(rows []map[string]interface{}, userID string) []map[string]interface{} {
	if userID == "" {
		return rows
	}
	var filtered []map[string]interface{}
	for _, row := range rows {
		if v, ok := row["user_id"]; ok && v != nil {
			if s, ok := v.(string); ok && s == userID {
				filtered = append(filtered, row)
			}
		}
	}
	if len(filtered) == 0 {
		return rows
	}
	return filtered
}

func formatDecisionsForLLM(rows []map[string]interface{}) string {
	if len(rows) == 0 {
		return ""
	}
	var sb strings.Builder
	for i, row := range rows {
		parts := []string{}
		if v, ok := row["reason"]; ok && v != nil {
			parts = append(parts, fmt.Sprintf("reason: %v", v))
		}
		if v, ok := row["saveable"]; ok && v != nil {
			parts = append(parts, fmt.Sprintf("saveable: %v", v))
		}
		if v, ok := row["selected"]; ok && v != nil {
			parts = append(parts, fmt.Sprintf("selected: %v", v))
		}
		if v, ok := row["exclusion_reason"]; ok && v != nil {
			parts = append(parts, fmt.Sprintf("exclusion_reason: %v", v))
		}
		if len(parts) > 0 {
			if i > 0 {
				sb.WriteString("\n\n")
			}
			sb.WriteString(strings.Join(parts, ", "))
		}
	}
	return sb.String()
}

func formatCaptionDecisionsForLLM(rows []map[string]interface{}) string {
	if len(rows) == 0 {
		return ""
	}
	var sb strings.Builder
	for i, row := range rows {
		if v, ok := row["caption_text"]; ok && v != nil {
			if i > 0 {
				sb.WriteString("\n\n")
			}
			sb.WriteString(fmt.Sprintf("%v", v))
		}
	}
	return sb.String()
}

func updateLastActivity(ctx context.Context, event QueryEvent) {
	if profilesTable == "" || ddbClient == nil {
		return
	}
	sessionID := event.SessionContext
	if sessionID == "" {
		sessionID = event.UserID
	}
	if sessionID == "" {
		return
	}
	if err := rag.UpdateLastActivity(ctx, ddbClient, profilesTable, sessionID); err != nil {
		log.Warn().Err(err).Str("sessionId", sessionID).Msg("UpdateLastActivity failed")
	}
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
	if clusterARN != "" && secretARN != "" && database != "" {
		dataAPIClient = rag.NewDataAPIClient(rdsClient, clusterARN, secretARN, database)
	}

	embeddingModel = os.Getenv("BEDROCK_EMBEDDING_MODEL_ID")
	if embeddingModel == "" {
		embeddingModel = "amazon.titan-embed-text-v2:0"
	}

	profilesTable = os.Getenv("RAG_PROFILES_TABLE_NAME")

	lambda.Start(handler)
}
