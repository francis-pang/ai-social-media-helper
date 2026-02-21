package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/rdsdata"
	rdsdatatypes "github.com/aws/aws-sdk-go-v2/service/rdsdata/types"
	"github.com/rs/zerolog/log"
)

type DataAPIClient struct {
	client     *rdsdata.Client
	clusterARN string
	secretARN  string
	database   string
}

func NewDataAPIClient(client *rdsdata.Client, clusterARN, secretARN, database string) *DataAPIClient {
	return &DataAPIClient{
		client:     client,
		clusterARN: clusterARN,
		secretARN:  secretARN,
		database:   database,
	}
}

func TableForEventType(eventType string) string {
	switch eventType {
	case EventTriageFinalized:
		return TableTriageDecisions
	case EventSelectionFinalized, EventOverridesFinalized:
		return TableSelectionDecisions
	case EventOverrideAction:
		return TableOverrideDecisions
	case EventDescriptionFinalized:
		return TableCaptionDecisions
	case EventPublishFinalized:
		return TablePublishDecisions
	default:
		return ""
	}
}

var allowedTables = map[string]bool{
	TableTriageDecisions: true, TableSelectionDecisions: true, TableOverrideDecisions: true,
	TableCaptionDecisions: true, TablePublishDecisions: true,
}

