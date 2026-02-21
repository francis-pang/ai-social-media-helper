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
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/rdsdata"
	rdsdatatypes "github.com/aws/aws-sdk-go-v2/service/rdsdata/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/fpang/gemini-media-cli/internal/chat"
	"github.com/fpang/gemini-media-cli/internal/lambdaboot"
	"github.com/fpang/gemini-media-cli/internal/rag"
	"github.com/rs/zerolog/log"
	"google.golang.org/genai"
)

var rdsDataClient *rdsdata.Client
var dynamoClient *dynamodb.Client
var ssmClient *ssm.Client
var clusterARN, secretARN, database, tableName string

func handler(ctx context.Context) error {
	if clusterARN == "" || secretARN == "" || database == "" || tableName == "" {
		log.Warn().Msg("Required env vars not configured (AURORA_CLUSTER_ARN, AURORA_SECRET_ARN, AURORA_DATABASE_NAME, RAG_PROFILES_TABLE_NAME)")
		return nil
	}

	// 1. Query Aurora via Data API for all historical decisions
	triageRows, err := executeQuery(ctx, `SELECT * FROM triage_decisions ORDER BY created_at DESC`)
	if err != nil {
		log.Error().Err(err).Msg("Failed to query triage_decisions")
		return err
	}

	selectionRows, err := executeQuery(ctx, `SELECT * FROM selection_decisions ORDER BY created_at DESC`)
	if err != nil {
		log.Error().Err(err).Msg("Failed to query selection_decisions")
		return err
	}

	overrideRows, err := executeQuery(ctx, `SELECT * FROM override_decisions WHERE is_finalized = true ORDER BY created_at DESC`)
	if err != nil {
		log.Error().Err(err).Msg("Failed to query override_decisions")
		return err
	}

	captionRows, err := executeQuery(ctx, `SELECT * FROM caption_decisions ORDER BY created_at DESC`)
	if err != nil {
		log.Error().Err(err).Msg("Failed to query caption_decisions")
		return err
	}

	// 2. Parse results into rag types
	triageDecisions := parseTriageDecisions(triageRows)
	selectionDecisions := parseSelectionDecisions(selectionRows)
	overrideDecisions := parseOverrideDecisions(overrideRows)
	captionDecisions := parseCaptionDecisions(captionRows)

	// 3. Compute stats
	stats := rag.ComputeStats(triageDecisions, overrideDecisions, selectionDecisions)

	// 4. Format stats for LLM
	prompt := rag.FormatStatsForLLM(stats)

	// 5. Load Gemini API key from SSM
	lambdaboot.LoadGeminiKey(ssmClient)
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		log.Error().Msg("Gemini API key not available")
		return nil
	}

	// 6. Call Gemini to generate preference profile
	geminiClient, err := chat.NewGeminiClient(ctx, apiKey)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create Gemini client")
		return err
	}
	systemPrompt := "You are a preference profile writer. Write a concise bullet-point preference profile based on the user's media curation statistics. Be specific. Do not invent patterns not present in the data."
	config := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{{Text: systemPrompt}},
		},
	}

	modelName := chat.GetModelName()
	resp, err := geminiClient.Models.GenerateContent(ctx, modelName, genai.Text(prompt), config)
	if err != nil {
		log.Error().Err(err).Msg("Failed to generate profile from Gemini")
		return err
	}

	profileText := resp.Text()
	if profileText == "" {
		log.Warn().Msg("Gemini returned empty profile")
		profileText = "No preference profile generated."
	}

	// 7. Build caption style examples from last 10 caption_decisions
	captionExamplesText := buildCaptionExamples(captionDecisions, 10)

	// 8. Build stats JSON for storage
	statsMap := map[string]interface{}{
		"totalSessions":      stats.TotalSessions,
		"totalDecisions":     stats.TotalDecisions,
		"keepRate":           stats.KeepRate,
		"overrideRate":       stats.OverrideRate,
		"keepReasonCounts":   stats.KeepReasonCounts,
		"discardReasonCounts": stats.DiscardReasonCounts,
		"overridePatterns":   stats.OverridePatterns,
		"mediaTypeBreakdown": stats.MediaTypeBreakdown,
	}
	statsJSON, _ := json.Marshal(statsMap)

	// 9. Write to DynamoDB
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

	_, err = dynamoClient.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(tableName),
		Item:      item,
	})
	if err != nil {
		log.Error().Err(err).Msg("Failed to write profile to DynamoDB")
		return err
	}

	log.Info().
		Str("computedAt", computedAt).
		Int("triageCount", len(triageDecisions)).
		Int("overrideCount", len(overrideDecisions)).
		Msg("RAG preference profile computed and stored")
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
		// hashtags, media_keys may be arrays - skip for caption examples
		out = append(out, d)
	}
	return out
}

func main() {
	cfg, err := awsconfig.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load AWS config")
	}

	rdsDataClient = rdsdata.NewFromConfig(cfg)
	dynamoClient = dynamodb.NewFromConfig(cfg)
	ssmClient = ssm.NewFromConfig(cfg)

	clusterARN = os.Getenv("AURORA_CLUSTER_ARN")
	secretARN = os.Getenv("AURORA_SECRET_ARN")
	database = os.Getenv("AURORA_DATABASE_NAME")
	tableName = os.Getenv("RAG_PROFILES_TABLE_NAME")
	if os.Getenv("SSM_API_KEY_PARAM") == "" {
		os.Setenv("SSM_API_KEY_PARAM", "/ai-social-media/prod/gemini-api-key")
	}

	lambda.Start(handler)
}
