# DDR-081 — FB Prep Processing Bug Fixes

**Date:** 2026-03-01  
**Author:** AI Agent  
**Status:** Implemented

## Context

Three bugs were discovered in the FB Prep processing flow via CloudWatch investigation of job `ad427d97-9a81-41d8-9605-2be784549855`:

1. Jobs appear stuck in "PROCESSING" indefinitely — the timer runs past 16 minutes with no resolution
2. "Token Budget" in the UI shows a hardcoded 60% bar
3. "S3 Bandwidth" always shows "Calculating..."

## Root Causes

### 1. Lambda /tmp Exhaustion (critical)

`buildFBPrepMediaParts()` uses `defer cleanup()` inside a `for` loop at five points (thumbnail download, original fallback, video download, video thumbnail fallback, compressed video). Because Go defers execute at function return — not at loop iteration end — all 68 downloaded temp files accumulate in `/tmp` simultaneously.

With `/tmp` at 1024 MB and 68 media items averaging ~15–50 MB each (after thumbnailing), the partition fills partway through. Subsequent downloads fail with `"no space left on device"`. The Lambda eventually calls `SubmitGeminiBatch` with an empty or partial `parts` slice, which likely returns an error. The handler returns error → AWS async invocation retries twice more (same RequestId confirmed in CloudWatch at 15:36, 15:38, 15:40). After 3 total attempts, the job remains stuck at `status: "processing"` in DynamoDB.

**Fix:** Replace `defer cleanup()` with immediate `cleanup()` calls after `os.ReadFile()` in each branch.

### 2. Economy Mode Has No Batch Completion Mechanism

In economy mode, the Lambda submits to Gemini Batch API, writes `batchJobId` + `status: "pending"` to DynamoDB, and returns. Other workers (triage, description, selection) return the `batchJobId` to a Step Functions state machine, which uses the `gemini-batch-poll` Lambda to wait for completion. FB Prep was invoked directly (no SFN) and has no equivalent polling mechanism. The Gemini batch job completes (after ~10 min) but nothing reads the result or updates DynamoDB.

**Fix:** Remove the economy mode code path in the FB Prep Lambda — always use synchronous `GenerateContent`. Real-time mode completes in 1–4 minutes for typical batch sizes. The global economy mode toggle continues to function for other features.

### 3. DynamoDB Never Reaches Error State

When the Lambda handler returns an error (after /tmp exhaustion), the API handler's initial `status: "processing"` record in DynamoDB is never updated. After all retries are exhausted, the job stays `processing` forever.

**Fix:** Add a deferred error recorder at the start of `handler()` using named return values. On any non-nil error return, it writes `status: "error"` to DynamoDB before the function exits.

### 4. Missing `createdAt` in Results Response

The `GET /api/fb-prep/{id}/results` handler returns only `id`, `status`, `items`, and `error`. The frontend timer starts from component mount, which resets on navigation and is inaccurate for long jobs.

**Fix:** Return `createdAt` in the results response so the frontend can compute accurate elapsed time from the server-side job start.

### 5. Fake Resource Usage UI

`ProcessingIndicator.tsx` hardcodes "Estimating...", "60%", and "Calculating..." for Gemini Tokens, Token Budget, and S3 Bandwidth respectively. No real data was ever wired.

**Fix:** Remove the Resource Usage sidebar panel entirely. It adds no value when showing static placeholder data.

### 6. Frontend Polling Silent Timeout

`pollResults()` in `FBPrepView.tsx` creates a poller with `timeoutMs: 60000` but has no `.catch()` on the returned promise. When the poller times out after 60 seconds, the rejection is silently swallowed — `error.value` is never set, and the UI freezes in "PROCESSING" indefinitely.

**Fix:** Add a `.catch()` handler that sets `error.value`. Increase `timeoutMs` to 12 minutes (720000 ms) to cover typical real-time processing time for large batches.

## Decision

Apply all six fixes as described. No new infrastructure required.

## Trade-offs

- Removing economy mode from FB Prep means slightly higher Gemini API cost per job vs. batch pricing. For typical FB Prep usage (20–80 items, infrequent), the difference is negligible.
- The deferred error recorder in the Lambda writes to DynamoDB on every error return, including transient failures. This is acceptable — the API handler can always overwrite `status` on retry.

## Affected Files

- `cmd/fb-prep-lambda/handler.go` — defer fix, remove economy mode, deferred error recorder
- `cmd/api/fb_prep.go` — return `createdAt` in results
- `web/src/types/api.ts` — add `createdAt` to `FBPrepJob`
- `web/src/components/FBPrepView.tsx` — polling catch, timeout, pass `createdAt`
- `web/src/hooks/useElapsedTimer.ts` — accept optional `startedAtMs`
- `web/src/components/ProcessingIndicator.tsx` — use `startedAt`, remove fake Resource Usage
