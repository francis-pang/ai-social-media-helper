package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/rdsdata"
	rdsdatatypes "github.com/aws/aws-sdk-go-v2/service/rdsdata/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/fpang/gemini-media-cli/internal/chat"
	"github.com/fpang/gemini-media-cli/internal/lambdaboot"
	"github.com/fpang/gemini-media-cli/internal/logging"
	"github.com/fpang/gemini-media-cli/internal/rag"
	"github.com/rs/zerolog/log"
	"google.golang.org/genai"
)

var (
	rdsDataClient  *rdsdata.Client
	rdsClient      *rds.Client
	dynamoClient   *dynamodb.Client
	ssmClient      *ssm.Client
	genaiClient    *genai.Client
	clusterARN     string
	secretARN      string
	database       string
	profilesTable  string
	stagingTable   string
)

type stagingItem struct {
	PK           string `dynamodbav:"PK"`
	SK           string `dynamodbav:"SK"`
	FeedbackJSON string `dynamodbav:"feedbackJSON"`
	EventType    string `dynamodbav:"eventType"`
	SessionID    string `dynamodbav:"sessionId"`
}

// handler runs the daily batch lifecycle (DDR-068):
// staging ingest → Aurora embed+insert → profile build → cleanup → Aurora stop.
func handler(ctx context.Context) error {
	if clusterARN == "" || secretARN == "" || database == "" || profilesTable == "" || stagingTable == "" {
		log.Warn().Msg("Required env vars not configured")
		return nil
	}

	items, err := readStagingItems(ctx)
	if err != nil {
		log.Error().Err(err).Msg("Failed to read staging items")
		return err
	}

	if len(items) == 0 {
		log.Info().Msg("No staging items to process, skipping batch")
		return nil
	}

	log.Info().Int("count", len(items)).Msg("Staging items found, starting batch")

	if err := ensureAuroraAvailable(ctx); err != nil {
		return err
	}

	lambdaboot.LoadGeminiKey(ssmClient)
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("Gemini API key not available")
	}
	genaiClient, err = chat.NewGeminiClient(ctx, apiKey)
	if err != nil {
		return fmt.Errorf("create Gemini client: %w", err)
	}

	dataAPI := rag.NewDataAPIClient(rdsDataClient, clusterARN, secretARN, database)
	processed := ingestStagingItems(ctx, items, dataAPI)
	log.Info().Int("processed", len(processed)).Int("total", len(items)).Msg("Staging items ingested to Aurora")

	if err := buildAndWriteProfile(ctx, dataAPI); err != nil {
		log.Error().Err(err).Msg("Profile build failed (continuing to cleanup)")
	}

	deleteStagingItems(ctx, processed)
	stopAurora(ctx)

	return nil
}

// ---------------------------------------------------------------------------
// Phase 1: Read staging items
// ---------------------------------------------------------------------------

func readStagingItems(ctx context.Context) ([]stagingItem, error) {
	var items []stagingItem
	var lastKey map[string]types.AttributeValue

	for {
		input := &dynamodb.QueryInput{
			TableName:              aws.String(stagingTable),
			KeyConditionExpression: aws.String("PK = :pk"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":pk": &types.AttributeValueMemberS{Value: "STAGING"},
			},
			ExclusiveStartKey: lastKey,
		}

		out, err := dynamoClient.Query(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("query staging table: %w", err)
		}

		for _, item := range out.Items {
			var si stagingItem
			if err := attributevalue.UnmarshalMap(item, &si); err != nil {
				log.Warn().Err(err).Msg("failed to unmarshal staging item, skipping")
				continue
			}
			items = append(items, si)
		}

		lastKey = out.LastEvaluatedKey
		if lastKey == nil {
			break
		}
	}

	return items, nil
}

// ---------------------------------------------------------------------------
// Phase 2: Aurora lifecycle
// ---------------------------------------------------------------------------

func extractClusterID() string {
	id := clusterARN
	if idx := strings.LastIndex(clusterARN, ":"); idx >= 0 && idx < len(clusterARN)-1 {
		id = clusterARN[idx+1:]
	}
	return id
}

