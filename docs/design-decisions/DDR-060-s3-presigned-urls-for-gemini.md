# DDR-060: S3 Presigned URLs for Gemini Video Transfer

**Date**: 2026-02-14
**Status**: Accepted
**Iteration**: 1

## Context

The Gemini API (Jan 2026) now supports external HTTPS URLs — including S3 presigned URLs — directly in `FileData.FileURI`. This lets Gemini fetch files from S3 on its own, bypassing the two-hop transfer (S3 → Lambda /tmp → Gemini Files API).

The existing triage video flow uses 3 Step Function steps:

1. **triage-prepare** (L122-248): S3 GetObject → /tmp → `client.Files.Upload` (raw, uncompressed)
2. **triage-check-gemini** (L252-293): Poll `client.Files.Get` in a retry loop
3. **triage-run** (L297-500): S3 GetObject → /tmp → `AskMediaTriage` (compress + `uploadVideoFile` + poll) → thumbnails → delete originals (DDR-059)

The triage Lambda uses a "light" container image (no ffmpeg), so it cannot compress videos. Videos are uploaded raw to the Gemini Files API, requiring a separate polling step to wait for Gemini to finish processing.

## Decision

Use S3 presigned GET URLs for videos in the triage and selection pipelines. When a `MediaFile` has a `PresignedURL` set, Gemini fetches the video directly from S3 instead of going through the Files API upload + polling cycle.

### 1. Add `PresignedURL` field to `MediaFile`

Add an optional `PresignedURL` field to `filehandler.MediaFile`. When set, the chat layer uses `FileData{FileURI: presignedURL}` instead of compressing + uploading via the Files API.

### 2. Add S3 presigner to triage Lambda

Use the existing `lambdaboot.S3Clients.Presigner` (already created by `InitS3`) and add a `generatePresignedURL` helper with a 15-minute expiry.

### 3. Simplify `handleTriagePrepare`

Remove the Gemini client creation, file downloads, and `client.Files.Upload` calls. The prepare step now only lists S3 objects and counts files for progress tracking. Always return `HasVideos: false` so the Step Function skips `triage-check-gemini`.

### 4. Set presigned URLs in `handleTriageRun`

After `LoadMediaFile` in the download loop, generate a presigned URL for video files and set `mf.PresignedURL`. Videos are still downloaded to /tmp because `handleTriageRun` needs them for metadata extraction (ffprobe) and DDR-059 thumbnail generation.

### 5. Use presigned URLs in `askMediaTriageSingle`

When `file.PresignedURL != ""`, use `FileData{FileURI: presignedURL}` directly and skip compression + Files API upload. The existing compress + upload path remains as fallback for CLI/local mode where no presigned URL is set.

### 6. Apply same pattern to selection flow

Apply the same conditional in `buildMediaParts` (`selection_media.go`): if `PresignedURL` is set, use it directly. Also add presigner to the selection Lambda.

## Rationale

1. **Eliminates the Gemini Files API upload** — no more `client.Files.Upload` + polling loop for videos in the triage pipeline
2. **Eliminates the `triage-check-gemini` step** — reducing pipeline latency by 5-30 seconds (the polling interval)
3. **No SDK upgrade needed** — `FileData.FileURI` is a string field; the server-side API change means any HTTPS URL works. Current SDK v1.45.0 is sufficient.
4. **Graceful fallback** — CLI/local mode (no S3) continues to use the existing compress + upload path since `PresignedURL` is empty

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Keep current Files API upload | Adds unnecessary latency (upload + polling) when Gemini can fetch directly from S3 |
| Use presigned URLs for images too | Images already use efficient inline thumbnails (~100KB); presigned URLs for full-size images would significantly increase token costs |
| Compress videos before presigned URL | Triage Lambda is a "light" container (no ffmpeg); compression is only possible in "heavy" containers. Raw video via presigned URL is acceptable since it eliminates Lambda-side processing entirely |

## Consequences

**Positive:**
- Pipeline latency reduced by eliminating the `triage-check-gemini` polling step (5-30 seconds)
- Simpler `triage-prepare` step — no file downloads, no Gemini client, just S3 listing
- No Gemini files to clean up after inference (the `defer` cleanup for `uploadedFiles` is skipped for presigned URL paths)
- Works with existing SDK version — no dependency changes needed

**Trade-offs:**
- Raw videos sent via presigned URL are larger than compressed ones, which may increase Gemini processing time and token cost
- Presigned URLs expire after 15 minutes — sufficient for synchronous `GenerateContent` calls but would need adjustment for async workflows
- `storeCompressed` callback is not called for presigned URL paths, so the `compressed/` prefix in S3 won't have triage entries (only selection entries if that flow still compresses)

## Interaction with DDR-059 (Frugal Triage Cleanup)

The timeline within `handleTriageRun` is safe:

1. Download files to /tmp (metadata + thumbnails)
2. `AskMediaTriage` runs — Gemini fetches videos from S3 via presigned URLs during `GenerateContent`
3. **After** `AskMediaTriage` returns: thumbnail generation (L384-418)
4. **After** thumbnails stored: `deleteOriginals` (L488) removes originals from S3

Gemini has already fetched the video content in step 2 before originals are deleted in step 4. The 15-minute presigned URL expiry is also irrelevant since Gemini fetches during the synchronous `GenerateContent` call.

## Related Documents

- [DDR-011: Video Metadata and Upload](./DDR-011-video-metadata-and-upload.md) — original video upload design
- [DDR-012: Files API for All Uploads](./DDR-012-files-api-for-all-uploads.md) — Files API streaming upload
- [DDR-018: Video Compression for Gemini](./DDR-018-video-compression-gemini3.md) — compression pipeline
- [DDR-052: Step Functions Polling](./DDR-052-step-functions-polling-for-long-running-ops.md) — triage pipeline architecture
- [DDR-053: Granular Lambda Split](./DDR-053-granular-lambda-split.md) — triage Lambda entrypoint
- [DDR-059: Frugal Triage Cleanup](./DDR-059-frugal-triage-s3-cleanup.md) — S3 cleanup interaction
