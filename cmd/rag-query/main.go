package main

import (
	"context"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/rs/zerolog/log"

	"github.com/fpang/ai-social-media-helper/internal/logging"
	"github.com/fpang/ai-social-media-helper/internal/rag"
)

var (
	ddbClient     *dynamodb.Client
	profilesTable string
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

// handler returns the pre-computed preference profile from DynamoDB (DDR-068).
// Aurora is no longer queried at request time; all data comes from the daily
// batch-built profile stored in the rag-preference-profiles table.
func handler(ctx context.Context, event QueryEvent) (QueryResponse, error) {
	profile, err := readProfile(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("readProfile failed")
		return QueryResponse{RAGContext: "", Source: "empty"}, nil
	}

	if profile == nil {
		return QueryResponse{RAGContext: "", Source: "empty"}, nil
	}

	switch event.QueryType {
	case "triage", "selection":
		if profile.ProfileText != "" {
			return QueryResponse{RAGContext: profile.ProfileText, Source: "cache"}, nil
		}
	case "caption":
		if profile.CaptionExamplesText != "" {
			return QueryResponse{RAGContext: profile.CaptionExamplesText, Source: "cache"}, nil
		}
	default:
		log.Warn().Str("queryType", event.QueryType).Msg("unknown query type")
	}

	return QueryResponse{RAGContext: "", Source: "empty"}, nil
}

func readProfile(ctx context.Context) (*rag.PreferenceProfile, error) {
	if profilesTable == "" || ddbClient == nil {
		return nil, nil
	}

	result, err := ddbClient.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(profilesTable),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: "PROFILE#preference"},
			"SK": &types.AttributeValueMemberS{Value: "latest"},
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

func main() {
	logging.Init()

	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load AWS config")
	}

	ddbClient = dynamodb.NewFromConfig(cfg)
	profilesTable = os.Getenv("RAG_PROFILES_TABLE_NAME")

	lambda.Start(handler)
}
