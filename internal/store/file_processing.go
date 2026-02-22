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

// FileProcessingTTL is the TTL for per-file processing records (4 hours).
// Shorter than SessionTTL (24h) since these are only needed during triage.
const FileProcessingTTL = 4 * time.Hour

// FileResult represents a single per-file processing result in the
// media-file-processing DynamoDB table (DDR-061).
type FileResult struct {
	SessionID    string            `json:"-" dynamodbav:"-"`
	JobID        string            `json:"-" dynamodbav:"-"`
	Filename     string            `json:"filename" dynamodbav:"-"` // Derived from SK
	Status       string            `json:"status" dynamodbav:"status"`
	OriginalKey  string            `json:"originalKey" dynamodbav:"originalKey"`
	ProcessedKey string            `json:"processedKey,omitempty" dynamodbav:"processedKey,omitempty"`
	ThumbnailKey string            `json:"thumbnailKey,omitempty" dynamodbav:"thumbnailKey,omitempty"`
	FileType     string            `json:"fileType" dynamodbav:"fileType"`
	MimeType     string            `json:"mimeType" dynamodbav:"mimeType"`
	FileSize     int64             `json:"fileSize" dynamodbav:"fileSize"`
	Converted    bool              `json:"converted" dynamodbav:"converted"`
	Fingerprint  string            `json:"fingerprint,omitempty" dynamodbav:"fingerprint,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty" dynamodbav:"metadata,omitempty"`
	Error        string            `json:"error,omitempty" dynamodbav:"error,omitempty"`
}

// FileProcessingStore provides operations on the dedicated media-file-processing
// DynamoDB table (DDR-061). It stores per-file processing results written by
// the MediaProcess Lambda and read by triage-run and the API results endpoint.
type FileProcessingStore struct {
	client    *dynamodb.Client
	tableName string
}

// NewFileProcessingStore creates a FileProcessingStore for the given table.
func NewFileProcessingStore(client *dynamodb.Client, tableName string) *FileProcessingStore {
	return &FileProcessingStore{
		client:    client,
		tableName: tableName,
	}
}

// fileProcessingPK returns the partition key: {sessionId}#{jobId}
func fileProcessingPK(sessionID, jobID string) string {
	return sessionID + "#" + jobID
}

// fileProcessingExpiresAt returns the TTL timestamp (now + FileProcessingTTL).
func fileProcessingExpiresAt() int64 {
	return time.Now().Add(FileProcessingTTL).Unix()
}

// PutFileResult writes a per-file processing result to the file-processing table.
func (s *FileProcessingStore) PutFileResult(ctx context.Context, sessionID, jobID string, result *FileResult) error {
	pk := fileProcessingPK(sessionID, jobID)
	sk := result.Filename

	start := time.Now()
	item, err := attributevalue.MarshalMap(result)
	if err != nil {
		return fmt.Errorf("marshal file result: %w", err)
	}

	item["PK"] = &types.AttributeValueMemberS{Value: pk}
	item["SK"] = &types.AttributeValueMemberS{Value: sk}
	item["expiresAt"] = &types.AttributeValueMemberN{Value: strconv.FormatInt(fileProcessingExpiresAt(), 10)}

	_, err = s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: &s.tableName,
		Item:      item,
	})
	duration := time.Since(start)
	if err != nil {
		log.Debug().Err(err).Str("pk", pk).Str("sk", sk).Dur("duration", duration).Msg("PutFileResult: DynamoDB PutItem failed")
		return fmt.Errorf("PutItem file result PK=%s SK=%s: %w", pk, sk, err)
	}
	log.Debug().Str("pk", pk).Str("sk", sk).Str("status", result.Status).Dur("duration", duration).Msg("PutFileResult: file result persisted")
	return nil
}

