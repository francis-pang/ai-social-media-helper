package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
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

// gcsInputKey extracts the GCS object name from a full GCS URI (gs://bucket/path).
func gcsInputKey(gcsURI string) (bucket, object string) {
	s := strings.TrimPrefix(gcsURI, "gs://")
	parts := strings.SplitN(s, "/", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return parts[0], ""
}

// newGCSClient creates a GCS storage client using ADC (GOOGLE_APPLICATION_CREDENTIALS).
func newGCSClient(ctx context.Context) (*storage.Client, error) {
	var opts []option.ClientOption
	if creds := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); creds != "" {
		opts = append(opts, option.WithCredentialsFile(creds))
	}
	client, err := storage.NewClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCS client: %w", err)
	}
	return client, nil
}

// gcpServiceAccountEmail extracts the client_email from the GCP service
// account JSON already loaded into the environment by LoadGCPServiceAccount.
func gcpServiceAccountEmail() string {
	saJSON := os.Getenv("GCP_SERVICE_ACCOUNT_JSON")
	if saJSON == "" {
		return ""
	}
	var sa struct {
		ClientEmail string `json:"client_email"`
	}
	if err := json.Unmarshal([]byte(saJSON), &sa); err != nil {
		return ""
	}
	return sa.ClientEmail
}

// batchJSONLRow is the Vertex AI batch input format per line.
type batchJSONLRow struct {
	Request batchJSONLRequest `json:"request"`
}

type batchJSONLRequest struct {
	Contents           []*genai.Content            `json:"contents"`
	GenerationConfig   *genai.GenerateContentConfig `json:"generationConfig,omitempty"`
	SystemInstruction  *genai.Content              `json:"systemInstruction,omitempty"`
}

// batchOutputRow is the Vertex AI batch output format per line.
type batchOutputRow struct {
	Status   string                        `json:"status"`
	Response *genai.GenerateContentResponse `json:"response"`
}

