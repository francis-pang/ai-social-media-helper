package ai

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"google.golang.org/genai"
)

// GeminiBatchResult holds a single response from a batch job.
type GeminiBatchResult struct {
	Index    int
	Response *genai.GenerateContentResponse
	Error    string
}

// GeminiBatchStatus represents the current state of a Gemini batch job.
type GeminiBatchStatus struct {
	State   string
	Results []GeminiBatchResult
	Error   string
}

// SubmitGeminiBatch submits an inline batch of GenerateContent requests to the
// Vertex AI / Gemini Batch API and returns the batch job name for polling.
func SubmitGeminiBatch(ctx context.Context, client *genai.Client, model string, requests []*genai.InlinedRequest) (string, error) {
	callStart := time.Now()
	log.Info().
		Str("model", model).
		Int("request_count", len(requests)).
		Msg("Submitting Gemini batch job")

	job, err := client.Batches.Create(ctx, model, &genai.BatchJobSource{
		InlinedRequests: requests,
	}, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create batch job: %w", err)
	}

	log.Info().
		Str("job_name", job.Name).
		Str("state", string(job.State)).
		Dur("duration", time.Since(callStart)).
		Msg("Gemini batch job submitted")

	return job.Name, nil
}

// CheckGeminiBatch polls the status of a Gemini batch job.
// Returns the current state and results (if the job has completed).
func CheckGeminiBatch(ctx context.Context, client *genai.Client, jobName string) (*GeminiBatchStatus, error) {
	job, err := client.Batches.Get(ctx, jobName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get batch job %s: %w", jobName, err)
	}

	status := &GeminiBatchStatus{
		State: string(job.State),
	}

	log.Debug().
		Str("job_name", jobName).
		Str("state", status.State).
		Msg("Checked Gemini batch job status")

	if job.State == genai.JobStateFailed {
		status.Error = "batch job failed"
		if job.Error != nil && job.Error.Message != "" {
			status.Error = job.Error.Message
		}
		return status, nil
	}

	if job.State == genai.JobStateSucceeded && job.Dest != nil && job.Dest.InlinedResponses != nil {
		for i, resp := range job.Dest.InlinedResponses {
			result := GeminiBatchResult{Index: i}
			if resp.Response != nil {
				result.Response = resp.Response
			}
			if resp.Error != nil && resp.Error.Message != "" {
				result.Error = resp.Error.Message
			}
			status.Results = append(status.Results, result)
		}
		log.Info().
			Str("job_name", jobName).
			Int("result_count", len(status.Results)).
			Msg("Gemini batch job completed with results")
	}

	return status, nil
}
