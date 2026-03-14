# FB Prep Job Stuck in "Pending" — Investigation Guide

**Job ID:** `fb-c41bf1821111930c5fbf29665e91e439`  
**Session ID:** `7697250f-7572-42bb-9862-203d4f429d67` (UI may show this as "Job ID")

---

## Root Cause (Investigation 2026-03-14)

**The FBPrepPipeline SFN skips Poll and Collect when RunFBPrep returns `batch_job_id` but not `batches_meta`.**

### What Happened

1. **RunFBPrep** (economy mode, 5 items: 3 videos + 2 photos) submitted a batch inline and returned:
   ```json
   {"session_id":"7697250f-...","status":"pending","batch_job_id":"projects/530730562944/locations/global/batchPredictionJobs/748163786631806976|gs://..."}
   ```
2. RunFBPrep wrote `status: "pending"` to DynamoDB.
3. **FBPrepIsBatch** Choice checks only `$.prep_result.Payload.batches_meta`.
4. RunFBPrep returned `batch_job_id` but **not** `batches_meta` (inline batch path, no GCS upload).
5. Choice took **otherwise** → `fbPrepSucceed` → SFN succeeded immediately.
6. **Poll and Collect never ran.** The job remains "pending" forever.

### Pipeline Logic Gap (Fixed 2026-03-14)

**Root cause:** Deployed RunFBPrep Lambda was old (inline submit, returns `batch_job_id`) while the SFN was updated for the GCS flow (checks `batches_meta` only). The `batch_job_id` branch had been removed, so the SFN took `otherwise` → Succeed and never ran Poll/Collect.

| RunFBPrep returns | SFN path | Result |
|-------------------|----------|--------|
| `batches_meta` (videos need GCS) | MapUpload → Submit → Poll → Collect | ✓ Complete |
| `batch_job_id` or `batch_job_ids` (old Lambda) | SetOldLambdaError → MarkBatchError → Fail | ✓ Error with clear message |
| neither (real-time) | otherwise → Succeed | ✓ Complete |

**Fix:** Added a branch for `batch_job_id` or `batch_job_ids` present without `batches_meta`. The SFN now fails explicitly with a clear error: "RunFBPrep returned batch_job_id (inline submit). Deploy the new fb-prep Lambda that returns batches_meta + videos_to_upload for GCS-based economy mode." The job is marked `status: "error"` in DynamoDB so the user sees an actionable error instead of stuck "pending".

**Long-term:** Deploy the new fb-prep Lambda (from `cmd/lambda/fb-prep-lambda/`) so economy mode uses the GCS flow: RunFBPrep returns `batches_meta` + `videos_to_upload` → MapUploadVideos → Submit → Poll → Collect.

### Evidence

- SFN execution `fb-c41bf1821111930c5fbf29665e91e439`: **SUCCEEDED** in ~9s (15:35:48 → 15:35:57)
- RunFBPrep output: `batch_job_id` present, `batches_meta` absent
- Flow: RunFBPrep → FBPrepIsBatch → FBPrepSucceed (no MapUpload, Submit, Poll, or Collect)
- Vertex AI batch job: `748163786631806976` — may have completed, but Collect never ran

---

## What "Pending" Means

In economy mode, the FB Prep pipeline flow is:

1. **API** → writes `status: "processing"`, starts FBPrepPipeline SFN
2. **RunFBPrep** → downloads thumbnails, builds context, returns `batches_meta` + `videos_to_upload`
3. **MapUploadVideos** → uploads each video to GCS
4. **RunFBPrepSubmit** → submits to Gemini Batch API, **writes `status: "pending"` to DynamoDB**
5. **Poll** (GeminiBatchPollPipeline) → waits 15s, polls, loops until `JOB_STATE_SUCCEEDED` or `JOB_STATE_FAILED`
6. **CollectBatchResults** → retrieves results, writes `status: "complete"` to DynamoDB

When you see `Phase: pending`, the Submit step has completed and the pipeline is waiting for the **Gemini Batch job** to finish. The frontend polls DynamoDB and shows whatever status is there.

## Likely Causes

### 1. Gemini Batch Still Processing (Most Common)

- **Gemini batch jobs typically take 10–60 minutes** to complete.
- The poll SFN waits 15s → polls → if not done, loops. This is expected.
- **Action:** Wait longer. If it’s been &lt; 30 minutes, this is normal.

### 2. GeminiBatchPollPipeline Timeout (30 min)

- The **GeminiBatchPollPipeline** has a **30-minute timeout**.
- Gemini batch can take up to **60 minutes**.
- If the batch runs longer than 30 minutes, the poll SFN times out. The catch handler runs `MarkBatchError`, which writes `status: "error"` to DynamoDB.
- **If you see "pending" (not "error")**, the poll has not failed yet, so this is unlikely to be the current cause.

