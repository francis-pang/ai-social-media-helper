# DDR-059: Frugal Triage — Early S3 Cleanup via Thumbnails

**Date**: 2026-02-14
**Status**: Accepted
**Iteration**: 1

## Context

The triage pipeline uploads user media (photos and videos) to S3, where files persist for 24 hours via the bucket's lifecycle policy (DDR-035). A typical session contains 36 files totaling ~500 MB, but the user interaction from upload to confirm takes only ~12 minutes. This means originals sit in S3 for 24–48 hours (lifecycle evaluation runs once daily at ~midnight UTC) despite being needed only during the triage-run step.

Key observations from production logs:

- Triage pipeline execution: **5s – 4 min** (Step Functions)
- Triage-run Lambda duration: **4s – 143s** (varies by file count)
- Full user interaction (start to confirm): **~12 minutes**
- S3 Standard has **no minimum storage duration** charge — deletion at any time incurs no penalty
- The triage-run step already generates in-memory thumbnails for Gemini; these are discarded after the API call

After triage-run completes, the originals are never accessed again — the review UI only needs small thumbnails (~30 KB each) and the video thumbnail handler already returns a placeholder SVG based on file extension without needing the original file.

## Decision

Reduce S3 storage costs by generating and persisting thumbnails during triage-run, then immediately deleting original files. After the user confirms triage results, clean up all remaining session artifacts (thumbnails, compressed videos).

### 1. Generate and store thumbnails during triage-run, then delete originals

In `handleTriageRun` (triage-lambda), after `AskMediaTriage` returns successfully and results are written to DynamoDB:

- Generate image thumbnails using `filehandler.GenerateThumbnail` from the temp files already on disk
- Upload each thumbnail to S3 at `{sessionId}/thumbnails/{baseName}.webp`
- Set `ThumbnailURL` for image results to `/api/media/thumbnail?key={sessionId}/thumbnails/{baseName}.webp` (the existing `handleThumbnail` in media-lambda already serves pre-generated thumbnails at this prefix — DDR-030)
- For video results, keep existing `ThumbnailURL` as-is — the handler returns a placeholder SVG based on file extension, without needing the original file in S3
- Delete all original files from S3 under `{sessionId}/` (excluding `thumbnails/` and `compressed/` prefixes)
- This is best-effort; the 1-day lifecycle remains as safety net

### 2. Clean up entire session after triage confirm

In `handleTriageConfirm` (media-lambda), after processing delete requests:

- Call `cleanupS3Prefix(sessionID, "")` to delete ALL remaining S3 objects under `{sessionId}/` — this removes thumbnails, compressed videos, and any stragglers
- Runs best-effort in a goroutine (same pattern as session invalidation in DDR-037)

### 3. No infrastructure changes needed

- S3 Standard has no minimum storage duration charge
- The 1-day lifecycle stays as a safety net for abandoned sessions
- DynamoDB TTL stays at 24h (aligned with S3 lifecycle)

## Rationale

1. **Originals are never accessed after triage-run** — the review UI uses thumbnails, and video playback uses compressed versions or placeholder SVGs
2. **Thumbnails are already generated in-memory** — `AskMediaTriage` creates thumbnails for the Gemini API call; we just need to persist them to S3 instead of discarding them
3. **The pre-generated thumbnail path already works** — `handleThumbnail` in media-lambda detects keys under `/thumbnails/` and serves them directly from S3 without regeneration (DDR-030)
4. **Active deletion is the only way to achieve sub-day cleanup** — lifecycle evaluation runs once daily, so `Days: 1` means 24–48h actual retention
5. **Best-effort pattern is proven** — session invalidation already uses fire-and-forget goroutines for S3 cleanup (DDR-037)

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Reduce lifecycle to shorter duration | S3 lifecycle minimum is 1 day; evaluation runs once daily so actual deletion is 24–48h |
| S3 Intelligent-Tiering | Minimum 30-day storage; costs more than Standard for short-lived objects |
| Move to S3 One Zone-IA | 30-day minimum storage duration charge; not suitable for ephemeral data |
| Delete only after confirm (no thumbnails) | Originals would persist during the ~12 min review period unnecessarily |
| Generate thumbnails in triage-prepare | Would require an extra S3 round-trip; triage-run already has files on disk |

## Consequences

**Positive:**
- For a typical 36-file session (~500 MB originals), S3 storage-hours drop from ~12,000 MB-hours to ~26 MB-hours (thumbnails only, for ~12 min review)
- No infrastructure or CDK changes required
- No user-visible behavior change — thumbnails look identical to the on-the-fly generated ones
- Lifecycle policy remains as safety net for abandoned sessions

**Trade-offs:**
- Triage-run Lambda duration increases slightly (thumbnail generation + S3 uploads + S3 deletes)
- If triage-run fails mid-cleanup, some originals may be deleted without thumbnails — mitigated by running cleanup only after all thumbnails are uploaded
- Full-resolution image preview (`handleFullImage`) will return 404 for triage sessions after cleanup — acceptable since triage UI only shows thumbnails

## Related Documents

- [DDR-030: Cloud Selection Backend Architecture](./DDR-030-cloud-selection-backend.md) — pre-generated thumbnail serving
- [DDR-035: Multi-Lambda Deployment Architecture](./DDR-035-multi-lambda-deployment.md) — S3 lifecycle policy
- [DDR-037: Step Navigation and State Invalidation](./DDR-037-step-navigation-and-state-invalidation.md) — goroutine cleanup pattern
- [DDR-050: Replace Goroutines with Async Dispatch](./DDR-050-replace-goroutines-with-async-dispatch.md) — DynamoDB job state
- [DDR-052: Step Functions Polling](./DDR-052-step-functions-polling-for-long-running-ops.md) — triage pipeline architecture