// GetFileResults retrieves all per-file processing results for a session+job.
func (s *FileProcessingStore) GetFileResults(ctx context.Context, sessionID, jobID string) ([]FileResult, error) {
	pk := fileProcessingPK(sessionID, jobID)

	start := time.Now()
	input := &dynamodb.QueryInput{
		TableName:              &s.tableName,
		KeyConditionExpression: aws.String("PK = :pk"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk": &types.AttributeValueMemberS{Value: pk},
		},
	}

	var allItems []map[string]types.AttributeValue
	for {
		result, err := s.client.Query(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("Query file results PK=%s: %w", pk, err)
		}
		allItems = append(allItems, result.Items...)
		if result.LastEvaluatedKey == nil {
			break
		}
		input.ExclusiveStartKey = result.LastEvaluatedKey
	}

	duration := time.Since(start)
	results := make([]FileResult, 0, len(allItems))
	for _, item := range allItems {
		var fr FileResult
		if err := attributevalue.UnmarshalMap(item, &fr); err != nil {
			log.Warn().Err(err).Str("pk", pk).Msg("Failed to unmarshal file result, skipping")
			continue
		}
		// Extract filename from SK
		if skAttr, ok := item["SK"].(*types.AttributeValueMemberS); ok {
			fr.Filename = skAttr.Value
		}
		// Extract sessionID and jobID from PK
		fr.SessionID = sessionID
		fr.JobID = jobID
		results = append(results, fr)
	}

	log.Debug().Str("pk", pk).Int("resultCount", len(results)).Dur("duration", duration).Msg("GetFileResults: query completed")
	return results, nil
}

// PutFingerprintMapping stores a fingerprint→filename mapping for dedup (DDR-067).
// Stored as SK=fp#{fingerprint} so lookups are O(1) key queries.
func (s *FileProcessingStore) PutFingerprintMapping(ctx context.Context, sessionID, jobID, fingerprint, filename string) error {
	pk := fileProcessingPK(sessionID, jobID)
	sk := "fp#" + fingerprint

	_, err := s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: &s.tableName,
		Item: map[string]types.AttributeValue{
			"PK":        &types.AttributeValueMemberS{Value: pk},
			"SK":        &types.AttributeValueMemberS{Value: sk},
			"filename":  &types.AttributeValueMemberS{Value: filename},
			"expiresAt": &types.AttributeValueMemberN{Value: strconv.FormatInt(fileProcessingExpiresAt(), 10)},
		},
	})
	if err != nil {
		return fmt.Errorf("PutItem fingerprint PK=%s SK=%s: %w", pk, sk, err)
	}
	log.Debug().Str("pk", pk).Str("fingerprint", fingerprint).Str("filename", filename).Msg("Fingerprint mapping stored")
	return nil
}

// GetFingerprintMapping checks if a fingerprint already exists for this session+job (DDR-067).
// Returns the original filename if found, empty string otherwise.
func (s *FileProcessingStore) GetFingerprintMapping(ctx context.Context, sessionID, jobID, fingerprint string) (string, error) {
	pk := fileProcessingPK(sessionID, jobID)
	sk := "fp#" + fingerprint

	result, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &s.tableName,
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: pk},
			"SK": &types.AttributeValueMemberS{Value: sk},
		},
		ProjectionExpression: aws.String("filename"),
	})
	if err != nil {
		return "", fmt.Errorf("GetItem fingerprint PK=%s SK=%s: %w", pk, sk, err)
	}
	if result.Item == nil {
		return "", nil
	}
	if fnAttr, ok := result.Item["filename"].(*types.AttributeValueMemberS); ok {
		return fnAttr.Value, nil
	}
	return "", nil
}

// GetFileResultByFilename retrieves a single file result by filename (DDR-067).
func (s *FileProcessingStore) GetFileResultByFilename(ctx context.Context, sessionID, jobID, filename string) (*FileResult, error) {
	pk := fileProcessingPK(sessionID, jobID)

	result, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: &s.tableName,
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: pk},
			"SK": &types.AttributeValueMemberS{Value: filename},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("GetItem file result PK=%s SK=%s: %w", pk, filename, err)
	}
	if result.Item == nil {
		return nil, nil
	}

	var fr FileResult
	if err := attributevalue.UnmarshalMap(result.Item, &fr); err != nil {
		return nil, fmt.Errorf("unmarshal file result: %w", err)
	}
	fr.Filename = filename
	fr.SessionID = sessionID
	fr.JobID = jobID
	return &fr, nil
}

// GetFileResultCount returns the count of items for a session+job using SELECT COUNT.
func (s *FileProcessingStore) GetFileResultCount(ctx context.Context, sessionID, jobID string) (int, error) {
	pk := fileProcessingPK(sessionID, jobID)

	result, err := s.client.Query(ctx, &dynamodb.QueryInput{
		TableName:              &s.tableName,
		KeyConditionExpression: aws.String("PK = :pk"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk": &types.AttributeValueMemberS{Value: pk},
		},
		Select: types.SelectCount,
	})
	if err != nil {
		return 0, fmt.Errorf("Query count PK=%s: %w", pk, err)
	}

	return int(result.Count), nil
}
