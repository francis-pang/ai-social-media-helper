# DDR-067: Triage Processing Optimization

**Date**: 2026-02-21  
**Status**: Implemented  
**Iteration**: Cloud — reliability and performance

## Context

The triage pipeline times out in 6 of the last 10 executions. The root cause is architectural: the 30-minute Step Functions timeout clock starts when the first file is dropped (`initTriageSession` starts the SF execution), not when uploads complete. Large video uploads create multi-minute gaps where the timer runs but no processing happens. Additionally, video compression of large files (up to 4.6 GB in the Kona folder) is CPU-intensive and can exceed Lambda limits, and duplicate files waste both upload bandwidth and processing time.

## Decision

Five changes to improve triage pipeline reliability and performance:

| Area | Decision |
|------|----------|
| **SF timeout decoupling** | Don't start SF execution until frontend confirms all uploads complete; S3 events still process files as they arrive, so most are already done when SF starts |
| **Content deduplication** | Two-tier browser-side hashing (quick fingerprint: SHA-256 of size+head+tail; full hash on collision) plus server-side fingerprint safety net |
| **Lambda resources** | Increase MediaProcess Lambda: memory 2→4 GB (~2.3 vCPU), ephemeral storage 4→7 GB, timeout 10→15 min |
| **Adaptive compression** | Select SVT-AV1 preset based on video duration instead of fixed preset 4; presets 6–10 produce nearly identical output sizes but encode 15–70x faster |
| **Server-side dedup** | MediaProcess Lambda checks file-processing DDB table for existing results with same content fingerprint before processing |

### Change 1: Decouple Step Functions Timeout from Upload

The critical reliability fix. `handleTriageInit` now only creates the DDB job (no SF start). A new `handleTriageFinalize` endpoint starts the SF execution after the frontend confirms all uploads are complete. The 30-minute timeout begins when processing needs to happen, not while the user is still uploading.

**Files modified:**
- `cmd/media-lambda/triage.go` — split init, add finalize handler
- `cmd/media-lambda/main.go` — register `/api/triage/finalize` route
- `web/frontend/src/api/client.ts` — add `finalizeTriageUploads`
- `web/frontend/src/components/FileUploader.tsx` — call finalize when all uploads done
- `web/frontend/src/components/TriageView.tsx` — fix error message (15→30 min)
- `internal/store/store.go` — add `Model` field to `TriageJob` (persisted for finalize)

### Change 2: Content-Based Deduplication

Detect and skip duplicate files before uploading using a two-tier hashing strategy. `quickFingerprint` computes SHA-256 of `(fileSize || first64KB || last64KB)` using `crypto.subtle.digest` — runs in milliseconds even for multi-GB files. `fullHash` is only computed when fingerprints collide.

**Files created:**
- `web/frontend/src/utils/fileHash.ts`

**Files modified:**
- `web/frontend/src/components/FileUploader.tsx` — content dedup after filename dedup

### Change 3: Increase Lambda Resources

The Kona folder contains videos up to 4.6 GB. The current 4 GB ephemeral storage cannot hold these, and 2 GB memory provides only ~1 vCPU which makes video encoding very slow.

**Files modified:**
- `cdk/lib/storage-stack.ts` — memory 2048→4096 MB (~2.3 vCPU), ephemeral 4096→7168 MiB, timeout 10→15 min

### Change 4: Adaptive Video Compression

Select SVT-AV1 preset based on video duration rather than using a fixed preset. Duration is the primary driver of encoding time — production data shows compression time scales with video duration, not file size. Presets 6–8 produce nearly identical output sizes to preset 4 but encode 15–35x faster.

Thresholds: ≤10 min → preset 4, 10–60 min → preset 6, 60–180 min → preset 8, >180 min → preset 10.

**Files modified:**
- `internal/filehandler/video_compress.go` — add `SelectPreset`, parameterize `buildFFmpegArgs`
- `cmd/media-process-lambda/processor.go` — use adaptive preset, increase compression timeout 8→12 min

### Change 5: Skip Compression for Duplicate Content

Server-side safety net for duplicates that bypass the frontend dedup (e.g., files selected from different directories). After downloading a file from S3, compute a content fingerprint and check the file-processing DDB table for existing results with the same fingerprint in this session. If found, copy the existing result instead of reprocessing.

**Files modified:**
- `internal/store/file_processing.go` — add fingerprint field, put/get fingerprint mapping
- `cmd/media-process-lambda/processor.go` — fingerprint check before processing

## Rationale

- **Decoupling SF timeout** eliminates the root cause of 60% timeout failures — the timer no longer wastes time on upload delays.
- **Client-side dedup** prevents redundant uploads over slow connections; two-tier hashing avoids false positives while keeping fingerprinting fast (sub-millisecond for multi-GB files).
- **4 GB memory** gives ~2.3 vCPU (proportional allocation) which, combined with faster presets, yields ~61% compute cost reduction for long videos while providing headroom for peak memory on 4K files.
- **Duration-based preset selection** matches the actual bottleneck — encoding time scales linearly with frame count (determined by duration), not file size. Presets 6–8 produce nearly identical compressed sizes (e.g., 3 MB vs 3.6 MB at these small output sizes).
- **Server-side fingerprint dedup** is a low-cost safety net; most duplicates are caught on the client.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Increase SF timeout to 60 min | Masks the problem; SF Express max is 5 min, Standard has no timeout but costs more |
| File-size-based preset selection | Duration is the primary driver of encoding time, not file size |
| Fixed preset 6 for all videos | Loses quality optimization on short videos where preset 4 is fast enough |
| Server-only dedup (no client) | Wastes upload bandwidth and S3 storage for duplicate multi-GB files |
| GSI on fingerprint in DDB | Over-engineering for small per-session datasets; filter expression on Query is sufficient |

## Consequences

**Positive:**

- Triage pipeline should no longer time out on large uploads — SF timer starts only after uploads complete.
- Duplicate detection saves upload bandwidth, S3 storage, and Lambda compute time.
- Large videos (4K, long duration) compress within Lambda limits with appropriate preset selection.
- Cost efficiency: faster presets reduce compute time by 15–70x while producing nearly identical output sizes.

**Trade-offs:**

- Frontend complexity increases with dedup logic and finalize flow.
- Lambda cost per invocation increases (4 GB vs 2 GB memory) but total cost decreases due to faster processing (billed per 1ms).
- Adaptive preset may produce marginally larger compressed files for long videos (8–22% at small absolute sizes).

## Related Documents

- DDR-018 (video compression for Gemini)
- DDR-050 (async dispatch and DynamoDB job state)
- DDR-052 (Step Functions pipelines)
- DDR-054 (multipart upload)
- DDR-061 (S3 event-driven per-file processing)
- DDR-063 (split processing UI screens)
