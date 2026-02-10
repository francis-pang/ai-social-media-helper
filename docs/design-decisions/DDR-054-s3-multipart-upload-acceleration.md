# DDR-054: S3 Multipart Upload Acceleration

**Date**: 2026-02-10  
**Status**: Accepted  
**Iteration**: Phase 2 — Cloud Deployment

## Context

Files are currently uploaded one-at-a-time as a single presigned PUT to S3 (`uploadToS3` in `client.ts`). Both `FileUploader.tsx` and `MediaUploader.tsx` fire off all file uploads concurrently (no concurrency limit), but each file is a single HTTP request. For large videos (up to 5 GB per DDR-028 limits), this means one long-lived connection per file with no parallelism within a file.

Single-PUT uploads have several limitations for large files:
- **No resumability**: A network interruption at 90% means restarting from zero.
- **Single-connection throughput ceiling**: One TCP stream cannot saturate a high-bandwidth link.
- **Browser timeout risk**: Very large files on slower connections may hit browser or API Gateway timeouts.

S3's multipart upload API splits a file into independently-uploadable parts, enabling parallel chunk uploads that can fully utilize available bandwidth.

## Decision

Implement S3 multipart upload with parallel chunks for files exceeding 10 MB. Files at or below 10 MB continue using the existing single presigned PUT path (fast, simple, no overhead).

### Backend (3 new endpoints)

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/api/upload-multipart/init` | POST | Create multipart upload, batch-presign all part URLs |
| `/api/upload-multipart/complete` | POST | Complete multipart upload with ETags |
| `/api/upload-multipart/abort` | POST | Abort multipart upload (cleanup on failure) |

The init endpoint batch-presigns all part URLs in a single round trip, eliminating per-part API calls from the browser.

### Frontend (smart routing)

The `uploadFile` function in both uploaders checks file size:
- **≤10 MB**: Existing `getUploadUrl` → `uploadToS3` (single PUT)
- **>10 MB**: New `uploadToS3Multipart` orchestrator:
  1. Call init to get all presigned part URLs
  2. Slice `File` into chunks via `file.slice(start, end)`
  3. Upload chunks in parallel (concurrency pool of 6)
  4. Track aggregate progress across all chunks
  5. On success: call complete with all ETags
  6. On failure: call abort to clean up orphaned parts

### CDK (CORS update)

Expose the `ETag` response header in S3 CORS configuration so the browser can read ETags from chunk upload responses (required for `CompleteMultipartUpload`).

## Rationale

- **Parallel throughput**: For a 500 MB video with 10 MB chunks, 6 concurrent uploads yield ~6x throughput improvement over a single connection.
- **Browser connection pool alignment**: Browsers allow ~6 connections per origin. Since chunk uploads target S3 (different origin from the API), this maximizes S3-bound connections without contending with API calls.
- **Batch presigning**: All part URLs returned from a single init call avoids N round trips to the API Lambda (one per chunk), reducing latency and Lambda invocation costs.
- **Transparent fallback**: Files under the threshold use the existing path — no behavioral change for small uploads (photos, short clips).
- **No downstream changes**: The final S3 object at `{sessionId}/{filename}` is identical whether uploaded via single PUT or multipart. All triage, selection, and enhancement pipelines work unchanged.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Per-part presign requests (one API call per chunk) | N round trips to Lambda adds latency proportional to file size; a 500 MB file = 50 extra Lambda invocations |
| Client-side chunking with server-side reassembly | Requires server-side storage and assembly logic; S3 multipart is a native primitive that handles this |
| Always use multipart (no threshold) | Multipart has minimum 5 MB part size and adds 2 extra API calls; overhead not worthwhile for small files |
| `tus` or resumable upload protocol | Requires running a tus server; S3 multipart is sufficient and uses native AWS infrastructure |
| Increase single-PUT timeout | Does not address throughput — a single TCP stream still cannot saturate bandwidth |

## Consequences

**Positive:**
- Large video uploads (100 MB–5 GB) complete significantly faster via parallel chunk uploads
- Failed uploads can be retried at the chunk level rather than restarting entirely
- Abort endpoint prevents orphaned multipart uploads from accruing S3 storage costs
- No impact on small file upload UX — threshold keeps the simple path for photos

**Trade-offs:**
- Three new backend endpoints to maintain
- Frontend upload logic becomes more complex (chunking, concurrency pool, ETag collection)
- S3 lifecycle rules should include `AbortIncompleteMultipartUpload` as a safety net for abandoned uploads (already covered by 24h expiration policy)

## Related Documents

- [DDR-026](./DDR-026-phase2-lambda-s3-deployment.md) — Phase 2 Lambda + S3 architecture
- [DDR-028](./DDR-028-security-hardening.md) — Security hardening (input validation, content-type allowlist)
- [DDR-011](./DDR-011-video-metadata-and-upload.md) — Video metadata and large file upload
- [DDR-045](./DDR-045-stateful-stateless-stack-split.md) — Stateful/Stateless stack split (StorageStack owns S3 CORS)