func ensureAuroraAvailable(ctx context.Context) error {
	clusterID := extractClusterID()

	desc, err := rdsClient.DescribeDBClusters(ctx, &rds.DescribeDBClustersInput{
		DBClusterIdentifier: aws.String(clusterID),
	})
	if err != nil {
		return fmt.Errorf("DescribeDBClusters: %w", err)
	}
	if len(desc.DBClusters) == 0 {
		return fmt.Errorf("cluster %s not found", clusterID)
	}

	status := aws.ToString(desc.DBClusters[0].Status)
	if status == "available" {
		log.Info().Msg("Aurora already available")
		return nil
	}

	if status == "stopped" {
		log.Info().Msg("Starting Aurora cluster")
		if _, err := rdsClient.StartDBCluster(ctx, &rds.StartDBClusterInput{
			DBClusterIdentifier: aws.String(clusterID),
		}); err != nil {
			return fmt.Errorf("StartDBCluster: %w", err)
		}
	}

	// Poll until available (up to 5 minutes)
	for i := 0; i < 30; i++ {
		time.Sleep(10 * time.Second)
		desc, err := rdsClient.DescribeDBClusters(ctx, &rds.DescribeDBClustersInput{
			DBClusterIdentifier: aws.String(clusterID),
		})
		if err != nil {
			log.Warn().Err(err).Int("attempt", i).Msg("DescribeDBClusters poll failed")
			continue
		}
		if len(desc.DBClusters) > 0 && aws.ToString(desc.DBClusters[0].Status) == "available" {
			log.Info().Int("pollAttempts", i+1).Msg("Aurora is available")
			return nil
		}
	}

	return fmt.Errorf("Aurora did not become available within timeout")
}

func stopAurora(ctx context.Context) {
	clusterID := extractClusterID()
	if _, err := rdsClient.StopDBCluster(ctx, &rds.StopDBClusterInput{
		DBClusterIdentifier: aws.String(clusterID),
	}); err != nil {
		log.Error().Err(err).Msg("StopDBCluster failed")
		return
	}
	log.Info().Str("clusterId", clusterID).Msg("Aurora stop requested")
}

// ---------------------------------------------------------------------------
// Phase 3: Batch embed + insert to Aurora
// ---------------------------------------------------------------------------

func ingestStagingItems(ctx context.Context, items []stagingItem, dataAPI *rag.DataAPIClient) []stagingItem {
	var processed []stagingItem

	for _, item := range items {
		var feedback rag.ContentFeedback
		if err := json.Unmarshal([]byte(item.FeedbackJSON), &feedback); err != nil {
			log.Error().Err(err).Str("sk", item.SK).Msg("failed to parse feedback JSON")
			continue
		}

		if err := embedAndUpsert(ctx, dataAPI, feedback); err != nil {
			log.Error().Err(err).Str("sk", item.SK).Str("eventType", feedback.EventType).Msg("embed+upsert failed")
			continue
		}

		processed = append(processed, item)
	}

	return processed
}

