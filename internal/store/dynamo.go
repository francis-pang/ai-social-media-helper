package store

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/rs/zerolog/log"
)

// DynamoDB key constants for the single-table design.
const (
	pkPrefix    = "SESSION#"
	skMeta      = "META"
	skTriage    = "TRIAGE#"
	skSelection = "SELECTION#"
	skEnhance   = "ENHANCE#"
	skDownload  = "DOWNLOAD#"
	skDesc      = "DESC#"
	skGroup     = "GROUP#"
	skPublish   = "PUBLISH#"

	// maxBatchWrite is the DynamoDB BatchWriteItem limit per call.
	maxBatchWrite = 25
)

// stepToSKPrefix maps step names (from StepOrder) to DynamoDB sort key prefixes.
var stepToSKPrefix = map[string]string{
	"triage":      skTriage,
	"selection":   skSelection,
	"enhancement": skEnhance,
	"grouping":    skGroup,
	"download":    skDownload,
	"description": skDesc,
	"publish":     skPublish,
}

// DynamoStore implements SessionStore using AWS DynamoDB.
// It uses the single-table design defined in DDR-039.
type DynamoStore struct {
	client    *dynamodb.Client
	tableName string
}

// Compile-time interface check.
var _ SessionStore = (*DynamoStore)(nil)

// NewDynamoStore creates a DynamoStore for the given table.
// The client should be initialized from the shared AWS config.
func NewDynamoStore(client *dynamodb.Client, tableName string) *DynamoStore {
	return &DynamoStore{
		client:    client,
		tableName: tableName,
	}
}

// Client returns the underlying DynamoDB client.
// Used by MediaProcess Lambda to share one client across stores (DDR-061).
func (s *DynamoStore) Client() *dynamodb.Client {
	return s.client
}

// QueryBySKPrefix queries all items for a session where SK begins with the given prefix.
// Public wrapper for cross-package use (DDR-061: MediaProcess Lambda).
func (s *DynamoStore) QueryBySKPrefix(ctx context.Context, sessionID, skPrefix string) ([]map[string]types.AttributeValue, error) {
	return s.queryBySKPrefix(ctx, sessionID, skPrefix)
}

// --- Internal helpers ---

// sessionPK returns the partition key for a session.
func sessionPK(sessionID string) string {
	return pkPrefix + sessionID
}

// expiresAt returns the Unix epoch timestamp for record expiration (now + SessionTTL).
func expiresAt() int64 {
	return time.Now().Add(SessionTTL).Unix()
}

// putItem marshals a domain object and writes it to DynamoDB with PK, SK, and TTL.
// The domain object should use dynamodbav:"-" for fields derived from PK/SK.
func (s *DynamoStore) putItem(ctx context.Context, pk, sk string, data interface{}) error {
	log.Trace().Str("pk", pk).Str("sk", sk).Msg("putItem: marshaling and writing to DynamoDB")

	start := time.Now()
	item, err := attributevalue.MarshalMap(data)
	if err != nil {
		log.Trace().Err(err).Str("pk", pk).Str("sk", sk).Msg("putItem: marshal failed")
		return fmt.Errorf("marshal: %w", err)
	}
	log.Trace().Str("pk", pk).Str("sk", sk).Msg("putItem: marshal completed")

	// Add key and TTL attributes (overwrite any conflicting keys from the data).
	item["PK"] = &types.AttributeValueMemberS{Value: pk}
	item["SK"] = &types.AttributeValueMemberS{Value: sk}
	item["expiresAt"] = &types.AttributeValueMemberN{Value: strconv.FormatInt(expiresAt(), 10)}

	_, err = s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: &s.tableName,
		Item:      item,
	})
	duration := time.Since(start)
	if err != nil {
		log.Debug().Err(err).Str("pk", pk).Str("sk", sk).Dur("duration", duration).Msg("putItem: DynamoDB PutItem failed")
		return fmt.Errorf("PutItem PK=%s SK=%s: %w", pk, sk, err)
	}
	log.Debug().Str("pk", pk).Str("sk", sk).Dur("duration", duration).Msg("putItem: DynamoDB PutItem completed")
	return nil
}

