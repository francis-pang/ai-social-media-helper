# DDR-083: FB Prep Processing Pipeline UX — 3-Dot Progress, Upload Gauge, Status Filters

**Date**: 2026-03-01  
**Status**: Accepted  
**Iteration**: Cloud — FB Prep upload UX parity with Media Triage + new filtering and progress features

## Context

### Problem 1: FB Prep Lacks Server-Side Processing Feedback

Media Triage shows a 3-step mini-pipeline on each file card (Uploading / Downsizing / Thumbnail) driven by server-side status from the MediaProcess Lambda. FB Prep only shows a 2-step pipeline (Uploading / Done) because:

- MediaProcess already runs on FB Prep uploads (S3 event-triggered), but skips writing results to DynamoDB — `findTriageJobID()` returns an error since no `TRIAGE#` record exists for FB Prep sessions.
- No polling endpoint exists for FB Prep file statuses.
- `FBPrepUploader` only tracks local upload engine state, with no lifecycle merging.

The actual processing (downsizing, thumbnail generation) already happens; the gap is plumbing status back to the frontend.

### Problem 2: Processing Order Mismatches Pipeline Labels

The existing pipeline labels in Media Triage are "Uploading / Downsizing / Thumbnail", but the backend already processes thumbnails first (faster, enables preview) then downsizing. The labels should match actual processing order.

### Problem 3: No Upload Progress Granularity Per File

File cards show "40MB / 60.8MB" text during upload but lack a visual gauge. Users cannot quickly scan which files are nearly done.

### Problem 4: No Status Filtering on Large Batches

With 50+ files, users cannot quickly find files in a specific state. The Upload Summary sidebar shows counts but is not interactive.

## Decision

### 1. Session-Scoped File Result Storage

Add `PutSessionFileResult(sessionId, result)` and `GetSessionFileResults(sessionId)` to `FileProcessingStore` using `PK = sessionId`, `SK = file#{filename}`. This is a separate key scheme from the triage-scoped `PK = sessionId#jobId`, requiring no triage job.

MediaProcess now always stores file results: when `jobID != ""`, uses the existing `PutFileResult`; when `jobID == ""`, falls back to `PutSessionFileResult`. Triage-specific operations (`IncrementTriageProcessedCount`, `PutFingerprintMapping`) remain gated on `jobID != ""`.

### 2. Intermediate "thumbnailed" Status

MediaProcess writes a `FileResult` with `status: "thumbnailed"` after generating and uploading the thumbnail, before starting downsizing. This gives the frontend a 4th lifecycle state:

- `uploading` → Uploading (active), Thumbnail (pending), Downsizing (pending)
- `processing` → Uploading (done), Thumbnail (active), Downsizing (pending)
- `thumbnailed` → Uploading (done), Thumbnail (done), Downsizing (active)
- `ready` → all done

### 3. Pipeline Label Reorder

Labels changed from `Uploading / Downsizing / Thumbnail` to `Uploading / Thumbnail / Downsizing` to match the actual backend processing order (thumbnail is generated first for both images and videos).

### 4. Session File-Status API Endpoint

`GET /api/sessions/{sessionId}/file-status` — lightweight, workflow-agnostic endpoint that reads from the file-processing table using the session-only key scheme. Returns the same `FileProcessingStatus` shape used by the triage results endpoint.

### 5. Shared MiniPipeline Component

Extracted pipeline rendering (dots, connectors, labels) into `web/src/components/shared/MiniPipeline.tsx`. Both `FileUploader` and `FBPrepUploader` import the shared component. Step-generation logic stays in each consumer since step labels may differ per workflow.

### 6. Upload Progress Gauge

File cards get a background fill (`.file-card__gauge`) in the info area below the thumbnail, scaled proportionally to upload progress (0–100%). Uses `scaleX` transform for smooth animation. The existing size text ("40MB / 60.8MB") sits on top.

### 7. Multi-Select Status Filter

Upload Summary sidebar rows (dot + label + count) are clickable filter toggles. `statusFilter` is a `Signal<Set<FileLifecycleStatus>>` supporting multi-select. Clicking toggles a status in/out of the set. Active rows get a subtle background highlight. A "Clear" button appears in the header when any filter is active. Counts always show total (unfiltered) values.

## Files Changed

### Backend
- `internal/store/file_processing.go` — `PutSessionFileResult`, `GetSessionFileResults`
- `cmd/media-process/processor.go` — `writeFileResult` helper, intermediate "thumbnailed" writes, unconditional result storage
- `cmd/api/triage.go` — `handleSessionFileStatus` handler
- `cmd/api/main.go` — `/api/sessions/` route registration

### Frontend
- `web/src/types/api.ts` — `"thumbnailed"` added to `FileProcessingStatus.status` union
- `web/src/api/client.ts` — `getSessionFileStatuses(sessionId)`
- `web/src/components/shared/MiniPipeline.tsx` — new shared component
- `web/src/components/FileUploader.tsx` — MiniPipeline import, thumbnailed lifecycle, gauge, clickable filter rows
- `web/src/components/FBPrepUploader.tsx` — full rewrite with polling, lifecycle merging, gauge, filters
- `web/src/style.css` — `.file-card__info`, `.file-card__gauge` rules

## Consequences

- FB Prep and Media Triage now share the same granular processing feedback UX
- MediaProcess writes to DynamoDB for all uploads, adding ~2 DDB writes per file (intermediate + final) — negligible cost at current scale
- The session-only key scheme (`PK = sessionId`) coexists with the triage key scheme (`PK = sessionId#jobId`) in the same table, distinguished by SK prefix
- Future workflows that use the upload engine get server-side processing feedback for free via the session file-status endpoint

## Rejected Alternatives

- **Reuse triage job infrastructure for FB Prep**: Would couple FB Prep to triage's `initTriage`/`finalizeTriageUploads` lifecycle, adding complexity for no benefit
- **Client-side-only processing simulation**: Would show fake progress without actual backend feedback, misleading users about file readiness
- **Single-select filter**: Multi-select is more useful for large batches ("show me everything that's either uploading or generating thumbnails")