func embedAndUpsert(ctx context.Context, dataAPI *rag.DataAPIClient, feedback rag.ContentFeedback) error {
	text := rag.BuildEmbeddingInput(feedback)
	embedding, err := rag.GenerateEmbedding(ctx, genaiClient, text)
	if err != nil {
		return fmt.Errorf("generate embedding: %w", err)
	}

	createdAt := feedback.Timestamp
	if createdAt == "" {
		createdAt = time.Now().UTC().Format(time.RFC3339)
	}

	switch feedback.EventType {
	case rag.EventTriageFinalized:
		saveable := strings.EqualFold(feedback.UserVerdict, "keep") || strings.EqualFold(feedback.UserVerdict, "save")
		if feedback.UserVerdict == "" {
			saveable = strings.EqualFold(feedback.AIVerdict, "keep") || strings.EqualFold(feedback.AIVerdict, "save")
		}
		return dataAPI.UpsertTriageDecision(ctx, rag.TriageDecision{
			SessionID: feedback.SessionID, UserID: feedback.UserID, MediaKey: feedback.MediaKey,
			Filename: getMeta(feedback.Metadata, "filename"), MediaType: feedback.MediaType,
			Saveable: saveable, Reason: feedback.Reason, MediaMetadata: feedback.Metadata,
			Embedding: embedding, CreatedAt: createdAt,
		})

	case rag.EventSelectionFinalized:
		selected := strings.EqualFold(feedback.UserVerdict, "keep") || strings.EqualFold(feedback.UserVerdict, "select")
		if feedback.UserVerdict == "" {
			selected = strings.EqualFold(feedback.AIVerdict, "keep") || strings.EqualFold(feedback.AIVerdict, "select")
		}
		return dataAPI.UpsertSelectionDecision(ctx, rag.SelectionDecision{
			SessionID: feedback.SessionID, UserID: feedback.UserID, MediaKey: feedback.MediaKey,
			Filename: getMeta(feedback.Metadata, "filename"), MediaType: feedback.MediaType,
			Selected: selected, ExclusionCategory: getMeta(feedback.Metadata, "exclusionCategory"),
			ExclusionReason: getMeta(feedback.Metadata, "exclusionReason"),
			SceneGroup: getMeta(feedback.Metadata, "sceneGroup"), MediaMetadata: feedback.Metadata,
			Embedding: embedding, CreatedAt: createdAt,
		})

	case rag.EventOverrideAction, rag.EventOverridesFinalized:
		action := feedback.UserVerdict
		if action == "" {
			action = getMeta(feedback.Metadata, "action")
		}
		return dataAPI.UpsertOverrideDecision(ctx, rag.OverrideDecision{
			SessionID: feedback.SessionID, UserID: feedback.UserID, MediaKey: feedback.MediaKey,
			Filename: getMeta(feedback.Metadata, "filename"), MediaType: feedback.MediaType,
			Action: action, AIVerdict: feedback.AIVerdict, AIReason: feedback.Reason,
			IsFinalized: feedback.EventType == rag.EventOverridesFinalized, MediaMetadata: feedback.Metadata,
			Embedding: embedding, CreatedAt: createdAt,
		})

	case rag.EventDescriptionFinalized:
		captionText := feedback.AIVerdict
		if captionText == "" {
			captionText = getMeta(feedback.Metadata, "captionText")
		}
		d := rag.CaptionDecision{
			SessionID: feedback.SessionID, UserID: feedback.UserID, CaptionText: captionText,
			Hashtags: parseStringSlice(feedback.Metadata, "hashtags"),
			LocationTag: getMeta(feedback.Metadata, "locationTag"),
			MediaKeys: parseStringSlice(feedback.Metadata, "mediaKeys"),
			PostGroupName: getMeta(feedback.Metadata, "postGroupName"), MediaMetadata: feedback.Metadata,
			Embedding: embedding, CreatedAt: createdAt,
		}
		if d.PostGroupName == "" {
			d.PostGroupName = feedback.SessionID + "-caption"
		}
		return dataAPI.UpsertCaptionDecision(ctx, d)

	case rag.EventPublishFinalized:
		d := rag.PublishDecision{
			SessionID: feedback.SessionID, UserID: feedback.UserID,
			Platform: getMeta(feedback.Metadata, "platform"),
			PostGroupName: getMeta(feedback.Metadata, "postGroupName"),
			CaptionText: getMeta(feedback.Metadata, "captionText"),
			Hashtags: parseStringSlice(feedback.Metadata, "hashtags"),
			LocationTag: getMeta(feedback.Metadata, "locationTag"),
			MediaKeys: parseStringSlice(feedback.Metadata, "mediaKeys"), MediaMetadata: feedback.Metadata,
			Embedding: embedding, CreatedAt: createdAt,
		}
		if d.Platform == "" {
			d.Platform = "instagram"
		}
		if d.PostGroupName == "" {
			d.PostGroupName = feedback.SessionID + "-publish"
		}
		return dataAPI.UpsertPublishDecision(ctx, d)

	default:
		log.Warn().Str("eventType", feedback.EventType).Msg("unhandled event type in batch")
		return nil
	}
}

func getMeta(m map[string]string, key string) string {
	if m == nil {
		return ""
	}
	return m[key]
}

