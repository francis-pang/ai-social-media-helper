package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"
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
	skSelection = "SELECTION#"
	skEnhance   = "ENHANCE#"
	skDownload  = "DOWNLOAD#"
	skDesc      = "DESC#"
	skGroup     = "GROUP#"

	// maxBatchWrite is the DynamoDB BatchWriteItem limit per call.
	maxBatchWrite = 25
)

// stepToSKPrefix maps step names (from StepOrder) to DynamoDB sort key prefixes.
var stepToSKPrefix = map[string]string{
	"selection":   skSelection,
	"enhancement": skEnhance,
	"grouping":    skGroup,
	"download":    skDownload,
	"description": skDesc,
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
	item, err := attributevalue.MarshalMap(data)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	// Add key and TTL attributes (overwrite any conflicting keys from the data).
	item["PK"] = &types.AttributeValueMemberS{Value: pk}
	item["SK"] = &types.AttributeValueMemberS{Value: sk}
	item["expiresAt"] = &types.AttributeValueMemberN{Value: strconv.FormatInt(expiresAt(), 10)}

	_, err = s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: &s.tableName,
		Item:      item,
	})
	if err != nil {
		return fmt.Errorf("PutItem PK=%s SK=%s: %w", pk, sk, err)
	}
	return nil
}

// getItem reads a single item from DynamoDB and unmarshals it into out.
// Returns false if the item does not exist (out is not modified).
func (s *DynamoStore) getItem(ctx context.Context, pk, sk string, out interface{}) (bool, error) {
	result, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &s.tableName,
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: pk},
			"SK": &types.AttributeValueMemberS{Value: sk},
		},
	})
	if err != nil {
		return false, fmt.Errorf("GetItem PK=%s SK=%s: %w", pk, sk, err)
	}
	if result.Item == nil {
		return false, nil
	}
	if err := attributevalue.UnmarshalMap(result.Item, out); err != nil {
		return false, fmt.Errorf("unmarshal PK=%s SK=%s: %w", pk, sk, err)
	}
	return true, nil
}

// deleteItem removes a single item from DynamoDB by PK/SK.
func (s *DynamoStore) deleteItem(ctx context.Context, pk, sk string) error {
	_, err := s.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: &s.tableName,
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: pk},
			"SK": &types.AttributeValueMemberS{Value: sk},
		},
	})
	if err != nil {
		return fmt.Errorf("DeleteItem PK=%s SK=%s: %w", pk, sk, err)
	}
	return nil
}

// queryBySKPrefix queries all items for a session where SK begins with the given prefix.
// Returns raw DynamoDB items for flexible processing by the caller.
func (s *DynamoStore) queryBySKPrefix(ctx context.Context, sessionID, skPrefix string) ([]map[string]types.AttributeValue, error) {
	pk := sessionPK(sessionID)

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
			return nil, fmt.Errorf("Query PK=%s SK prefix=%s: %w", pk, skPrefix, err)
		}
		allItems = append(allItems, result.Items...)

		if result.LastEvaluatedKey == nil {
			break
		}
		input.ExclusiveStartKey = result.LastEvaluatedKey
	}

	return allItems, nil
}

// batchDeleteKeys deletes multiple items by their PK/SK keys.
// Handles DynamoDB's 25-item-per-batch limit automatically.
func (s *DynamoStore) batchDeleteKeys(ctx context.Context, keys []map[string]types.AttributeValue) error {
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

		_, err := s.client.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{
			RequestItems: map[string][]types.WriteRequest{
				s.tableName: requests,
			},
		})
		if err != nil {
			return fmt.Errorf("BatchWriteItem delete (%d items): %w", len(requests), err)
		}

		// Note: UnprocessedItems are not retried here. With PAY_PER_REQUEST
		// billing and low throughput, unprocessed items are extremely rare.
		// The 24-hour TTL provides a safety net for any missed deletes.
	}
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
		return nil, nil
	}

	session.ID = sessionID
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

// --- Selection job operations ---

func (s *DynamoStore) PutSelectionJob(ctx context.Context, sessionID string, job *SelectionJob) error {
	sk := skSelection + job.ID
	if err := s.putItem(ctx, sessionPK(sessionID), sk, job); err != nil {
		return fmt.Errorf("put selection job %s/%s: %w", sessionID, job.ID, err)
	}

	log.Debug().
		Str("sessionId", sessionID).
		Str("jobId", job.ID).
		Str("status", job.Status).
		Int("selected", len(job.Selected)).
		Int("excluded", len(job.Excluded)).
		Msg("Selection job persisted")
	return nil
}