// getItem reads a single item from DynamoDB and unmarshals it into out.
// Returns false if the item does not exist (out is not modified).
func (s *DynamoStore) getItem(ctx context.Context, pk, sk string, out interface{}) (bool, error) {
	log.Trace().Str("pk", pk).Str("sk", sk).Msg("getItem: reading from DynamoDB")

	start := time.Now()
	result, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &s.tableName,
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: pk},
			"SK": &types.AttributeValueMemberS{Value: sk},
		},
	})
	duration := time.Since(start)
	if err != nil {
		log.Debug().Err(err).Str("pk", pk).Str("sk", sk).Dur("duration", duration).Msg("getItem: DynamoDB GetItem failed")
		return false, fmt.Errorf("GetItem PK=%s SK=%s: %w", pk, sk, err)
	}
	if result.Item == nil {
		log.Debug().Str("pk", pk).Str("sk", sk).Dur("duration", duration).Bool("found", false).Msg("getItem: item not found")
		return false, nil
	}
	log.Trace().Str("pk", pk).Str("sk", sk).Msg("getItem: unmarshaling item")
	if err := attributevalue.UnmarshalMap(result.Item, out); err != nil {
		log.Trace().Err(err).Str("pk", pk).Str("sk", sk).Msg("getItem: unmarshal failed")
		log.Warn().Err(err).Str("pk", pk).Str("sk", sk).Msg("getItem: unmarshal failure")
		return false, fmt.Errorf("unmarshal PK=%s SK=%s: %w", pk, sk, err)
	}
	log.Trace().Str("pk", pk).Str("sk", sk).Msg("getItem: unmarshal completed")
	log.Debug().Str("pk", pk).Str("sk", sk).Dur("duration", duration).Bool("found", true).Msg("getItem: item retrieved and unmarshaled")
	return true, nil
}

// deleteItem removes a single item from DynamoDB by PK/SK.
func (s *DynamoStore) deleteItem(ctx context.Context, pk, sk string) error {
	log.Trace().Str("pk", pk).Str("sk", sk).Msg("deleteItem: deleting from DynamoDB")

	start := time.Now()
	_, err := s.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: &s.tableName,
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: pk},
			"SK": &types.AttributeValueMemberS{Value: sk},
		},
	})
	duration := time.Since(start)
	if err != nil {
		log.Debug().Err(err).Str("pk", pk).Str("sk", sk).Dur("duration", duration).Msg("deleteItem: DynamoDB DeleteItem failed")
		return fmt.Errorf("DeleteItem PK=%s SK=%s: %w", pk, sk, err)
	}
	log.Debug().Str("pk", pk).Str("sk", sk).Dur("duration", duration).Msg("deleteItem: DynamoDB DeleteItem completed")
	return nil
}

// queryBySKPrefix queries all items for a session where SK begins with the given prefix.
// Returns raw DynamoDB items for flexible processing by the caller.
func (s *DynamoStore) queryBySKPrefix(ctx context.Context, sessionID, skPrefix string) ([]map[string]types.AttributeValue, error) {
	pk := sessionPK(sessionID)
	log.Debug().Str("sessionId", sessionID).Str("skPrefix", skPrefix).Str("pk", pk).Msg("queryBySKPrefix: querying DynamoDB")

	start := time.Now()
	input := &dynamodb.QueryInput{
		TableName:              &s.tableName,
		KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :skPrefix)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk":       &types.AttributeValueMemberS{Value: pk},
			":skPrefix": &types.AttributeValueMemberS{Value: skPrefix},
		},
	}

	var allItems []map[string]types.AttributeValue

	// Handle pagination — DynamoDB returns up to 1MB per Query call.
	for {
		result, err := s.client.Query(ctx, input)
		if err != nil {
			duration := time.Since(start)
			log.Debug().Err(err).Str("sessionId", sessionID).Str("skPrefix", skPrefix).Dur("duration", duration).Msg("queryBySKPrefix: DynamoDB Query failed")
			return nil, fmt.Errorf("Query PK=%s SK prefix=%s: %w", pk, skPrefix, err)
		}
		allItems = append(allItems, result.Items...)

		if result.LastEvaluatedKey == nil {
			break
		}
		input.ExclusiveStartKey = result.LastEvaluatedKey
	}

	duration := time.Since(start)
	log.Debug().Str("sessionId", sessionID).Str("skPrefix", skPrefix).Int("resultCount", len(allItems)).Dur("duration", duration).Msg("queryBySKPrefix: query completed")
	return allItems, nil
}