// uploadBatchInputToGCS serializes InlinedRequests to JSONL and uploads to GCS.
// Returns the full GCS URI of the uploaded file.
func uploadBatchInputToGCS(ctx context.Context, bucketName string, requests []*genai.InlinedRequest) (string, error) {
	gcsClient, err := newGCSClient(ctx)
	if err != nil {
		return "", err
	}
	defer gcsClient.Close()

	var buf bytes.Buffer
	for _, req := range requests {
		row := batchJSONLRow{
			Request: batchJSONLRequest{
				Contents: req.Contents,
			},
		}
		if req.Config != nil {
			// Copy config and nil out SystemInstruction so it doesn't appear
			// inside generationConfig in the JSONL output. Vertex AI requires
			// systemInstruction as a sibling of generationConfig, not nested
			// within it.
			cfgCopy := *req.Config
			row.Request.SystemInstruction = cfgCopy.SystemInstruction
			cfgCopy.SystemInstruction = nil
			row.Request.GenerationConfig = &cfgCopy
		}
		line, err := json.Marshal(row)
		if err != nil {
			return "", fmt.Errorf("failed to marshal batch request: %w", err)
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}

	objectName := fmt.Sprintf("batch-input/%s.jsonl", uuid.New().String())
	gcsURI := fmt.Sprintf("gs://%s/%s", bucketName, objectName)

	obj := gcsClient.Bucket(bucketName).Object(objectName)
	w := obj.NewWriter(ctx)
	w.ContentType = "application/jsonl"
	if _, err := io.Copy(w, &buf); err != nil {
		_ = w.Close()
		return "", fmt.Errorf("failed to write JSONL to GCS: %w", err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("failed to finalize GCS upload: %w", err)
	}

	log.Info().
		Str("gcs_uri", gcsURI).
		Int("request_count", len(requests)).
		Int("bytes", buf.Len()).
		Msg("Batch input JSONL uploaded to GCS")

	return gcsURI, nil
}

// readBatchOutputFromGCS reads the Vertex AI batch output JSONL files from a GCS prefix
// and parses each line into GeminiBatchResult.
func readBatchOutputFromGCS(ctx context.Context, outputGCSURI string) ([]GeminiBatchResult, error) {
	bucketName, prefix := gcsInputKey(outputGCSURI)

	gcsClient, err := newGCSClient(ctx)
	if err != nil {
		return nil, err
	}
	defer gcsClient.Close()

	bucket := gcsClient.Bucket(bucketName)

	// List all objects under the output prefix.
	var results []GeminiBatchResult
	idx := 0

	it := bucket.Objects(ctx, &storage.Query{Prefix: prefix})
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to list GCS output objects: %w", err)
		}
		if !strings.HasSuffix(attrs.Name, ".jsonl") {
			continue
		}

		rc, err := bucket.Object(attrs.Name).NewReader(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to read GCS output object %s: %w", attrs.Name, err)
		}

		// Use bufio.Reader.ReadBytes instead of bufio.Scanner so there is no
		// hard line-length limit. Scanner caps at its buffer max (even if set
		// to 4 MB) and returns "token too long" on large batch responses.
		// ReadBytes dynamically allocates — the 4 MB here is just the initial
		// read buffer, not a ceiling on line size.
		reader := bufio.NewReaderSize(rc, 4*1024*1024)
		for {
			line, readErr := reader.ReadBytes('\n')
			// Process whatever was read before inspecting the error.
			if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
				var row batchOutputRow
				if err := json.Unmarshal(trimmed, &row); err != nil {
					log.Warn().Err(err).Int("line_idx", idx).Msg("Failed to parse batch output line")
					results = append(results, GeminiBatchResult{Index: idx, Error: fmt.Sprintf("parse error: %v", err)})
				} else {
					result := GeminiBatchResult{Index: idx}
					if row.Status != "" {
						result.Error = row.Status
					} else if row.Response != nil {
						result.Response = row.Response
					}
					results = append(results, result)
				}
				idx++
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				rc.Close()
				return nil, fmt.Errorf("failed to read GCS output object %s: %w", attrs.Name, readErr)
			}
		}
		rc.Close()
	}

	log.Info().
		Str("output_prefix", outputGCSURI).
		Int("result_count", len(results)).
		Msg("Read batch output from GCS")

	return results, nil
}

// deleteGCSObject deletes a single GCS object by URI (non-fatal; logs on failure).
func deleteGCSObject(ctx context.Context, gcsURI string) {
	bucketName, objectName := gcsInputKey(gcsURI)
	if bucketName == "" || objectName == "" {
		return
	}
	gcsClient, err := newGCSClient(ctx)
	if err != nil {
		log.Warn().Err(err).Str("gcs_uri", gcsURI).Msg("Could not create GCS client for cleanup")
		return
	}
	defer gcsClient.Close()
	if err := gcsClient.Bucket(bucketName).Object(objectName).Delete(ctx); err != nil {
		log.Warn().Err(err).Str("gcs_uri", gcsURI).Msg("Failed to delete batch input JSONL from GCS")
	} else {
		log.Debug().Str("gcs_uri", gcsURI).Msg("Deleted batch input JSONL from GCS")
	}
}

