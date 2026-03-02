# DDR-085: Batch Economy Mode — Location Pre-Enrichment + Failure Propagation
**Date**: 2026-03-02
**Status**: Accepted
**Iteration**: Cloud — Economy mode Maps-accurate locations + resilient error handling

## Context

### Problem 1: GoogleMaps Tool Unsupported in Vertex AI Batch JSONL

The Vertex AI batch prediction API rejects JSONL input that contains tools with empty
structs (e.g., `googleMaps: {}`). DDR-078 introduced the GoogleMaps grounding tool to
give fb-prep location tags verified against Google's 250M+ places database. In economy
mode (batch), the tool was removed (DDR-084), silently downgrading location accuracy to
Gemini training-data-only inference from raw GPS coordinates.

### Problem 2: Batch Poll Failure Leaves Job Stuck PENDING

When the `GeminiBatchPollPipeline` Step Function sub-execution fails (e.g., JSONL import
error, quota exhaustion), the parent `FBPrepPipeline` transitions to FAILED but no code
path updates the DynamoDB job record from `status: "pending"` to `status: "error"`. The
frontend polls indefinitely and the UI shows "PENDING" forever with no actionable error.

## Decision

### 1. Pre-Enrichment Real-Time Call for Maps-Verified Locations

Before submitting the Vertex AI batch JSONL, run one fast real-time Gemini call that:
- Sends GPS coordinates as plain text (no images) for all items that have GPS data
- Enables the GoogleMaps tool (supported in real-time, not in batch)
- Parses the returned JSON array of `{index, location_tag}` objects
- Injects the Maps-verified place names into the batch job's metadata context as a
  supplementary `## Maps-verified locations` section

The batch model receives the answer pre-computed in its context and uses it for the
`location_tag` field without needing a live Maps lookup itself. This preserves
full Maps accuracy in economy mode at the cost of one small, fast real-time API call
(text-only, ~1–2 seconds).

**Silent fallback**: if the pre-enrichment call fails for any reason, the batch job
proceeds with GPS-only metadata — identical to the previous behavior. The pre-enrichment
failure is logged and metricked but never blocks the batch submission.

### 2. Location Tag Comparison Metrics

To evaluate whether the pre-enrichment call is worth keeping, metrics are emitted in
`handleCollectBatch` comparing the pre-enrichment location tags against the batch model's
own location tags:

- `LocationTagMatchCount` / `LocationTagMismatchCount` — per-job count of items where
  pre-enrichment and batch agree/disagree on the location_tag
- `LocationTagAgreementRate` — percentage of agreement (0–100), emitted per job
- Pre-enrichment locations are stored in DynamoDB as `preEnrichLocations` on the
  `FBPrepJob` record so they are available to `handleCollectBatch`

Mismatches are individually logged at INFO level with both location values for manual
inspection via CloudWatch Logs Insights.

### 3. Batch Failure Propagation via SFN Catch Handler

Add a `.addCatch()` on the `StartGeminiBatchPoll` task in the `FBPrepPipeline` CDK
definition. On any failure of the `GeminiBatchPollPipeline` sub-execution, the catch
handler:
1. Invokes the fb-prep lambda with `type: "fb-prep-mark-error"` to write
   `status: "error"` to the DynamoDB job record
2. Transitions the `FBPrepPipeline` to a `Fail` state

This ensures the frontend immediately shows an error message and the "Try Again" button
instead of being permanently stuck on "PENDING".

## Files Changed

### Backend (`ai-social-media-helper`)
- `cmd/fb-prep-lambda/handler.go` — `resolveLocationTags()`, `handleMarkError()`,
  updated `buildFBPrepPrompt()`, economy mode path pre-enrichment + `PreEnrichLocations`
  storage, `handleCollectBatch` comparison metrics
- `internal/store/fb_prep.go` — `FBPrepJob.PreEnrichLocations map[string]string`

### CDK (`ai-social-media-helper-deploy`)
- `cdk/lib/constructs/step-functions-pipelines.ts` — `MarkBatchError` catch on
  `StartGeminiBatchPoll`

### Docs
- `docs/facebook-prep.md` — economy mode flow, pre-enrichment step, comparison metrics
- `docs/architecture.md` — updated FB Prep batch flow diagram
- `docs/operations.md` — new CloudWatch metrics

## Consequences

- **Location accuracy in economy mode**: restored to Maps-verified quality (same as
  real-time mode) with a ~1–2s latency overhead before batch submission
- **Evaluation**: `LocationTagAgreementRate` metric will show whether the batch model's
  own GPS inference agrees with pre-enrichment; if agreement is consistently high (>95%),
  the pre-enrichment call can be removed in a future DDR to reduce cost
- **Error visibility**: batch poll failures now surface immediately in the UI as errors
  instead of infinite PENDING state
- **DynamoDB schema**: `FBPrepJob` gains `preEnrichLocations` field; existing records
  without this field are handled gracefully (nil map = no comparison metrics emitted)

## Rejected Alternatives

- **Reverse geocoding via Maps API directly**: Would require enabling the Maps Platform
  Geocoding API and managing a separate API key; the Gemini GoogleMaps tool already
  handles this internally with no additional credentials
- **Accepting GPS-only inference**: Tested in practice — Gemini's training-data knowledge
  of coordinates is adequate for city/region level but imprecise for specific venues
  (e.g., returns "Seattle, WA" instead of "Pike Place Market, Seattle, WA")
- **Post-processing locations via second batch job**: Doubles the batch queue time;
  the pre-enrichment real-time call is faster and simpler