// batchDeleteKeys deletes multiple items by their PK/SK keys.
// Handles DynamoDB's 25-item-per-batch limit automatically.
func (s *DynamoStore) batchDeleteKeys(ctx context.Context, keys []map[string]types.AttributeValue) error {
	log.Debug().Int("keyCount", len(keys)).Msg("batchDeleteKeys: starting batch delete")

	totalUnprocessed := 0
	for i := 0; i < len(keys); i += maxBatchWrite {
		end := i + maxBatchWrite
		if end > len(keys) {
			end = len(keys)
		}

		var requests []types.WriteRequest
		for _, key := range keys[i:end] {
			requests = append(requests, types.WriteRequest{
				DeleteRequest: &types.DeleteRequest{Key: key},
			})
		}

		result, err := s.client.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{
			RequestItems: map[string][]types.WriteRequest{
				s.tableName: requests,
			},
		})
		if err != nil {
			log.Debug().Err(err).Int("keyCount", len(keys)).Int("unprocessedCount", totalUnprocessed).Msg("batchDeleteKeys: BatchWriteItem failed")
			return fmt.Errorf("BatchWriteItem delete (%d items): %w", len(requests), err)
		}

		// Count unprocessed items for this batch
		if unprocessed, ok := result.UnprocessedItems[s.tableName]; ok {
			unprocessedCount := len(unprocessed)
			totalUnprocessed += unprocessedCount
		}

		// Note: UnprocessedItems are not retried here. With PAY_PER_REQUEST
		// billing and low throughput, unprocessed items are extremely rare.
		// The 24-hour TTL provides a safety net for any missed deletes.
	}
	log.Debug().Int("keyCount", len(keys)).Int("unprocessedCount", totalUnprocessed).Msg("batchDeleteKeys: batch delete completed")
	return nil
}

// --- Session operations ---

func (s *DynamoStore) PutSession(ctx context.Context, session *Session) error {
	if session.CreatedAt == 0 {
		session.CreatedAt = time.Now().Unix()
	}

	if err := s.putItem(ctx, sessionPK(session.ID), skMeta, session); err != nil {
		return fmt.Errorf("put session %s: %w", session.ID, err)
	}

	log.Debug().Str("sessionId", session.ID).Str("status", session.Status).Msg("Session persisted to DynamoDB")
	return nil
}

func (s *DynamoStore) GetSession(ctx context.Context, sessionID string) (*Session, error) {
	var session Session
	found, err := s.getItem(ctx, sessionPK(sessionID), skMeta, &session)
	if err != nil {
		return nil, fmt.Errorf("get session %s: %w", sessionID, err)
	}
	if !found {
		log.Debug().Str("sessionId", sessionID).Bool("found", false).Msg("GetSession: session not found")
		return nil, nil
	}

	session.ID = sessionID
	log.Debug().Str("sessionId", sessionID).Str("status", session.Status).Bool("found", true).Msg("GetSession: session retrieved")
	return &session, nil
}

func (s *DynamoStore) UpdateSessionStatus(ctx context.Context, sessionID, status string) error {
	_, err := s.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &s.tableName,
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: sessionPK(sessionID)},
			"SK": &types.AttributeValueMemberS{Value: skMeta},
		},
		UpdateExpression: aws.String("SET #s = :s"),
		ExpressionAttributeNames: map[string]string{
			"#s": "status", // "status" is a DynamoDB reserved word
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":s": &types.AttributeValueMemberS{Value: status},
		},
	})
	if err != nil {
		return fmt.Errorf("update session status %s -> %s: %w", sessionID, status, err)
	}

	log.Debug().Str("sessionId", sessionID).Str("status", status).Msg("Session status updated")
	return nil
}
