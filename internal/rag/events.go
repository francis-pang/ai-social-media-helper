package rag

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	eventbridgetypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	"github.com/rs/zerolog/log"
)

func EmitContentFeedback(ctx context.Context, client *eventbridge.Client, event ContentFeedback) error {
	detail, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal ContentFeedback: %w", err)
	}

	source := "ai-social-media-helper"
	detailType := "ContentFeedback"

	input := &eventbridge.PutEventsInput{
		Entries: []eventbridgetypes.PutEventsRequestEntry{
			{
				Source:       aws.String(source),
				DetailType:   aws.String(detailType),
				Detail:       aws.String(string(detail)),
			},
		},
	}

	result, err := client.PutEvents(ctx, input)
	if err != nil {
		log.Error().Err(err).Str("sessionId", event.SessionID).Str("eventType", event.EventType).Msg("EventBridge PutEvents failed")
		return fmt.Errorf("PutEvents: %w", err)
	}

	if result.FailedEntryCount > 0 {
		for i, entry := range result.Entries {
			if entry.ErrorCode != nil || entry.ErrorMessage != nil {
				log.Error().
					Int("index", i).
					Str("errorCode", aws.ToString(entry.ErrorCode)).
					Str("errorMessage", aws.ToString(entry.ErrorMessage)).
					Str("sessionId", event.SessionID).
					Str("eventType", event.EventType).
					Msg("EventBridge PutEvents entry failed")
				return fmt.Errorf("PutEvents entry %d failed: %s - %s", i, aws.ToString(entry.ErrorCode), aws.ToString(entry.ErrorMessage))
			}
		}
	}

	log.Debug().Str("sessionId", event.SessionID).Str("eventType", event.EventType).Msg("ContentFeedback emitted to EventBridge")
	return nil
}
