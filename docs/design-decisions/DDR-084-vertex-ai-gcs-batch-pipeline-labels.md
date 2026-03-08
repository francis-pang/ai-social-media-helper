# DDR-084: Vertex AI Batch via GCS + ProcessingIndicator Label Fix

**Date**: 2026-03-02  
**Status**: Accepted  
**Iteration**: Cloud — Vertex AI batch job fix + UI pipeline label accuracy

## Context

### Problem 1: Economy Mode Fails on Vertex AI Backend

All 4 workflows (FB Prep, Triage, Description, Selection) use `ai.SubmitGeminiBatch()` for economy mode. This function passes `BatchJobSource.InlinedRequests` to the genai SDK. The genai SDK v1.48.0 explicitly rejects this for the Vertex AI backend:

```
InlinedRequests is not supported for Vertex AI backend.
```

Vertex AI's batch API requires input as a JSONL file in Google Cloud Storage (GCS), not inline in the API request. The `NewAIClient()` function prefers `BackendVertexAI` when `VERTEX_AI_PROJECT` is set, so all batch submissions fail in production.

### Problem 2: ProcessingIndicator Shows Wrong Pipeline Stages

The `ProcessingIndicator` component shows 3 hardcoded stages:
- "Upload to Gemini" / "Video Processing" / "AI Evaluation"

"Video Processing" implies a separate media processing step that doesn't exist. All 4 workflows do the same thing:
1. Prepare and send media to Gemini (from pre-processed S3 files)
2. Gemini runs AI analysis
3. Parse and store results

The labels should reflect the actual pipeline.

## Decision

### 1. GCS-Based Batch Input/Output for Vertex AI

Add GCS upload/download to `SubmitGeminiBatch` and `CheckGeminiBatch` in `internal/ai/batch.go`. When `GCS_BATCH_BUCKET` env var is set, the functions automatically use GCS instead of inline requests:

- **Submit**: Serialize `[]*genai.InlinedRequest` → JSONL → upload to `gs://BUCKET/batch-input/{uuid}.jsonl` → call `Batches.Create` with `BatchJobSource.GCSURI`
- **Check**: When job completes, list and read output JSONL from `gs://BUCKET/batch-output/{job-id}/` → parse each line into `GeminiBatchResult` → delete input JSONL
- **Fallback**: When `GCS_BATCH_BUCKET` is not set, use the existing `InlinedRequests` path (Gemini API backend only)

All 4 callers (`fb-prep-lambda`, `triage`, `description`, `selection_media`) are unchanged — the GCS logic is fully encapsulated in `batch.go`.

### 2. GCS Bucket

Bucket `social-media-ai-app-bucket` in project `gen-lang-client-0436578028` (pre-existing bucket).

IAM: `Storage Folder Admin` already assigned to `aws-app@gen-lang-client-0436578028.iam.gserviceaccount.com` at project level. The batch job explicitly passes this SA via `CreateBatchJobConfig.HTTPOptions.ExtraBody{"serviceAccount": ...}` so Vertex AI uses it for GCS writes instead of the default AI Platform Service Agent (which has no bucket-level IAM).

### 3. New Dependency: `cloud.google.com/go/storage`

Added as a direct dependency. Already an indirect transitive dependency via `google.golang.org/genai`.

### 4. ProcessingIndicator Stage Labels

Updated `STAGES` constant in `ProcessingIndicator.tsx`:

| Before | After |
|--------|-------|
| Upload to Gemini ☁️ | Upload to Gemini ☁️ (unchanged) |
| Video Processing 🎬 | AI Analysis 🤖 |
| AI Evaluation 🤖 | Generating Results 📋 |

Updated `deriveStage()` to match new semantics. Applied universally to all workflows.

## Files Changed

### Backend
- `internal/ai/batch.go` — GCS helpers (`newGCSClient`, `uploadBatchInputToGCS`, `readBatchOutputFromGCS`), updated `SubmitGeminiBatch`, updated `CheckGeminiBatch`
- `go.mod` / `go.sum` — `cloud.google.com/go/storage` added as direct dependency

### CDK
- `cdk/lib/constructs/processing-lambdas.ts` — `GCS_BATCH_BUCKET` added to `sharedEnv`

### Frontend
- `web/src/components/ProcessingIndicator.tsx` — updated `STAGES` and `deriveStage`

## JSONL Format

Input (one line per `InlinedRequest`):
```json
{"request":{"contents":[{"role":"user","parts":[{"text":"..."}]}],"generationConfig":{"temperature":1}}}
```

Output (one line per response, Vertex AI format):
```json
{"status":"","request":{...},"response":{"candidates":[{"content":{"parts":[{"text":"..."}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{...}}}
```

## Consequences

- Economy mode works on Vertex AI (`$300` GCP credits pool)
- GCS cost: negligible — files are KBs in size, stored for minutes
- All 4 batch-capable workflows fixed with a single `batch.go` change
- ProcessingIndicator labels accurately describe the pipeline for all workflows
- `GCS_BATCH_BUCKET` not set → falls back to `InlinedRequests` (Gemini API only)

## Rejected Alternatives

- **Gemini API fallback for batch**: Uses the Gemini API free tier (1,000–1,500 RPD limit, uncertain long-term) instead of the $300 GCP credits pool
- **Thread separate client through all callers**: Requires changes to 4 callers + their function signatures
