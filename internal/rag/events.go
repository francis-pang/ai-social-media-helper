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

type BatchEmitter struct {
	client  *eventbridge.Client
	pending []eventbridgetypes.PutEventsRequestEntry
	errors  []error
}

func NewBatchEmitter(client *eventbridge.Client) *BatchEmitter {
	return &BatchEmitter{client: client}
}

func (b *BatchEmitter) Add(event ContentFeedback) {
	detail, err := json.Marshal(event)
	if err != nil {
		b.errors = append(b.errors, fmt.Errorf("marshal ContentFeedback for %s/%s: %w", event.SessionID, event.EventType, err))
		return
	}
	b.pending = append(b.pending, eventbridgetypes.PutEventsRequestEntry{
		Source:     aws.String("ai-social-media-helper"),
		DetailType: aws.String("ContentFeedback"),
		Detail:     aws.String(string(detail)),
	})
}

func (b *BatchEmitter) Flush(ctx context.Context) error {
	if len(b.errors) > 0 {
		for _, e := range b.errors {
			log.Error().Err(e).Msg("BatchEmitter: marshal error")
		}
	}
	if len(b.pending) == 0 {
		return nil
	}

	const maxBatch = 10
	var firstErr error
	total := len(b.pending)

	for i := 0; i < len(b.pending); i += maxBatch {
		end := i + maxBatch
		if end > len(b.pending) {
			end = len(b.pending)
		}
		batch := b.pending[i:end]

		result, err := b.client.PutEvents(ctx, &eventbridge.PutEventsInput{Entries: batch})
		if err != nil {
			log.Error().Err(err).Int("batch_size", len(batch)).Msg("BatchEmitter: PutEvents failed")
			if firstErr == nil {
				firstErr = fmt.Errorf("PutEvents batch: %w", err)
			}
			continue
		}
		if result.FailedEntryCount > 0 {
			for j, entry := range result.Entries {
				if entry.ErrorCode != nil {
					log.Error().Int("index", i+j).Str("errorCode", aws.ToString(entry.ErrorCode)).Str("errorMessage", aws.ToString(entry.ErrorMessage)).Msg("BatchEmitter: entry failed")
				}
			}
			if firstErr == nil {
				firstErr = fmt.Errorf("PutEvents: %d entries failed", result.FailedEntryCount)
			}
		}
	}

	log.Debug().Int("total", total).Msg("BatchEmitter: flushed")
	b.pending = nil
	b.errors = nil
	return firstErr
}
