package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/fpang/ai-social-media-helper/internal/ai"
	"github.com/rs/zerolog/log"
)

// PollInput is the input for the batch poll Lambda.
type PollInput struct {
	BatchJobID string `json:"batch_job_id"`
}

// PollOutput is the output from the batch poll Lambda.
type PollOutput struct {
	State   string          `json:"state"`
	Results json.RawMessage `json:"results,omitempty"`
	Error   string          `json:"error,omitempty"`
}

func handler(ctx context.Context, input PollInput) (PollOutput, error) {
	if input.BatchJobID == "" {
		return PollOutput{}, fmt.Errorf("batch_job_id is required")
	}

	if err := ai.LoadGCPServiceAccount(); err != nil {
		return PollOutput{}, fmt.Errorf("failed to load GCP service account: %w", err)
	}

	client, err := ai.NewAIClient(ctx)
	if err != nil {
		return PollOutput{}, fmt.Errorf("failed to create AI client: %w", err)
	}

	status, err := ai.CheckGeminiBatch(ctx, client, input.BatchJobID)
	if err != nil {
		return PollOutput{}, fmt.Errorf("failed to check batch status: %w", err)
	}

	output := PollOutput{
		State: status.State,
		Error: status.Error,
	}

	if len(status.Results) > 0 {
		resultsJSON, err := json.Marshal(status.Results)
		if err != nil {
			log.Warn().Err(err).Msg("Failed to marshal batch results")
		} else {
			output.Results = resultsJSON
		}
	}

	log.Info().
		Str("batch_job_id", input.BatchJobID).
		Str("state", output.State).
		Int("result_count", len(status.Results)).
		Msg("Batch poll complete")

	return output, nil
}

func main() {
	lambda.Start(handler)
}
