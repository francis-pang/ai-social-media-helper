# DDR-061: S3 Event-Driven Per-File Processing

**Date**: 2026-02-15
**Status**: Accepted
**Iteration**: 1

## Context

Currently, the triage pipeline works in 3 steps: triage-prepare (list S3, count files) → triage-check-gemini (poll Gemini Files API for video processing) → triage-run (download all files, extract metadata, generate thumbnails, call Gemini for AI triage, write results to DDB).

All processing happens in the triage-run step, which is a monolithic Lambda invocation that downloads everything, processes everything, and calls Gemini. Users see no per-file feedback during upload — they only see "uploading" then "processing" phases. With 36+ files, the triage-run step takes 2–3 minutes because it serially downloads, processes, and generates thumbnails.

## Decision

### 1. Add MediaProcess Lambda (S3 event-driven)

Add a new **MediaProcess** Lambda (1 GB, Heavy container with ffmpeg, 5 min timeout) triggered by S3 ObjectCreated events. Each uploaded file triggers its own MediaProcess Lambda invocation that:

- Validates the file
- Converts if needed (resize large photos, compress videos)
- Generates thumbnail
- Writes result to a new dedicated DynamoDB table (`media-file-processing`)

### 2. Redesign Step Functions pipeline

Change from **Prepare → CheckGemini → Run** to **InitSession → Poll → TriageRun**:

- **InitSession**: Creates the session and starts the Step Function
- **Poll**: Polls DDB every 3 seconds waiting for `processedCount == expectedFileCount`
- **TriageRun**: Reads the pre-processed file manifest from DDB, generates presigned URLs, calls Gemini, and writes results

### 3. Browser integration

- Browser calls `POST /api/triage/init` on first file drop (before uploads complete), which creates the session and starts the Step Function
- Frontend shows per-file processing status during upload by polling the existing results endpoint

### 4. New DynamoDB table

New dedicated `media-file-processing` DynamoDB table for per-file results — separate from sessions table for isolation and scalability.

### Key design decisions

| Decision | Choice | Reason |
|----------|--------|--------|
| S3 event trigger vs browser signaling API | S3 events | Simplicity and reliability |
| Combined MediaProcess Lambda (validate+convert+thumbnail) vs separate Lambdas | Single Lambda | Reduce invocation overhead and simplify |
| Separate DDB table for file processing vs same table | Separate table (Option B) | Data isolation, zero write contention, independent scaling, shorter TTL |
| Polling pattern in SFN vs Task Token callback | Polling | Simplicity — MediaProcess Lambda doesn't know about the SFN |

## Rationale

- **Per-file feedback**: Users see validation and processing status as each file uploads
- **Parallel processing**: Files are processed as soon as they land in S3, not after all uploads complete
- **Simplified triage-run**: No /tmp downloads; only reads manifest, generates presigned URLs, calls Gemini
- **Error isolation**: One bad file doesn't fail the entire batch
- **Scale headroom**: Architecture supports 500+ files

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Browser signaling API instead of S3 events | More complex; requires coordination between upload completion and backend; S3 events are simpler and reliable |
| Separate Lambdas for validate, convert, thumbnail | Higher invocation overhead; more state to pass between steps |
| Same DynamoDB table as sessions | Write contention; mixed TTL requirements; harder to scale independently |
| Task Token callback from MediaProcess to SFN | MediaProcess doesn't know about the SFN; would require passing execution token through S3 event payload |

## Consequences

**Positive:**

- Per-file validation feedback during upload
- Faster end-to-end triage (processing starts as soon as first file uploads)
- Simplified triage-run Lambda (Gemini-only, no /tmp downloads)
- Better error isolation (one bad file doesn't fail the entire batch)
- Scale headroom for 500+ files

**Trade-offs:**

- One more DynamoDB table to manage (CDK, IAM, TTL, monitoring)
- One more Lambda function to build, deploy, and monitor
- More Step Functions transitions (polling adds ~$0.0003 per job)
- S3 event notifications require careful key filtering to avoid recursive triggers

**Cost impact:** Approximately cost-neutral ($0.0027 vs $0.0028 per job)

**Latency impact:** Wall-clock time from first upload to Gemini triage drops from ~2–3 minutes to ~1–1.5 minutes

## Related Documents

- [DDR-050: Replace Goroutines with Async Dispatch](./DDR-050-replace-goroutines-with-async-dispatch.md) — async dispatch and triage job pattern
- [DDR-052: Step Functions Polling](./DDR-052-step-functions-polling-for-long-running-ops.md) — Step Functions architecture
- [DDR-053: Granular Lambda Split](./DDR-053-granular-lambda-split.md) — multi-Lambda architecture
- [DDR-059: Frugal Triage S3 Cleanup](./DDR-059-frugal-triage-s3-cleanup.md) — S3 cleanup
- [DDR-060: S3 Presigned URLs for Gemini](./DDR-060-s3-presigned-urls-for-gemini.md) — presigned URLs
