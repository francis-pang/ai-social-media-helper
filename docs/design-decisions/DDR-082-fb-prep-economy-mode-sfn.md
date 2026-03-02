# DDR-082 — FB Prep Economy Mode via Step Functions Pipeline

**Date:** 2026-03-02  
**Author:** AI Agent  
**Status:** Implemented  
**Supersedes:** DDR-081 §Bug 2 (economy mode removed)

## Context

DDR-081 removed FB Prep's economy mode because the Gemini Batch submission had no completion mechanism — the Lambda wrote `batchJobId` to DynamoDB and returned, but nothing polled the batch to completion. As a result, jobs remained pending forever.

This DDR re-implements economy mode correctly using a Step Functions state machine that:
1. Invokes the FB Prep Lambda (which downloads media, builds Gemini parts, and submits the Gemini Batch job)
2. Waits for the batch to complete using the existing `GeminiBatchPollPipeline` SFN
3. Invokes the FB Prep Lambda again to collect batch results and write the completed job to DynamoDB

## Architecture

```
API Lambda (handleFBPrepStart)
  │
  └─ StartExecution → FBPrepPipeline SFN
                         │
                         ├─ RunFBPrep (LambdaInvoke: fb-prep Lambda)
                         │    Returns: {session_id, status, batch_job_id?}
                         │
                         └─ FBPrepIsBatch (Choice)
                              │
                              ├─ batch_job_id present (economy mode):
                              │    StartGeminiBatchPoll (StepFunctionsStartExecution.sync)
                              │      → GeminiBatchPollPipeline (wait 15s → poll → loop)
                              │    CollectBatchResults (LambdaInvoke: fb-prep-collect-batch)
                              │    Succeed
                              │
                              └─ batch_job_id absent (real-time):
                                   Succeed (Lambda already wrote complete status)
```

## Decision

### FBPrepPipeline SFN (AiSocialMediaFBPrepPipeline)
- Defined in `step-functions-pipelines.ts`
- 90-minute timeout (Gemini Batch can take up to 60 min)
- API Lambda uses `StartExecution` (non-blocking, fire-and-forget)

### `fb-prep-collect-batch` event handler
- New event type dispatched by `CollectBatchResults` step
- Calls `ai.CheckGeminiBatch` to retrieve the completed batch results
- Parses Gemini response using `parseFBPrepResponse`
- Writes `status: "complete"` + items + token usage to DynamoDB

### Token Usage Tracking
- Both real-time and batch paths capture `resp.UsageMetadata.PromptTokenCount` and `CandidatesTokenCount`
- Stored as `InputTokens` / `OutputTokens` in `FBPrepJob` (DynamoDB)
- Returned in the results API response
- Displayed in the frontend Resource Usage panel

### Resource Usage Panel
- Removed fake hardcoded values from `ProcessingIndicator.tsx`
- Shows real token counts when available, "Estimating..." while processing
- Token Budget bar: `(inputTokens + outputTokens) / (totalCount × 8000)` — 8000 tokens is the empirical per-item average
- Items Processed: `completedCount / totalCount`
- S3 Bandwidth removed — no real-time data source during Lambda processing

## Affected Files

**App repo (`ai-social-media-helper`):**
- `cmd/fb-prep-lambda/handler.go` — economy mode restored, collect-batch handler, token capture
- `cmd/api/fb_prep.go` — SFN dispatch, token usage in results response
- `cmd/api/globals.go` — `fbPrepSfnArn`
- `cmd/api/main.go` — `FB_PREP_SFN_ARN` env var
- `internal/store/fb_prep.go` — `InputTokens`, `OutputTokens` fields
- `web/src/types/api.ts` — `FBPrepJob.inputTokens`, `outputTokens`
- `web/src/components/ProcessingIndicator.tsx` — Resource Usage panel
- `web/src/components/FBPrepView.tsx` — pass token props

**Deploy repo (`ai-social-media-helper-deploy`):**
- `cdk/lib/constructs/step-functions-pipelines.ts` — FBPrepPipeline definition
- `cdk/lib/backend-stack.ts` — wiring, IAM, env vars