func parseStringSlice(m map[string]string, key string) []string {
	if m == nil {
		return nil
	}
	s := m[key]
	if s == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(s, ",") {
		if t := strings.TrimSpace(part); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Phase 4: Build preference profile (from DDR-066 profile Lambda logic)
// ---------------------------------------------------------------------------

func buildAndWriteProfile(ctx context.Context, dataAPI *rag.DataAPIClient) error {
	triageRows, err := executeQuery(ctx, `SELECT * FROM triage_decisions ORDER BY created_at DESC`)
	if err != nil {
		return fmt.Errorf("query triage_decisions: %w", err)
	}

	selectionRows, err := executeQuery(ctx, `SELECT * FROM selection_decisions ORDER BY created_at DESC`)
	if err != nil {
		return fmt.Errorf("query selection_decisions: %w", err)
	}

	overrideRows, err := executeQuery(ctx, `SELECT * FROM override_decisions WHERE is_finalized = true ORDER BY created_at DESC`)
	if err != nil {
		return fmt.Errorf("query override_decisions: %w", err)
	}

	captionRows, err := executeQuery(ctx, `SELECT * FROM caption_decisions ORDER BY created_at DESC`)
	if err != nil {
		return fmt.Errorf("query caption_decisions: %w", err)
	}

	triageDecisions := parseTriageDecisions(triageRows)
	selectionDecisions := parseSelectionDecisions(selectionRows)
	overrideDecisions := parseOverrideDecisions(overrideRows)
	captionDecisions := parseCaptionDecisions(captionRows)

	stats := rag.ComputeStats(triageDecisions, overrideDecisions, selectionDecisions)
	prompt := rag.FormatStatsForLLM(stats)

	systemPrompt := "You are a preference profile writer. Write a concise bullet-point preference profile based on the user's media curation statistics. Be specific. Do not invent patterns not present in the data."
	config := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{{Text: systemPrompt}},
		},
	}

	modelName := chat.GetModelName()
	resp, err := genaiClient.Models.GenerateContent(ctx, modelName, genai.Text(prompt), config)
	if err != nil {
		return fmt.Errorf("Gemini GenerateContent: %w", err)
	}

	profileText := resp.Text()
	if profileText == "" {
		profileText = "No preference profile generated."
	}

	captionExamplesText := buildCaptionExamples(captionDecisions, 10)

	statsMap := map[string]interface{}{
		"totalSessions":       stats.TotalSessions,
		"totalDecisions":      stats.TotalDecisions,
		"keepRate":            stats.KeepRate,
		"overrideRate":        stats.OverrideRate,
		"keepReasonCounts":    stats.KeepReasonCounts,
		"discardReasonCounts": stats.DiscardReasonCounts,
		"overridePatterns":    stats.OverridePatterns,
		"mediaTypeBreakdown":  stats.MediaTypeBreakdown,
	}
	statsJSON, _ := json.Marshal(statsMap)

	computedAt := time.Now().UTC().Format(time.RFC3339)
	item := map[string]types.AttributeValue{
		"PK":                  &types.AttributeValueMemberS{Value: "PROFILE#preference"},
		"SK":                  &types.AttributeValueMemberS{Value: "latest"},
		"profileText":         &types.AttributeValueMemberS{Value: profileText},
		"captionExamplesText": &types.AttributeValueMemberS{Value: captionExamplesText},
		"stats":               &types.AttributeValueMemberS{Value: string(statsJSON)},
		"computedAt":          &types.AttributeValueMemberS{Value: computedAt},
		"version":             &types.AttributeValueMemberN{Value: "1"},
	}

	if _, err := dynamoClient.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(profilesTable),
		Item:      item,
	}); err != nil {
		return fmt.Errorf("write profile to DynamoDB: %w", err)
	}

	log.Info().
		Str("computedAt", computedAt).
		Int("triageCount", len(triageDecisions)).
		Int("selectionCount", len(selectionDecisions)).
		Int("overrideCount", len(overrideDecisions)).
		Int("captionCount", len(captionDecisions)).
		Msg("Preference profile computed and stored")

	return nil
}

