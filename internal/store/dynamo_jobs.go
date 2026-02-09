package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/rs/zerolog/log"
)

// --- Triage job operations (DDR-050) ---

func (s *DynamoStore) PutTriageJob(ctx context.Context, sessionID string, job *TriageJob) error {
	sk := skTriage + job.ID
	if err := s.putItem(ctx, sessionPK(sessionID), sk, job); err != nil {
		return fmt.Errorf("put triage job %s/%s: %w", sessionID, job.ID, err)
	}

	log.Debug().
		Str("sessionId", sessionID).
		Str("jobId", job.ID).
		Str("status", job.Status).
		Int("keep", len(job.Keep)).
		Int("discard", len(job.Discard)).
		Msg("Triage job persisted")
	return nil
}

func (s *DynamoStore) GetTriageJob(ctx context.Context, sessionID, jobID string) (*TriageJob, error) {
	var job TriageJob
	found, err := s.getItem(ctx, sessionPK(sessionID), skTriage+jobID, &job)
	if err != nil {
		return nil, fmt.Errorf("get triage job %s/%s: %w", sessionID, jobID, err)
	}
	if !found {
		log.Debug().Str("sessionId", sessionID).Str("jobId", jobID).Str("jobType", "triage").Bool("found", false).Msg("GetTriageJob: job not found")
		return nil, nil
	}

	job.ID = jobID
	job.SessionID = sessionID
	log.Debug().Str("sessionId", sessionID).Str("jobId", jobID).Str("jobType", "triage").Str("status", job.Status).Bool("found", true).Msg("GetTriageJob: job retrieved")
	return &job, nil
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
		log.Debug().Str("sessionId", sessionID).Str("jobId", jobID).Str("jobType", "selection").Bool("found", false).Msg("GetSelectionJob: job not found")
		return nil, nil
	}

	job.ID = jobID
	job.SessionID = sessionID
	log.Debug().Str("sessionId", sessionID).Str("jobId", jobID).Str("jobType", "selection").Str("status", job.Status).Bool("found", true).Msg("GetSelectionJob: job retrieved")
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
		log.Debug().Str("sessionId", sessionID).Str("jobId", jobID).Str("jobType", "enhancement").Bool("found", false).Msg("GetEnhancementJob: job not found")
		return nil, nil
	}

	job.ID = jobID
	job.SessionID = sessionID
	log.Debug().Str("sessionId", sessionID).Str("jobId", jobID).Str("jobType", "enhancement").Str("status", job.Status).Bool("found", true).Msg("GetEnhancementJob: job retrieved")
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
		log.Debug().Str("sessionId", sessionID).Str("jobId", jobID).Str("jobType", "download").Bool("found", false).Msg("GetDownloadJob: job not found")
		return nil, nil
	}

	job.ID = jobID
	job.SessionID = sessionID
	log.Debug().Str("sessionId", sessionID).Str("jobId", jobID).Str("jobType", "download").Str("status", job.Status).Bool("found", true).Msg("GetDownloadJob: job retrieved")
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
		log.Debug().Str("sessionId", sessionID).Str("jobId", jobID).Str("jobType", "description").Bool("found", false).Msg("GetDescriptionJob: job not found")
		return nil, nil
	}

	job.ID = jobID
	job.SessionID = sessionID
	log.Debug().Str("sessionId", sessionID).Str("jobId", jobID).Str("jobType", "description").Str("status", job.Status).Bool("found", true).Msg("GetDescriptionJob: job retrieved")
	return &job, nil
}

// --- Publish job operations ---

func (s *DynamoStore) PutPublishJob(ctx context.Context, sessionID string, job *PublishJob) error {
	sk := skPublish + job.ID
	if err := s.putItem(ctx, sessionPK(sessionID), sk, job); err != nil {
		return fmt.Errorf("put publish job %s/%s: %w", sessionID, job.ID, err)
	}

	log.Debug().
		Str("sessionId", sessionID).
		Str("jobId", job.ID).
		Str("status", job.Status).
		Str("phase", job.Phase).
		Int("completed", job.CompletedItems).
		Int("total", job.TotalItems).
		Msg("Publish job persisted")
	return nil
}

func (s *DynamoStore) GetPublishJob(ctx context.Context, sessionID, jobID string) (*PublishJob, error) {
	var job PublishJob
	found, err := s.getItem(ctx, sessionPK(sessionID), skPublish+jobID, &job)
	if err != nil {
		return nil, fmt.Errorf("get publish job %s/%s: %w", sessionID, jobID, err)
	}
	if !found {
		log.Debug().Str("sessionId", sessionID).Str("jobId", jobID).Str("jobType", "publish").Bool("found", false).Msg("GetPublishJob: job not found")
		return nil, nil
	}

	job.ID = jobID
	job.SessionID = sessionID
	log.Debug().Str("sessionId", sessionID).Str("jobId", jobID).Str("jobType", "publish").Str("status", job.Status).Bool("found", true).Msg("GetPublishJob: job retrieved")
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