func (s *DynamoStore) GetSelectionJob(ctx context.Context, sessionID, jobID string) (*SelectionJob, error) {
	var job SelectionJob
	found, err := s.getItem(ctx, sessionPK(sessionID), skSelection+jobID, &job)
	if err != nil {
		return nil, fmt.Errorf("get selection job %s/%s: %w", sessionID, jobID, err)
	}
	if !found {
		return nil, nil
	}

	job.ID = jobID
	job.SessionID = sessionID
	return &job, nil
}

// --- Enhancement job operations ---

func (s *DynamoStore) PutEnhancementJob(ctx context.Context, sessionID string, job *EnhancementJob) error {
	sk := skEnhance + job.ID
	if err := s.putItem(ctx, sessionPK(sessionID), sk, job); err != nil {
		return fmt.Errorf("put enhancement job %s/%s: %w", sessionID, job.ID, err)
	}

	log.Debug().
		Str("sessionId", sessionID).
		Str("jobId", job.ID).
		Str("status", job.Status).
		Int("completed", job.CompletedCount).
		Int("total", job.TotalCount).
		Msg("Enhancement job persisted")
	return nil
}

func (s *DynamoStore) GetEnhancementJob(ctx context.Context, sessionID, jobID string) (*EnhancementJob, error) {
	var job EnhancementJob
	found, err := s.getItem(ctx, sessionPK(sessionID), skEnhance+jobID, &job)
	if err != nil {
		return nil, fmt.Errorf("get enhancement job %s/%s: %w", sessionID, jobID, err)
	}
	if !found {
		return nil, nil
	}

	job.ID = jobID
	job.SessionID = sessionID
	return &job, nil
}

// --- Download job operations ---

func (s *DynamoStore) PutDownloadJob(ctx context.Context, sessionID string, job *DownloadJob) error {
	sk := skDownload + job.ID
	if err := s.putItem(ctx, sessionPK(sessionID), sk, job); err != nil {
		return fmt.Errorf("put download job %s/%s: %w", sessionID, job.ID, err)
	}

	log.Debug().
		Str("sessionId", sessionID).
		Str("jobId", job.ID).
		Str("status", job.Status).
		Int("bundles", len(job.Bundles)).
		Msg("Download job persisted")
	return nil
}

func (s *DynamoStore) GetDownloadJob(ctx context.Context, sessionID, jobID string) (*DownloadJob, error) {
	var job DownloadJob
	found, err := s.getItem(ctx, sessionPK(sessionID), skDownload+jobID, &job)
	if err != nil {
		return nil, fmt.Errorf("get download job %s/%s: %w", sessionID, jobID, err)
	}
	if !found {
		return nil, nil
	}

	job.ID = jobID
	job.SessionID = sessionID
	return &job, nil
}

// --- Description job operations ---

func (s *DynamoStore) PutDescriptionJob(ctx context.Context, sessionID string, job *DescriptionJob) error {
	sk := skDesc + job.ID
	if err := s.putItem(ctx, sessionPK(sessionID), sk, job); err != nil {
		return fmt.Errorf("put description job %s/%s: %w", sessionID, job.ID, err)
	}

	log.Debug().
		Str("sessionId", sessionID).
		Str("jobId", job.ID).
		Str("status", job.Status).
		Int("historyLen", len(job.History)).
		Msg("Description job persisted")
	return nil
}

func (s *DynamoStore) GetDescriptionJob(ctx context.Context, sessionID, jobID string) (*DescriptionJob, error) {
	var job DescriptionJob
	found, err := s.getItem(ctx, sessionPK(sessionID), skDesc+jobID, &job)
	if err != nil {
		return nil, fmt.Errorf("get description job %s/%s: %w", sessionID, jobID, err)
	}
	if !found {
		return nil, nil
	}

	job.ID = jobID
	job.SessionID = sessionID
	return &job, nil
}

// --- Post group operations ---

func (s *DynamoStore) PutPostGroup(ctx context.Context, sessionID string, group *PostGroup) error {
	sk := skGroup + group.ID
	if err := s.putItem(ctx, sessionPK(sessionID), sk, group); err != nil {
		return fmt.Errorf("put post group %s/%s: %w", sessionID, group.ID, err)
	}

	log.Debug().
		Str("sessionId", sessionID).
		Str("groupId", group.ID).
		Str("name", group.Name).
		Int("mediaCount", len(group.MediaKeys)).
		Msg("Post group persisted")
	return nil
}