func buildCaptionExamples(decisions []rag.CaptionDecision, n int) string {
	if n > len(decisions) {
		n = len(decisions)
	}
	var lines []string
	for i := 0; i < n; i++ {
		line := strings.TrimSpace(decisions[i].CaptionText)
		line = strings.ReplaceAll(line, "\n", " ")
		quoted := `"` + strings.ReplaceAll(line, `"`, `\"`) + `"`
		lines = append(lines, quoted)
	}
	var sb strings.Builder
	for i, q := range lines {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(fmt.Sprintf("%d. %s", i+1, q))
	}
	return sb.String()
}

// ---------------------------------------------------------------------------
// Phase 5: Delete processed staging items
// ---------------------------------------------------------------------------

func deleteStagingItems(ctx context.Context, items []stagingItem) {
	const batchSize = 25
	for i := 0; i < len(items); i += batchSize {
		end := i + batchSize
		if end > len(items) {
			end = len(items)
		}

		var requests []types.WriteRequest
		for _, item := range items[i:end] {
			requests = append(requests, types.WriteRequest{
				DeleteRequest: &types.DeleteRequest{
					Key: map[string]types.AttributeValue{
						"PK": &types.AttributeValueMemberS{Value: item.PK},
						"SK": &types.AttributeValueMemberS{Value: item.SK},
					},
				},
			})
		}

		result, err := dynamoClient.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{
			RequestItems: map[string][]types.WriteRequest{
				stagingTable: requests,
			},
		})
		if err != nil {
			log.Error().Err(err).Int("batch", i/batchSize).Msg("BatchWriteItem delete failed")
			continue
		}

		unprocessed := result.UnprocessedItems[stagingTable]
		for retries := 0; len(unprocessed) > 0 && retries < 5; retries++ {
			backoff := time.Duration(1<<retries) * 100 * time.Millisecond
			log.Debug().Int("unprocessed", len(unprocessed)).Int("retry", retries+1).Dur("backoff", backoff).Msg("deleteStagingItems: retrying unprocessed items")
			time.Sleep(backoff)
			retryResult, retryErr := dynamoClient.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{
				RequestItems: map[string][]types.WriteRequest{stagingTable: unprocessed},
			})
			if retryErr != nil {
				log.Warn().Err(retryErr).Int("unprocessed", len(unprocessed)).Msg("deleteStagingItems: retry failed")
				break
			}
			unprocessed = retryResult.UnprocessedItems[stagingTable]
		}
		if len(unprocessed) > 0 {
			log.Warn().Int("remaining", len(unprocessed)).Msg("deleteStagingItems: unprocessed items remain after retries")
		}
	}

	log.Info().Int("count", len(items)).Msg("Staging items deleted")
}

// ---------------------------------------------------------------------------
// Aurora Data API helpers (for profile queries)
// ---------------------------------------------------------------------------