func formatVector(emb []float32) string {
	if len(emb) == 0 {
		return "[]"
	}
	var b strings.Builder
	b.WriteByte('[')
	for i, v := range emb {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(v), 'f', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}

func formatTextArray(arr []string) string {
	if len(arr) == 0 {
		return "{}"
	}
	escaped := make([]string, len(arr))
	for i, s := range arr {
		escaped[i] = `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
	}
	return "{" + strings.Join(escaped, ",") + "}"
}

func (c *DataAPIClient) exec(ctx context.Context, sql string, params []rdsdatatypes.SqlParameter) error {
	_, err := c.client.ExecuteStatement(ctx, &rdsdata.ExecuteStatementInput{
		ResourceArn: aws.String(c.clusterARN),
		SecretArn:   aws.String(c.secretARN),
		Database:    aws.String(c.database),
		Sql:         aws.String(sql),
		Parameters:  params,
	})
	return err
}

func (c *DataAPIClient) UpsertTriageDecision(ctx context.Context, d TriageDecision) error {
	mediaMeta := "{}"
	if len(d.MediaMetadata) > 0 {
		b, _ := json.Marshal(d.MediaMetadata)
		mediaMeta = string(b)
	}
	embStr := formatVector(d.Embedding)
	sql := `INSERT INTO triage_decisions (session_id, user_id, media_key, filename, media_type, saveable, reason, media_metadata, embedding, created_at)
		VALUES (:session_id, :user_id, :media_key, :filename, :media_type, :saveable, :reason, :media_metadata::jsonb, :embedding::vector, COALESCE(:created_at::timestamptz, NOW()))
		ON CONFLICT (session_id, media_key) DO UPDATE SET
			filename = EXCLUDED.filename, media_type = EXCLUDED.media_type, saveable = EXCLUDED.saveable,
			reason = EXCLUDED.reason, media_metadata = EXCLUDED.media_metadata, embedding = EXCLUDED.embedding, created_at = EXCLUDED.created_at`
	params := []rdsdatatypes.SqlParameter{
		{Name: aws.String("session_id"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.SessionID}},
		{Name: aws.String("user_id"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.UserID}},
		{Name: aws.String("media_key"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.MediaKey}},
		{Name: aws.String("filename"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.Filename}},
		{Name: aws.String("media_type"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.MediaType}},
		{Name: aws.String("saveable"), Value: &rdsdatatypes.FieldMemberBooleanValue{Value: d.Saveable}},
		{Name: aws.String("reason"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.Reason}},
		{Name: aws.String("media_metadata"), Value: &rdsdatatypes.FieldMemberStringValue{Value: mediaMeta}, TypeHint: rdsdatatypes.TypeHintJson},
		{Name: aws.String("embedding"), Value: &rdsdatatypes.FieldMemberStringValue{Value: embStr}},
		{Name: aws.String("created_at"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.CreatedAt}},
	}
	if err := c.exec(ctx, sql, params); err != nil {
		log.Error().Err(err).Str("sessionId", d.SessionID).Str("mediaKey", d.MediaKey).Msg("UpsertTriageDecision failed")
		return fmt.Errorf("UpsertTriageDecision: %w", err)
	}
	return nil
}

func (c *DataAPIClient) UpsertSelectionDecision(ctx context.Context, d SelectionDecision) error {
	mediaMeta := "{}"
	if len(d.MediaMetadata) > 0 {
		b, _ := json.Marshal(d.MediaMetadata)
		mediaMeta = string(b)
	}
	embStr := formatVector(d.Embedding)
	sql := `INSERT INTO selection_decisions (session_id, user_id, media_key, filename, media_type, selected, exclusion_category, exclusion_reason, scene_group, media_metadata, embedding, created_at)
		VALUES (:session_id, :user_id, :media_key, :filename, :media_type, :selected, :exclusion_category, :exclusion_reason, :scene_group, :media_metadata::jsonb, :embedding::vector, COALESCE(:created_at::timestamptz, NOW()))
		ON CONFLICT (session_id, media_key) DO UPDATE SET
			filename = EXCLUDED.filename, media_type = EXCLUDED.media_type, selected = EXCLUDED.selected,
			exclusion_category = EXCLUDED.exclusion_category, exclusion_reason = EXCLUDED.exclusion_reason,
			scene_group = EXCLUDED.scene_group, media_metadata = EXCLUDED.media_metadata, embedding = EXCLUDED.embedding, created_at = EXCLUDED.created_at`
	params := []rdsdatatypes.SqlParameter{
		{Name: aws.String("session_id"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.SessionID}},
		{Name: aws.String("user_id"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.UserID}},
		{Name: aws.String("media_key"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.MediaKey}},
		{Name: aws.String("filename"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.Filename}},
		{Name: aws.String("media_type"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.MediaType}},
		{Name: aws.String("selected"), Value: &rdsdatatypes.FieldMemberBooleanValue{Value: d.Selected}},
		{Name: aws.String("exclusion_category"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.ExclusionCategory}},
		{Name: aws.String("exclusion_reason"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.ExclusionReason}},
		{Name: aws.String("scene_group"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.SceneGroup}},
		{Name: aws.String("media_metadata"), Value: &rdsdatatypes.FieldMemberStringValue{Value: mediaMeta}, TypeHint: rdsdatatypes.TypeHintJson},
		{Name: aws.String("embedding"), Value: &rdsdatatypes.FieldMemberStringValue{Value: embStr}},
		{Name: aws.String("created_at"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.CreatedAt}},
	}
	if err := c.exec(ctx, sql, params); err != nil {
		log.Error().Err(err).Str("sessionId", d.SessionID).Str("mediaKey", d.MediaKey).Msg("UpsertSelectionDecision failed")
		return fmt.Errorf("UpsertSelectionDecision: %w", err)
	}
	return nil
}

func (c *DataAPIClient) UpsertOverrideDecision(ctx context.Context, d OverrideDecision) error {
	mediaMeta := "{}"
	if len(d.MediaMetadata) > 0 {
		b, _ := json.Marshal(d.MediaMetadata)
		mediaMeta = string(b)
	}
	embStr := formatVector(d.Embedding)
	sql := `INSERT INTO override_decisions (session_id, user_id, media_key, filename, media_type, action, ai_verdict, ai_reason, is_finalized, media_metadata, embedding, created_at)
		VALUES (:session_id, :user_id, :media_key, :filename, :media_type, :action, :ai_verdict, :ai_reason, :is_finalized, :media_metadata::jsonb, :embedding::vector, COALESCE(:created_at::timestamptz, NOW()))`
	params := []rdsdatatypes.SqlParameter{
		{Name: aws.String("session_id"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.SessionID}},
		{Name: aws.String("user_id"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.UserID}},
		{Name: aws.String("media_key"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.MediaKey}},
		{Name: aws.String("filename"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.Filename}},
		{Name: aws.String("media_type"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.MediaType}},
		{Name: aws.String("action"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.Action}},
		{Name: aws.String("ai_verdict"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.AIVerdict}},
		{Name: aws.String("ai_reason"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.AIReason}},
		{Name: aws.String("is_finalized"), Value: &rdsdatatypes.FieldMemberBooleanValue{Value: d.IsFinalized}},
		{Name: aws.String("media_metadata"), Value: &rdsdatatypes.FieldMemberStringValue{Value: mediaMeta}, TypeHint: rdsdatatypes.TypeHintJson},
		{Name: aws.String("embedding"), Value: &rdsdatatypes.FieldMemberStringValue{Value: embStr}},
		{Name: aws.String("created_at"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.CreatedAt}},
	}
	if err := c.exec(ctx, sql, params); err != nil {
		log.Error().Err(err).Str("sessionId", d.SessionID).Str("mediaKey", d.MediaKey).Msg("UpsertOverrideDecision failed")
		return fmt.Errorf("UpsertOverrideDecision: %w", err)
	}
	return nil
}

func (c *DataAPIClient) UpsertCaptionDecision(ctx context.Context, d CaptionDecision) error {
	mediaMeta := "{}"
	if len(d.MediaMetadata) > 0 {
		b, _ := json.Marshal(d.MediaMetadata)
		mediaMeta = string(b)
	}
	embStr := formatVector(d.Embedding)
	hashtagsStr := formatTextArray(d.Hashtags)
	mediaKeysStr := formatTextArray(d.MediaKeys)
	sql := `INSERT INTO caption_decisions (session_id, user_id, caption_text, hashtags, location_tag, media_keys, post_group_name, media_metadata, embedding, created_at)
		VALUES (:session_id, :user_id, :caption_text, :hashtags::text[], :location_tag, :media_keys::text[], :post_group_name, :media_metadata::jsonb, :embedding::vector, COALESCE(:created_at::timestamptz, NOW()))
		ON CONFLICT (session_id, post_group_name) DO UPDATE SET
			caption_text = EXCLUDED.caption_text, hashtags = EXCLUDED.hashtags, location_tag = EXCLUDED.location_tag,
			media_keys = EXCLUDED.media_keys, media_metadata = EXCLUDED.media_metadata, embedding = EXCLUDED.embedding, created_at = EXCLUDED.created_at`
	params := []rdsdatatypes.SqlParameter{
		{Name: aws.String("session_id"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.SessionID}},
		{Name: aws.String("user_id"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.UserID}},
		{Name: aws.String("caption_text"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.CaptionText}},
		{Name: aws.String("hashtags"), Value: &rdsdatatypes.FieldMemberStringValue{Value: hashtagsStr}},
		{Name: aws.String("location_tag"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.LocationTag}},
		{Name: aws.String("media_keys"), Value: &rdsdatatypes.FieldMemberStringValue{Value: mediaKeysStr}},
		{Name: aws.String("post_group_name"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.PostGroupName}},
		{Name: aws.String("media_metadata"), Value: &rdsdatatypes.FieldMemberStringValue{Value: mediaMeta}, TypeHint: rdsdatatypes.TypeHintJson},
		{Name: aws.String("embedding"), Value: &rdsdatatypes.FieldMemberStringValue{Value: embStr}},
		{Name: aws.String("created_at"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.CreatedAt}},
	}
	if err := c.exec(ctx, sql, params); err != nil {
		log.Error().Err(err).Str("sessionId", d.SessionID).Str("postGroupName", d.PostGroupName).Msg("UpsertCaptionDecision failed")
		return fmt.Errorf("UpsertCaptionDecision: %w", err)
	}
	return nil
}

func (c *DataAPIClient) UpsertPublishDecision(ctx context.Context, d PublishDecision) error {
	mediaMeta := "{}"
	if len(d.MediaMetadata) > 0 {
		b, _ := json.Marshal(d.MediaMetadata)
		mediaMeta = string(b)
	}
	embStr := formatVector(d.Embedding)
	hashtagsStr := formatTextArray(d.Hashtags)
	mediaKeysStr := formatTextArray(d.MediaKeys)
	sql := `INSERT INTO publish_decisions (session_id, user_id, platform, post_group_name, caption_text, hashtags, location_tag, media_keys, media_metadata, embedding, created_at)
		VALUES (:session_id, :user_id, :platform, :post_group_name, :caption_text, :hashtags::text[], :location_tag, :media_keys::text[], :media_metadata::jsonb, :embedding::vector, COALESCE(:created_at::timestamptz, NOW()))
		ON CONFLICT (session_id, post_group_name, platform) DO UPDATE SET
			caption_text = EXCLUDED.caption_text, hashtags = EXCLUDED.hashtags, location_tag = EXCLUDED.location_tag,
			media_keys = EXCLUDED.media_keys, media_metadata = EXCLUDED.media_metadata, embedding = EXCLUDED.embedding, created_at = EXCLUDED.created_at`
	params := []rdsdatatypes.SqlParameter{
		{Name: aws.String("session_id"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.SessionID}},
		{Name: aws.String("user_id"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.UserID}},
		{Name: aws.String("platform"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.Platform}},
		{Name: aws.String("post_group_name"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.PostGroupName}},
		{Name: aws.String("caption_text"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.CaptionText}},
		{Name: aws.String("hashtags"), Value: &rdsdatatypes.FieldMemberStringValue{Value: hashtagsStr}},
		{Name: aws.String("location_tag"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.LocationTag}},
		{Name: aws.String("media_keys"), Value: &rdsdatatypes.FieldMemberStringValue{Value: mediaKeysStr}},
		{Name: aws.String("media_metadata"), Value: &rdsdatatypes.FieldMemberStringValue{Value: mediaMeta}, TypeHint: rdsdatatypes.TypeHintJson},
		{Name: aws.String("embedding"), Value: &rdsdatatypes.FieldMemberStringValue{Value: embStr}},
		{Name: aws.String("created_at"), Value: &rdsdatatypes.FieldMemberStringValue{Value: d.CreatedAt}},
	}
	if err := c.exec(ctx, sql, params); err != nil {
		log.Error().Err(err).Str("sessionId", d.SessionID).Str("platform", d.Platform).Msg("UpsertPublishDecision failed")
		return fmt.Errorf("UpsertPublishDecision: %w", err)
	}
	return nil
}

func (c *DataAPIClient) QuerySimilar(ctx context.Context, table string, embedding []float32, topK int) ([]map[string]interface{}, error) {
	if !allowedTables[table] {
		return nil, fmt.Errorf("QuerySimilar: invalid table %q", table)
	}
	embStr := formatVector(embedding)
	sql := fmt.Sprintf(`SELECT *, 1 - (embedding <=> :emb::vector) AS similarity FROM %s ORDER BY embedding <=> :emb::vector LIMIT :topk`, table)
	params := []rdsdatatypes.SqlParameter{
		{Name: aws.String("emb"), Value: &rdsdatatypes.FieldMemberStringValue{Value: embStr}},
		{Name: aws.String("topk"), Value: &rdsdatatypes.FieldMemberLongValue{Value: int64(topK)}},
	}
	result, err := c.client.ExecuteStatement(ctx, &rdsdata.ExecuteStatementInput{
		ResourceArn: aws.String(c.clusterARN),
		SecretArn:   aws.String(c.secretARN),
		Database:    aws.String(c.database),
		Sql:         aws.String(sql),
		Parameters:  params,
	})
	if err != nil {
		log.Error().Err(err).Str("table", table).Msg("QuerySimilar failed")
		return nil, fmt.Errorf("QuerySimilar: %w", err)
	}
	rows := make([]map[string]interface{}, 0, len(result.Records))
	for _, rec := range result.Records {
		row := make(map[string]interface{})
		for i, col := range result.ColumnMetadata {
			if i >= len(rec) {
				break
			}
			name := aws.ToString(col.Name)
			f := rec[i]
			switch v := f.(type) {
			case *rdsdatatypes.FieldMemberStringValue:
				row[name] = v.Value
			case *rdsdatatypes.FieldMemberLongValue:
				row[name] = v.Value
			case *rdsdatatypes.FieldMemberBooleanValue:
				row[name] = v.Value
			case *rdsdatatypes.FieldMemberDoubleValue:
				row[name] = v.Value
			case *rdsdatatypes.FieldMemberIsNull:
				row[name] = nil
			case *rdsdatatypes.FieldMemberArrayValue:
				row[name] = v.Value
			case *rdsdatatypes.FieldMemberBlobValue:
				row[name] = v.Value
			default:
				row[name] = v
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func UpdateLastActivity(ctx context.Context, client *dynamodb.Client, tableName, sessionID string) error {
	pk := "SESSION#" + sessionID
	sk := "META"
	_, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: pk},
			"SK": &types.AttributeValueMemberS{Value: sk},
		},
		UpdateExpression: aws.String("SET lastActivityAt = :ts"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":ts": &types.AttributeValueMemberN{Value: strconv.FormatInt(time.Now().Unix(), 10)},
		},
	})
	if err != nil {
		return fmt.Errorf("UpdateLastActivity: %w", err)
	}
	return nil
}
