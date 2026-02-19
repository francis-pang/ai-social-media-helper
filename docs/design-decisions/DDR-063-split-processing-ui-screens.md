# DDR-063: Split File Processing and Gemini Request into Separate UI Screens

**Date**: 2026-02-18  
**Status**: Accepted

## Context

The triage flow currently jumps from the upload screen (`triage-upload`) directly to a single "Processing" screen (`processing` step, rendered by `TriageView`) that conflates two distinct phases:

1. **File-level processing** — per-file thumbnail generation and video compression by the MediaProcess Lambda (DDR-061)
2. **Gemini API request** — uploading files to Gemini, video processing, and AI analysis

These phases differ in duration, granularity, and user concern. File processing produces per-file status updates (with thumbnails) that are useful to show inline with the upload list, while the Gemini request is a single long-running operation with coarser progress. Mixing them in one screen makes the user wait on a generic spinner while per-file progress data goes unused.

The backend already returns per-file processing data (`fileStatuses`, `processedCount`, `expectedFileCount`) via `GET /api/triage/:id/results` during the `processing` phase (DDR-061), but currently only when `job.Status == "processing"`. The upload screen cannot display this data during the `pending` phase.

## Decision

Split the triage flow into two UI screens with distinct responsibilities:

- **Screen 1 (`triage-upload` / `FileUploader`)**: Upload files to S3 **and** show per-file server-side processing status (thumbnails, compression). Files are grouped by lifecycle status: Uploading, Processing, Ready, Error. The screen stays visible until all file-level processing completes (`processedCount >= expectedFileCount`).
- **Screen 2 (`processing` / `TriageView`)**: Gemini-only analysis. Shows only the three Gemini sub-phases: uploading to Gemini, video processing, AI analysis.

The backend condition for returning `fileStatuses` is expanded from `job.Status == "processing"` to also include `"pending"`, enabling the upload screen to show processing progress while files are still uploading.

## Rationale

1. **Per-file visibility**: Users see each file's progress through upload → processing → ready, with thumbnails appearing as they complete. This replaces a generic spinner with actionable feedback.
2. **Separation of concerns**: File-level processing is inherently per-file and incremental. Gemini analysis is a batch operation. Different progress models warrant different UI treatments.
3. **Data already available**: The backend's per-file processing data (DDR-061) was being returned but not displayed on the upload screen. This change surfaces existing data.
4. **Minimal backend change**: A single condition change (`"processing"` → `"pending" || "processing"`) in `triage.go` unlocks the feature.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Keep single processing screen, add per-file status list | Doesn't leverage the upload screen where users already see their files; forces a context switch |
| Three-screen flow (upload → processing → gemini) | Over-fragmented; processing completes quickly per file and belongs with upload context |
| WebSocket push for per-file updates | Over-engineering; polling at 2s intervals is already implemented and adequate |

## Consequences

**Positive:**
- Users see real per-file progress (upload → processing → ready with thumbnails) instead of a generic spinner
- Thumbnails appear inline as soon as they're generated, giving immediate visual feedback
- The Gemini processing screen is simplified to its actual scope
- Reduced perceived wait time — users see continuous progress during file processing

**Trade-offs:**
- `FileUploader` component grows in complexity (file grouping, server-side status merge, thumbnail display)
- Upload screen now has a dependency on poll results (previously only used for navigation)

## Related Documents

- [DDR-061](./DDR-061-s3-event-driven-per-file-processing.md) — S3 Event-Driven Per-File Processing (provides `fileStatuses` data)
- [DDR-058](./DDR-058-cloudconvert-style-file-list-restyle.md) — CloudConvert-Style File List UI
- [DDR-056](./DDR-056-loading-ux-and-url-routing.md) — Loading UX and URL Routing
- [DDR-042](./DDR-042-landing-page-workflow-switcher.md) — Landing Page Workflow Switcher