### 3. Poll SFN Failed (API / GCP Error)

- If the poll Lambda fails (e.g. GCP auth, network, rate limit), the catch runs and marks the job as `error`.
- **If you see "pending"**, the poll SFN is either still running or hasn’t reached the poll step.

### 4. SFN Stuck Before Poll

- **MapUploadVideos** or **RunFBPrepSubmit** could be stuck or failed.
- If Submit never ran, the job would stay `processing`, not `pending`.
- Since you see `pending`, Submit completed. The next step is Poll.

### 5. Wrong Job ID in SFN

- The collect step uses `jobId` from `$.prep_result.Payload.job_id`. If RunFBPrep doesn’t return `job_id`, Collect would fail.
- That would trigger the catch and set `error`, not leave `pending`.

## Investigation Steps

### Step 1: Check FBPrepPipeline Execution

```bash
# Replace ACCOUNT_ID (681565534940) and ensure JOB_ID is the actual job ID (e.g. fb-xxx)
aws stepfunctions describe-execution \
  --execution-arn "arn:aws:states:us-east-1:681565534940:execution:AiSocialMediaFBPrepPipeline:7697250f-7572-42bb-203d4f429d67"
```

- **RUNNING** → pipeline is still in progress (likely Poll).
- **SUCCEEDED** → pipeline finished; if UI still shows pending, check DynamoDB and API.
- **FAILED** / **TIMED_OUT** → inspect `cause` and `error`; check for Poll timeout or Collect failure.

### Step 2: Get Execution History

```bash
aws stepfunctions get-execution-history \
  --execution-arn "arn:aws:states:us-east-1:681565534940:execution:AiSocialMediaFBPrepPipeline:7697250f-7572-42bb-203d4f429d67" \
  --max-results 100
```

Look for:

- Last successful state (e.g. `RunFBPrepSubmit`, `PollFromSubmit`, `MapPollsFromSubmit`).
- Any `Failed` or `TimedOut` events.
- Sub-executions of `AiSocialMediaGeminiBatchPollPipeline`.

### Step 3: Check Submit Batch Logs for `batch_job_id`

```bash
aws logs filter-log-events \
  --log-group-name "/aws/lambda/AiSocialMediaBackend-FBPrepSubmitBatchProcessor4DB-rMqH39n0oQ6i" \
  --filter-pattern "7697250f-7572-42bb-203d4f429d67" \
  --start-time $(($(date +%s) - 86400))000
```

- Find the `batch_job_id` (or `batch_job_ids`) in the log.
- Use it in GCP to check the Vertex AI batch job status.

### Step 4: Check Gemini Batch Poll Logs

```bash
aws logs filter-log-events \
  --log-group-name "/aws/lambda/AiSocialMediaBackend-GeminiBatchPollProcessor4EF19-YL3iGbkHpPPq" \
  --filter-pattern "7697250f" \
  --start-time $(($(date +%s) - 86400))000
```

- See how often it’s polling and what `state` it returns.
- `JOB_STATE_SUCCEEDED` → Collect should run next.
- `JOB_STATE_PENDING` / `JOB_STATE_RUNNING` → still processing.

### Step 5: Verify Job ID Format

- The UI may show **session ID** where you expect job ID.
- FB Prep job IDs are usually `fb-`-prefixed.
- If you only have a UUID, search logs by session ID to find the real job ID (see `docs/operations.md`).

## Quick Checks

| Observation | Interpretation |
|-------------|----------------|
| Elapsed &lt; 30 min, status `pending` | Normal; batch likely still running |
| Elapsed &gt; 30 min, status `pending` | Poll may have timed out; check SFN execution status |
| Status `error` | Poll or Collect failed; check SFN history and MarkBatchError logs |
| SFN RUNNING, last state = Poll | Batch still processing; wait or check GCP batch job |
| SFN FAILED, cause mentions timeout | Consider increasing GeminiBatchPollPipeline timeout |

## Potential Fix: Increase Poll Timeout

If jobs often exceed 30 minutes, increase the GeminiBatchPollPipeline timeout to match the 60-minute batch limit:

**File:** `ai-social-media-helper-deploy/cdk/lib/constructs/step-functions-pipelines.ts`

```typescript
// Line ~329: Change from 30 to 75 minutes
this.geminiBatchPollPipeline = new sfn.StateMachine(this, 'GeminiBatchPollPipeline', {
  // ...
  timeout: cdk.Duration.minutes(75),  // was 30; batch can take up to 60 min
});
```

The parent FBPrepPipeline already has a 90-minute timeout, so this stays within that limit.
