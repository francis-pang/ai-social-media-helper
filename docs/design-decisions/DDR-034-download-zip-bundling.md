# DDR-034: Download ZIP Bundling with Speed-Based Video Grouping

**Date**: 2026-02-08  
**Status**: Accepted  
**Iteration**: Step 7 — Publish or Download

## Context

Step 7 of the media selection pipeline lets users download their enhanced media grouped by post. Users need a convenient way to download potentially dozens of photos and several large videos from a post group. Individual presigned URL downloads (Option D1) would require clicking each file separately. A single monolithic ZIP (Option D2 via a single Lambda) risks Lambda timeout (15 min) and `/tmp` storage limits (10 GB) for very large media sets.

The target user base uses **AT&T Internet Air** (5G fixed wireless) in San Jose, California. This service provides typical download speeds of **75–225 Mbps**, with a conservative mid-range estimate of **100 Mbps**. Downloads that exceed ~30 seconds feel slow and risk browser timeout or user abandonment.

## Decision

Implement a **Step Functions-orchestrated ZIP bundling strategy** (Option D3) with speed-based video grouping:

1. **One ZIP for all images** in the post group — photos are typically 5–15 MB each; even 20 photos total ~100–300 MB, which downloads in under 30 seconds at 100 Mbps.

2. **Videos split into ZIP bundles of ≤ 375 MB each** — calculated as 30 seconds × 100 Mbps ÷ 8 = 375 MB. If a single video exceeds 375 MB, it gets its own ZIP bundle.

3. **Step Functions state machine** (`DownloadPipeline`) orchestrates parallel ZIP creation with per-bundle retry, concurrency throttling, and fan-in. Each ZIP is created by a separate Lambda invocation (or goroutine in the interim in-process implementation).

4. **Zstandard (zstd) compression at level 12** — ZIP entries are compressed using zstd (ZIP method 93, APPNOTE 6.3.7) at level 12 via `github.com/klauspost/compress/zstd`. Level 12 maps to `SpeedBestCompression` in the Go library — the highest compression available. While media files (JPEG, MP4) are already compressed and see modest reduction, RAW photos (CR2, NEF, TIFF) and uncompressed audio tracks benefit significantly. The zstd encoder at level 12 requires approximately 128 MB of memory per concurrent writer, so the Lambda must be configured with **≥ 2 GB memory**.

5. **ZIP creation in Lambda `/tmp`** — each ZIP is assembled by downloading source files from S3 to Lambda `/tmp`, compressing with zstd into a ZIP file in `/tmp`, and uploading the ZIP back to S3. Since each bundle is capped at 375 MB (videos) or typically ≤ 300 MB (images), the 10 GB `/tmp` limit is never a concern.

6. **Presigned GET URLs** — after ZIP creation, presigned S3 GET URLs (1-hour expiry) are generated for each ZIP bundle and returned to the frontend.

### Speed Threshold Calculation

| Parameter | Value |
|-----------|-------|
| Target ISP | AT&T Internet Air (5G fixed wireless) |
| Location | San Jose, California |
| Advertised speeds | 75–225 Mbps (varies by plan and location) |
| Conservative estimate | 100 Mbps |
| Target download time | ≤ 30 seconds per bundle |
| **Max bundle size** | **100 Mbps × 30s ÷ 8 = 375 MB** |
| ZIP compression | Zstandard (zstd) level 12 via `klauspost/compress` |
| Lambda memory | ≥ 2 GB (zstd level 12 encoder window) |

### Bundle Naming Convention

| Bundle | S3 Key | Download Filename |
|--------|--------|-------------------|
| Images | `{sessionId}/downloads/{jobId}/images.zip` | `{groupLabel}-images.zip` |
| Videos (1st) | `{sessionId}/downloads/{jobId}/videos-1.zip` | `{groupLabel}-videos-1.zip` |
| Videos (2nd) | `{sessionId}/downloads/{jobId}/videos-2.zip` | `{groupLabel}-videos-2.zip` |

### Step Functions State Machine: `DownloadPipeline` (Planned)

```
Start → CalculateBundles → Map: CreateZIP (parallel) → GenerateURLs → Done
```

- **CalculateBundles**: HeadObject on each key, separate images/videos, group videos into ≤ 375 MB bundles
- **Map: CreateZIP**: One Lambda invocation per bundle (MaxConcurrency 5, **2 GB memory**), downloads files from S3, creates zstd-compressed ZIP in `/tmp`, uploads ZIP to S3
- **GenerateURLs**: Presigned GET URLs for each completed ZIP