func executeQuery(ctx context.Context, sql string) ([]map[string]interface{}, error) {
	result, err := rdsDataClient.ExecuteStatement(ctx, &rdsdata.ExecuteStatementInput{
		ResourceArn: aws.String(clusterARN),
		SecretArn:   aws.String(secretARN),
		Database:    aws.String(database),
		Sql:         aws.String(sql),
	})
	if err != nil {
		return nil, err
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

func parseTriageDecisions(rows []map[string]interface{}) []rag.TriageDecision {
	var out []rag.TriageDecision
	for _, row := range rows {
		var d rag.TriageDecision
		if v, ok := row["session_id"].(string); ok {
			d.SessionID = v
		}
		if v, ok := row["user_id"].(string); ok {
			d.UserID = v
		}
		if v, ok := row["media_key"].(string); ok {
			d.MediaKey = v
		}
		if v, ok := row["filename"].(string); ok {
			d.Filename = v
		}
		if v, ok := row["media_type"].(string); ok {
			d.MediaType = v
		}
		if v, ok := row["saveable"].(bool); ok {
			d.Saveable = v
		}
		if v, ok := row["reason"].(string); ok {
			d.Reason = v
		}
		if v, ok := row["created_at"].(string); ok {
			d.CreatedAt = v
		}
		if v, ok := row["media_metadata"].(string); ok && v != "" && v != "{}" {
			_ = json.Unmarshal([]byte(v), &d.MediaMetadata)
		}
		out = append(out, d)
	}
	return out
}

func parseSelectionDecisions(rows []map[string]interface{}) []rag.SelectionDecision {
	var out []rag.SelectionDecision
	for _, row := range rows {
		var d rag.SelectionDecision
		if v, ok := row["session_id"].(string); ok {
			d.SessionID = v
		}
		if v, ok := row["user_id"].(string); ok {
			d.UserID = v
		}
		if v, ok := row["media_key"].(string); ok {
			d.MediaKey = v
		}
		if v, ok := row["filename"].(string); ok {
			d.Filename = v
		}
		if v, ok := row["media_type"].(string); ok {
			d.MediaType = v
		}
		if v, ok := row["selected"].(bool); ok {
			d.Selected = v
		}
		if v, ok := row["exclusion_category"].(string); ok {
			d.ExclusionCategory = v
		}
		if v, ok := row["exclusion_reason"].(string); ok {
			d.ExclusionReason = v
		}
		if v, ok := row["scene_group"].(string); ok {
			d.SceneGroup = v
		}
		if v, ok := row["created_at"].(string); ok {
			d.CreatedAt = v
		}
		out = append(out, d)
	}
	return out
}

func parseOverrideDecisions(rows []map[string]interface{}) []rag.OverrideDecision {
	var out []rag.OverrideDecision
	for _, row := range rows {
		var d rag.OverrideDecision
		if v, ok := row["session_id"].(string); ok {
			d.SessionID = v
		}
		if v, ok := row["user_id"].(string); ok {
			d.UserID = v
		}
		if v, ok := row["media_key"].(string); ok {
			d.MediaKey = v
		}
		if v, ok := row["filename"].(string); ok {
			d.Filename = v
		}
		if v, ok := row["media_type"].(string); ok {
			d.MediaType = v
		}
		if v, ok := row["action"].(string); ok {
			d.Action = v
		}
		if v, ok := row["ai_verdict"].(string); ok {
			d.AIVerdict = v
		}
		if v, ok := row["ai_reason"].(string); ok {
			d.AIReason = v
		}
		if v, ok := row["is_finalized"].(bool); ok {
			d.IsFinalized = v
		}
		if v, ok := row["created_at"].(string); ok {
			d.CreatedAt = v
		}
		out = append(out, d)
	}
	return out
}

func parseCaptionDecisions(rows []map[string]interface{}) []rag.CaptionDecision {
	var out []rag.CaptionDecision
	for _, row := range rows {
		var d rag.CaptionDecision
		if v, ok := row["session_id"].(string); ok {
			d.SessionID = v
		}
		if v, ok := row["user_id"].(string); ok {
			d.UserID = v
		}
		if v, ok := row["caption_text"].(string); ok {
			d.CaptionText = v
		}
		if v, ok := row["location_tag"].(string); ok {
			d.LocationTag = v
		}
		if v, ok := row["post_group_name"].(string); ok {
			d.PostGroupName = v
		}
		if v, ok := row["created_at"].(string); ok {
			d.CreatedAt = v
		}
		out = append(out, d)
	}
	return out
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	logging.Init()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load AWS config")
	}

	rdsDataClient = rdsdata.NewFromConfig(cfg)
	rdsClient = rds.NewFromConfig(cfg)
	dynamoClient = dynamodb.NewFromConfig(cfg)
	ssmClient = ssm.NewFromConfig(cfg)

	clusterARN = os.Getenv("AURORA_CLUSTER_ARN")
	secretARN = os.Getenv("AURORA_SECRET_ARN")
	database = os.Getenv("AURORA_DATABASE_NAME")
	profilesTable = os.Getenv("RAG_PROFILES_TABLE_NAME")
	stagingTable = os.Getenv("STAGING_TABLE_NAME")

	if os.Getenv("SSM_API_KEY_PARAM") == "" {
		os.Setenv("SSM_API_KEY_PARAM", "/ai-social-media/prod/gemini-api-key")
	}

	lambda.Start(handler)
}
