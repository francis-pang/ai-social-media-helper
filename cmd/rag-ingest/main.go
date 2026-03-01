package main

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/rs/zerolog/log"

	"github.com/fpang/ai-social-media-helper/internal/logging"
	"github.com/fpang/ai-social-media-helper/internal/rag"
)

const stagingTTLDays = 14

var (
	ddbClient    *dynamodb.Client
	stagingTable string
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
			log.Info().Str("messageId", record.MessageId).Msg("staged SQS record to DynamoDB")
		}
	}

	if successCount == 0 && lastErr != nil {
		return lastErr
	}
	return nil
}

// processRecord writes the raw ContentFeedback JSON to the DynamoDB staging
// table for later batch processing (DDR-068). No embedding or Aurora interaction.
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

	ts := feedback.Timestamp
	if ts == "" {
		ts = time.Now().UTC().Format(time.RFC3339)
	}

	sk := ts + "#" + record.MessageId
	expiresAt := time.Now().Add(stagingTTLDays * 24 * time.Hour).Unix()

	_, err := ddbClient.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(stagingTable),
		Item: map[string]types.AttributeValue{
			"PK":           &types.AttributeValueMemberS{Value: "STAGING"},
			"SK":           &types.AttributeValueMemberS{Value: sk},
			"feedbackJSON": &types.AttributeValueMemberS{Value: string(envelope.Detail)},
			"eventType":    &types.AttributeValueMemberS{Value: feedback.EventType},
			"sessionId":    &types.AttributeValueMemberS{Value: feedback.SessionID},
			"expiresAt":    &types.AttributeValueMemberN{Value: strconv.FormatInt(expiresAt, 10)},
		},
	})
	return err
}

func main() {
	logging.Init()

	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load AWS config")
	}

	ddbClient = dynamodb.NewFromConfig(cfg)
	stagingTable = os.Getenv("STAGING_TABLE_NAME")
	if stagingTable == "" {
		log.Fatal().Msg("STAGING_TABLE_NAME is required")
	}

	lambda.Start(handler)
}
