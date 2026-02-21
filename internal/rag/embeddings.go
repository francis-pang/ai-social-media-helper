package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/rs/zerolog/log"
)

type titanEmbedRequest struct {
	InputText   string `json:"inputText"`
	Dimensions  int    `json:"dimensions"`
	Normalize   bool   `json:"normalize"`
}

type titanEmbedResponse struct {
	Embedding            []float64 `json:"embedding"`
	InputTextTokenCount  int       `json:"inputTextTokenCount"`
}

func GenerateEmbedding(ctx context.Context, client *bedrockruntime.Client, modelID string, text string) ([]float32, error) {
	if modelID == "" {
		modelID = "amazon.titan-embed-text-v2:0"
	}

	req := titanEmbedRequest{
		InputText:  text,
		Dimensions: 1024,
		Normalize:  true,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	result, err := client.InvokeModel(ctx, &bedrockruntime.InvokeModelInput{
		ModelId:     aws.String(modelID),
		ContentType: aws.String("application/json"),
		Body:        body,
	})
	if err != nil {
		log.Error().Err(err).Str("modelId", modelID).Msg("Bedrock InvokeModel failed")
		return nil, fmt.Errorf("InvokeModel: %w", err)
	}

	var resp titanEmbedResponse
	if err := json.NewDecoder(bytes.NewReader(result.Body)).Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	embedding := make([]float32, len(resp.Embedding))
	for i, v := range resp.Embedding {
		embedding[i] = float32(v)
	}
	return embedding, nil
}

func BuildEmbeddingInput(event ContentFeedback) string {
	outcome := event.UserVerdict
	if outcome == "" {
		outcome = event.AIVerdict
	}

	var parts []string
	parts = append(parts, outcome)
	if event.Reason != "" {
		parts = append(parts, event.Reason)
	}

	var meta []string
	if event.MediaType != "" {
		meta = append(meta, event.MediaType)
	}
	if event.MediaKey != "" {
		meta = append(meta, event.MediaKey)
	}
	for k, v := range event.Metadata {
		if v != "" {
			meta = append(meta, fmt.Sprintf("%s: %s", k, v))
		}
	}
	if len(meta) > 0 {
		parts = append(parts, strings.Join(meta, ", "))
	}

	return strings.Join(parts, " — ") + " | " + strings.Join(meta, ", ")
}