### Interim Implementation

Since Step Functions infrastructure is not yet deployed, the initial implementation runs ZIP creation as goroutines within the API Lambda (consistent with the existing triage, selection, and enhancement job patterns). The code is structured so the per-bundle ZIP creation logic can be extracted into a standalone Lambda function when Step Functions is deployed.

### API Endpoints

| Method | Path | Action |
|--------|------|--------|
| `POST` | `/api/download/start` | Calculate bundles, start ZIP creation |
| `GET` | `/api/download/{id}/results` | Poll bundle creation status and download URLs |

## Rationale

1. **375 MB threshold is grounded in real ISP data** — AT&T Internet Air in San Jose delivers 75–225 Mbps. Using the conservative 100 Mbps estimate ensures bundles download within 30 seconds even at the lower end of the speed range.

2. **Images bundled together** — even the maximum of 20 photos at 15 MB each = 300 MB, well under the 375 MB threshold and within comfortable download time.

3. **Videos need splitting** — phone videos range from 50 MB (30-second clip) to 1+ GB (long recording). A single 1 GB video would take 80 seconds at 100 Mbps. Splitting into ≤ 375 MB bundles ensures each download completes within the target window.

4. **Step Functions for resilience** — ZIP creation is I/O-intensive (download N files, write ZIP, upload). Step Functions provides automatic retry per bundle, concurrency control, and visual monitoring.

5. **ZIP with zstd compression** — ZIP is universally supported by all operating systems. Zstandard (zstd) at level 12 provides high compression ratios while maintaining fast decompression speed (~1.5 GB/s). macOS 12+ and modern archive utilities (7-Zip, The Unarchiver, `ditto`) support zstd-compressed ZIPs. The higher Lambda memory cost (2 GB vs 256 MB) is offset by reduced S3 storage and faster download times from smaller bundles.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| D1: Individual presigned URLs | Poor UX — user must click each file separately; no batching |
| D2: Single ZIP via Lambda | Risk of Lambda timeout (15 min) and `/tmp` overflow (10 GB) for large media sets; no retry per file |
| Single ZIP for everything | Videos + images together could exceed 375 MB threshold; user must wait for entire download even if they only want photos |
| Fixed file count per ZIP | File sizes vary enormously (1 MB photo vs 500 MB video); count-based splitting doesn't guarantee acceptable download times |
| Client-side ZIP (JSZip) | Browser memory limits; blocks UI thread; no resumability; large files cause tab crashes |
| ZIP with Deflate compression | Lower compression ratio than zstd; slower compression at equivalent ratios; universal compatibility is not needed (target is Chrome on macOS 16+) |
| ZIP with Store (no compression) | No size reduction; wastes bandwidth for RAW photos and uncompressed audio tracks; larger bundles may exceed 375 MB threshold |

## Consequences

**Positive:**
- Each download bundle completes within 30 seconds for typical AT&T Internet Air users
- Images always in a single convenient ZIP
- Videos intelligently grouped by size, not arbitrarily split
- Zstd level 12 provides high compression ratio — reduces download size and S3 transfer costs
- Zstd decompression is extremely fast (~1.5 GB/s) — no perceptible delay for the user
- Parallel ZIP creation reduces total wait time
- Step Functions provides retry and monitoring for production reliability

**Trade-offs:**
- Multiple video ZIPs for large media sets (user downloads 2-3 files instead of 1)
- S3 storage cost for temporary ZIP files (mitigated by 24-hour TTL auto-expiration)
- Lambda requires ≥ 2 GB memory for zstd level 12 encoder (higher cost per invocation vs 256 MB)
- Zstd-compressed ZIPs (method 93) require macOS 12+ or a modern archive utility — older OS versions may not decompress them natively
- Step Functions adds ~$0.001 per download job in state transition costs
- Initial implementation uses goroutines (no Step Functions retry/monitoring until infrastructure is deployed)

## Related Documents

- [DDR-026](./DDR-026-phase2-lambda-s3-deployment.md) — Phase 2 Lambda + S3 Cloud Deployment
- [DDR-028](./DDR-028-security-hardening.md) — Security Hardening
- [DDR-033](./DDR-033-post-grouping-ui.md) — Post Grouping UI
- [Media Selection Feature Update Plan](../../docs/ENHANCEMENT.md) — Step 7 specification