func (s *DynamoStore) GetPostGroups(ctx context.Context, sessionID string) ([]*PostGroup, error) {
	items, err := s.queryBySKPrefix(ctx, sessionID, skGroup)
	if err != nil {
		return nil, fmt.Errorf("get post groups for %s: %w", sessionID, err)
	}

	groups := make([]*PostGroup, 0, len(items))
	for _, item := range items {
		var group PostGroup
		if err := attributevalue.UnmarshalMap(item, &group); err != nil {
			log.Warn().Err(err).Str("sessionId", sessionID).Msg("Failed to unmarshal post group, skipping")
			continue
		}

		// Extract group ID from SK: "GROUP#grp-001" → "grp-001"
		if skAttr, ok := item["SK"].(*types.AttributeValueMemberS); ok {
			group.ID = strings.TrimPrefix(skAttr.Value, skGroup)
		}

		groups = append(groups, &group)
	}

	return groups, nil
}

func (s *DynamoStore) DeletePostGroup(ctx context.Context, sessionID, groupID string) error {
	if err := s.deleteItem(ctx, sessionPK(sessionID), skGroup+groupID); err != nil {
		return fmt.Errorf("delete post group %s/%s: %w", sessionID, groupID, err)
	}

	log.Debug().Str("sessionId", sessionID).Str("groupId", groupID).Msg("Post group deleted")
	return nil
}

// --- Session invalidation ---

func (s *DynamoStore) InvalidateDownstream(ctx context.Context, sessionID, fromStep string) ([]string, error) {
	// Determine which SK prefixes to delete based on the step cascade.
	fromIndex := -1
	for i, step := range StepOrder {
		if step == fromStep {
			fromIndex = i
			break
		}
	}
	if fromIndex < 0 {
		return nil, fmt.Errorf("invalid step %q: must be one of %v", fromStep, StepOrder)
	}

	var prefixes []string
	for _, step := range StepOrder[fromIndex:] {
		if p, ok := stepToSKPrefix[step]; ok {
			prefixes = append(prefixes, p)
		}
	}

	// Query all items for this session (keys only — minimize read capacity).
	pk := sessionPK(sessionID)
	queryInput := &dynamodb.QueryInput{
		TableName:              &s.tableName,
		KeyConditionExpression: aws.String("PK = :pk"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk": &types.AttributeValueMemberS{Value: pk},
		},
		ProjectionExpression: aws.String("PK, SK"),
	}

	var keysToDelete []map[string]types.AttributeValue
	var deletedSKs []string

	// Paginate through all session items.
	for {
		result, err := s.client.Query(ctx, queryInput)
		if err != nil {
			return nil, fmt.Errorf("query session %s for invalidation: %w", sessionID, err)
		}

		for _, item := range result.Items {
			skAttr, ok := item["SK"].(*types.AttributeValueMemberS)
			if !ok {
				continue
			}
			sk := skAttr.Value

			// Check if this SK matches any prefix to delete.
			// Skip the META record — we never delete the session itself.
			for _, prefix := range prefixes {
				if strings.HasPrefix(sk, prefix) {
					keysToDelete = append(keysToDelete, map[string]types.AttributeValue{
						"PK": item["PK"],
						"SK": item["SK"],
					})
					deletedSKs = append(deletedSKs, sk)
					break
				}
			}
		}

		if result.LastEvaluatedKey == nil {
			break
		}
		queryInput.ExclusiveStartKey = result.LastEvaluatedKey
	}

	if len(keysToDelete) == 0 {
		log.Debug().
			Str("sessionId", sessionID).
			Str("fromStep", fromStep).
			Msg("No downstream state to invalidate")
		return nil, nil
	}

	// Batch delete all matching items.
	if err := s.batchDeleteKeys(ctx, keysToDelete); err != nil {
		return deletedSKs, fmt.Errorf("batch delete downstream state for %s from %s: %w", sessionID, fromStep, err)
	}

	log.Info().
		Str("sessionId", sessionID).
		Str("fromStep", fromStep).
		Int("deleted", len(deletedSKs)).
		Strs("keys", deletedSKs).
		Msg("Downstream state invalidated in DynamoDB")

	return deletedSKs, nil
}