// SubmitGeminiBatch submits a batch of GenerateContent requests.
//
// When GCS_BATCH_BUCKET is set (Vertex AI path): serializes requests to JSONL,
// uploads to GCS, and submits a Vertex AI batch job using GCSURI.
//
// When GCS_BATCH_BUCKET is not set (Gemini API path): uses InlinedRequests
// directly (only supported by BackendGeminiAPI).
func SubmitGeminiBatch(ctx context.Context, client *genai.Client, model string, requests []*genai.InlinedRequest) (string, error) {
	callStart := time.Now()
	log.Info().
		Str("model", model).
		Int("request_count", len(requests)).
		Msg("Submitting Gemini batch job")

	bucket := os.Getenv("GCS_BATCH_BUCKET")
	if bucket != "" {
		// Vertex AI path: upload JSONL to GCS, submit batch with GCSURI.
		inputURI, err := uploadBatchInputToGCS(ctx, bucket, requests)
		if err != nil {
			return "", fmt.Errorf("failed to upload batch input to GCS: %w", err)
		}

		outputPrefix := fmt.Sprintf("gs://%s/batch-output/", bucket)
		// Extract the UUID from the input URI to use as the job display name.
		// Vertex AI requires display_name to be set on batch prediction jobs.
		jobDisplayName := fmt.Sprintf("batch-%s", uuid.New().String())
		if parts := strings.Split(inputURI, "/"); len(parts) > 0 {
			last := parts[len(parts)-1]
			if name := strings.TrimSuffix(last, ".jsonl"); name != "" {
				jobDisplayName = "batch-" + name
			}
		}
		cfg := &genai.CreateBatchJobConfig{
			DisplayName: jobDisplayName,
			Dest: &genai.BatchJobDestination{
				Format: "jsonl",
				GCSURI: outputPrefix,
			},
		}
		// The genai SDK doesn't expose the serviceAccount field on
		// BatchPredictionJob, so we inject it via ExtraBody. This tells
		// Vertex AI to use our application SA (which has Storage Folder Admin)
		// instead of the default AI Platform Service Agent (DDR-084).
		if email := gcpServiceAccountEmail(); email != "" {
			cfg.HTTPOptions = &genai.HTTPOptions{
				ExtraBody: map[string]any{
					"serviceAccount": email,
				},
			}
			log.Debug().Str("service_account", email).Msg("Batch job will use custom service account")
		}
		job, err := client.Batches.Create(ctx, model, &genai.BatchJobSource{
			GCSURI: []string{inputURI},
			Format: "jsonl",
		}, cfg)
		if err != nil {
			// Cleanup the uploaded input on failure.
			deleteGCSObject(ctx, inputURI)
			return "", fmt.Errorf("failed to create batch job: %w", err)
		}

		log.Info().
			Str("job_name", job.Name).
			Str("state", string(job.State)).
			Str("input_uri", inputURI).
			Dur("duration", time.Since(callStart)).
			Msg("Gemini batch job submitted via GCS (Vertex AI)")

		// Encode both the job name and input URI in the returned ID so CheckGeminiBatch
		// can clean up the input file after reading results.
		// Format: "job_name|input_gcs_uri"
		return job.Name + "|" + inputURI, nil
	}

	// Gemini API path: use InlinedRequests directly.
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
		Msg("Gemini batch job submitted (inline)")

	return job.Name, nil
}

// CheckGeminiBatch polls the status of a Gemini batch job.
// Returns the current state and results (if the job has completed).
//
// jobID may be "job_name|input_gcs_uri" (Vertex AI GCS path) or plain "job_name"
// (Gemini API inline path).
func CheckGeminiBatch(ctx context.Context, client *genai.Client, jobID string) (*GeminiBatchStatus, error) {
	// Parse the composite job ID if present.
	jobName := jobID
	inputGCSURI := ""
	if idx := strings.Index(jobID, "|"); idx != -1 {
		jobName = jobID[:idx]
		inputGCSURI = jobID[idx+1:]
	}

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

	if job.State == genai.JobStateSucceeded {
		// GCS output path (Vertex AI).
		if job.Dest != nil && job.Dest.GCSURI != "" {
			results, err := readBatchOutputFromGCS(ctx, job.Dest.GCSURI)
			if err != nil {
				return nil, fmt.Errorf("failed to read batch output from GCS: %w", err)
			}
			status.Results = results

			// Cleanup the input JSONL now that results are read.
			if inputGCSURI != "" {
				deleteGCSObject(ctx, inputGCSURI)
			}

			log.Info().
				Str("job_name", jobName).
				Int("result_count", len(results)).
				Msg("Gemini batch job completed (GCS output)")
			return status, nil
		}

		// Inline response path (Gemini API).
		if job.Dest != nil && job.Dest.InlinedResponses != nil {
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
				Msg("Gemini batch job completed (inline responses)")
		}
	}

	return status, nil
}
