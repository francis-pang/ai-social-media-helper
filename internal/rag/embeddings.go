package rag

import (
	"context"
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"
	"google.golang.org/genai"
)

func GenerateEmbedding(ctx context.Context, client *genai.Client, text string) ([]float32, error) {
	dim := int32(1024)
	result, err := client.Models.EmbedContent(ctx, "gemini-embedding-001",
		[]*genai.Content{genai.NewContentFromText(text, genai.RoleUser)},
		&genai.EmbedContentConfig{OutputDimensionality: &dim},
	)
	if err != nil {
		log.Error().Err(err).Msg("Gemini EmbedContent failed")
		return nil, fmt.Errorf("EmbedContent: %w", err)
	}
	return result.Embeddings[0].Values, nil
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
