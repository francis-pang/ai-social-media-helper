# DDR-030: Cloud Selection Backend Architecture

**Date**: 2026-02-08  
**Status**: Accepted  
**Iteration**: Step 2 & 3 of Media Selection Flow

## Context

The cloud-hosted media selection flow (DDR-029 established Step 1: Upload) needs a backend to orchestrate AI-powered media selection (Step 2) and serve structured results for the frontend review UI (Step 3).

The existing triage flow processes media within a single API Lambda invocation using in-memory goroutines. The selection flow has different characteristics:

1. **Comparative analysis required** — Gemini must see ALL media simultaneously to compare duplicates, group scenes, and make cross-item judgments. The Gemini call cannot be split per-file.
2. **Video compression is CPU-intensive** — Each video needs ffmpeg compression (AV1+Opus) before upload to Gemini Files API. This takes 10-30 seconds per video.
3. **Image processing is fast** — Thumbnail generation for images takes ~100ms each and parallelizes well with goroutines.
4. **Thumbnails should be pre-generated** — All downstream steps (review, enhancement, grouping, publishing) need thumbnails. Pre-generating during selection avoids repeated on-the-fly work.

Key constraints:
- API Gateway has a 30-second response timeout (but Lambda can run up to 15 minutes for background processing)
- The Gemini API call itself takes 30-90 seconds depending on media count
- Video compression is the secondary bottleneck (~10-30s per video)
- The user targets Chrome on macOS with an AWS Lambda backend

## Decision

### 1. Architecture: Single Lambda for All Images, Per-Video Lambda for Videos

**Target architecture** (to be deployed with Step Functions infrastructure):

```
POST /api/selection/start
    → API Lambda → Step Functions: SelectionPipeline
        → Map: Video Lambda (1 per video, MaxConcurrency 5)
            - Download video from S3
            - Compress with ffmpeg (AV1+Opus)
            - Upload to Gemini Files API
            - Return Gemini file URI
        → Selection Lambda (1 for everything)
            - Download all images from S3
            - Generate thumbnails (goroutines)
            - Upload thumbnails to S3 cache
            - Use video URIs from Map step
            - Single Gemini API call with all media
            - Parse structured JSON response
            - Write results to DynamoDB
```

**Initial implementation** (this session): All processing runs within the existing API Lambda using goroutines. Video compression and image thumbnail generation happen concurrently. The code is structured into separable functions ready for extraction into dedicated Lambda binaries.

This means:
- Image thumbnails are generated in parallel (goroutines within one invocation)
- Videos are compressed in parallel (one goroutine per video within the same invocation)
- A single Gemini API call receives all media for comparative analysis
- Results are stored in-memory (consistent with existing triage pattern)

The separation into multiple Lambdas and Step Functions is an infrastructure concern deferred to when DynamoDB and Step Functions are deployed.

### 2. Structured JSON Output from Gemini

The existing `AskMediaSelection()` returns freeform markdown text. The new `AskMediaSelectionJSON()` returns structured JSON for programmatic parsing:

```json
{
  "selected": [
    {
      "rank": 1,
      "media": 3,
      "filename": "IMG_001.jpg",
      "type": "Photo",
      "scene": "Tokyo Tower",
      "justification": "Iconic landmark with great composition",
      "comparisonNote": "Chosen over IMG_002.jpg because of better lighting"
    }
  ],
  "excluded": [
    {
      "media": 5,
      "filename": "IMG_005.jpg",
      "reason": "Near-duplicate of IMG_001.jpg with worse composition",
      "category": "near-duplicate",
      "duplicateOf": "IMG_001.jpg"
    }
  ],
  "sceneGroups": [
    {
      "name": "Tokyo Tower",
      "gps": "35.6586, 139.7454",
      "timeRange": "2:00 PM - 3:30 PM",
      "items": [
        {"media": 3, "filename": "IMG_001.jpg", "type": "Photo", "selected": true, "description": "Tower from main entrance"},
        {"media": 5, "filename": "IMG_005.jpg", "type": "Photo", "selected": false, "description": "Similar angle, worse lighting"}
      ]
    }
  ]
}
```

The system instruction tells Gemini to respond with ONLY a JSON object (no markdown fences, no explanatory text). A robust parser strips fences if present (like the triage parser).

### 3. No Maximum Item Limit

The existing `AskMediaSelection()` enforces `maxItems` (defaulting to 20, the Instagram carousel limit). For the new flow, selection and grouping are separate steps:

- **Step 2-3 (Selection)**: AI selects ALL worthy items with no cap
- **Step 6 (Grouping)**: User manually groups into posts (max 20 per carousel)

This allows the AI to surface all good media without artificial limits, and the user decides how to distribute them across posts.

### 4. Thumbnail Pre-Generation and Caching

During selection processing, thumbnails are generated and cached in S3:

- **Images**: 1024px thumbnails generated with existing `GenerateThumbnail()`, uploaded to `{sessionId}/thumbnails/{filename}.jpg`
- **Videos**: Frame extraction at 1 second using ffmpeg, uploaded to `{sessionId}/thumbnails/{filename}.jpg`

The existing `/api/media/thumbnail` endpoint is updated to check for pre-generated thumbnails first (keys under `{sessionId}/thumbnails/`), serving them directly from S3 without regeneration. This eliminates redundant processing for all downstream steps.

### 5. Video Thumbnail Generation

Added `GenerateVideoThumbnail()` to `filehandler` package, using ffmpeg to extract a frame at the 1-second mark. This replaces the SVG placeholder that the thumbnail endpoint currently returns for videos, providing actual visual previews in the selection review UI.

### 6. Frontend Review with Override

The review UI (Step 3) displays:
- **Selected media**: Grid of thumbnails with rank, type badge, scene label, justification, and comparison notes
- **Excluded media**: Collapsible section with thumbnails, exclusion reason, and category
- **Override capability**: Click to move items between selected/excluded
- **Scene groups**: Grouped view showing how media is organized by detected scenes

Overrides are client-side only — no backend call is needed. The final selection state is carried forward to Step 4 (Enhancement).

## Rationale

### Why single Lambda for images + per-video Lambda (not per-file Lambda for everything)?

Image thumbnail generation is fast (~100ms each). Goroutines within a single Lambda handle 50 images in a few seconds. The overhead of invoking a separate Lambda per image (cold start, S3 round-trips, coordination) exceeds the processing time.

Video compression is CPU-intensive (~10-30s each with ffmpeg). Per-video Lambda invocations provide:
- True parallel execution across multiple Lambda containers (not sharing CPU)
- Independent failure handling (one video failure doesn't block others)
- Memory isolation (each Lambda gets its own 4GB+ for ffmpeg)

### Why not per-file Lambda for everything?

The Gemini selection call MUST receive all media in a single request for comparative analysis. Per-file Lambdas could only help with pre-processing (thumbnails, compression), not the selection itself. For images, the pre-processing is so fast that the Lambda invocation overhead (100-500ms cold start) exceeds the processing time.

### Why structured JSON (not freeform text)?

Freeform text requires brittle regex parsing and makes the frontend dependent on Gemini's exact formatting. Structured JSON enables:
- Type-safe Go structs with `json.Unmarshal()`
- Direct mapping to TypeScript interfaces
- Reliable frontend rendering without text parsing
- Programmatic access to scene groups, exclusion reasons, and comparison notes

### Why in-memory state (not DynamoDB)?

DynamoDB is the planned persistent store (see plan's Infrastructure section) but is not yet deployed. The in-memory approach is consistent with the existing triage pattern and works reliably for single-session usage. The migration to DynamoDB is a separate infrastructure task that will enable: multi-Lambda coordination, container restart resilience, and long-running job recovery.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Per-file Lambda for all media (images + videos) | Lambda invocation overhead exceeds image processing time; only beneficial for videos |
| Step Functions for the initial implementation | Requires infrastructure deployment (state machine definitions, IAM roles, DynamoDB) that is orthogonal to the selection logic |
| Keep freeform text output from Gemini | Fragile parsing, poor frontend integration, no type safety |
| Keep maxItems=20 limit | Artificially constrains selection; grouping into posts is a separate step |
| On-the-fly thumbnails only (no caching) | Redundant processing on every thumbnail request; slow UI for large sets |

## Consequences

**Positive:**
- Selection results are structured and type-safe across Go and TypeScript
- Thumbnail caching eliminates redundant processing for all downstream steps
- Video thumbnails replace SVG placeholders with actual frame previews
- No maxItems limit lets AI surface all worthy media
- Code is structured for future Lambda/Step Functions split without refactoring

**Trade-offs:**
- In-memory job state is lost if Lambda container is recycled mid-processing (same risk as triage; mitigated by DynamoDB migration)
- Goroutine-based video compression shares CPU within one Lambda (future per-video Lambda will provide true isolation)
- S3 thumbnail cache uses additional storage (mitigated by 24-hour auto-expiration on the S3 bucket)

## Related Documents

- [DDR-016](./DDR-016-quality-agnostic-photo-selection.md) — Quality-Agnostic Photo Selection
- [DDR-017](./DDR-017-francis-reference-photo.md) — Francis Reference Photo
- [DDR-018](./DDR-018-video-compression-gemini3.md) — Video Compression for Gemini
- [DDR-020](./DDR-020-mixed-media-selection.md) — Mixed Media Selection Strategy
- [DDR-029](./DDR-029-file-system-access-api-upload.md) — File System Access API Upload (Step 1)
- [Media Selection Feature Plan](../../.cursor/plans/media_selection_feature_update_141c5fac.plan.md) — Full feature plan
