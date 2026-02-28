package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/fpang/gemini-media-cli/internal/rag"
)

type DDBProfileReader struct {
	Client    *dynamodb.Client
	TableName string
}

func (r *DDBProfileReader) GetPreferenceProfile(ctx context.Context) (*rag.PreferenceProfile, error) {
	result, err := r.Client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(r.TableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: "PROFILE#preference"},
			"SK": &types.AttributeValueMemberS{Value: "latest"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("GetItem: %w", err)
	}
	if result.Item == nil {
		return nil, nil
	}

	profile := &rag.PreferenceProfile{}

	if v, ok := result.Item["profileText"].(*types.AttributeValueMemberS); ok {
		profile.ProfileText = v.Value
	}
	if v, ok := result.Item["captionExamplesText"].(*types.AttributeValueMemberS); ok {
		profile.CaptionExamplesText = v.Value
	}
	if v, ok := result.Item["stats"].(*types.AttributeValueMemberS); ok && v.Value != "" {
		_ = json.Unmarshal([]byte(v.Value), &profile.Stats)
	}
	if v, ok := result.Item["computedAt"].(*types.AttributeValueMemberS); ok {
		profile.ComputedAt = v.Value
	}

	return profile, nil
}
