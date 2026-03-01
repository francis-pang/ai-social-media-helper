# DDR-071: Photo Downscaling and Media Resolution Strategy

**Date**: 2026-02-28  
**Status**: Accepted  
**Iteration**: Cloud — performance and cost

## Context

The media processing pipeline (DDR-061) compresses videos for Gemini using AV1+Opus (DDR-018) but passes photos through unmodified. iPhone photos are typically 4032x3024 at 6–8 MB. This creates three problems:

1. **Exceeds inline data limit**: Gemini 3 Pro's inline data limit is 7 MB per file. Several iPhone photos in typical sessions are 6–8 MB, right at or above this limit.
2. **Wasted bandwidth**: Gemini 3 Pro uses fixed token counts per image based on the `media_resolution` parameter (LOW=280, MEDIUM=560, HIGH=1120, ULTRA_HIGH=2240 tokens) — input pixel dimensions do not affect token cost. Sending a 7 MB photo uses the same tokens as a 500 KB photo at the same resolution level. The extra bandwidth is pure waste.
3. **Inconsistency**: Videos get compressed and show a "CONVERTED" badge in the UI; photos do not.

Additionally, triage and selection have different quality needs:
- **Triage** (keep/reject): Only needs to assess whether a photo is worth keeping. High resolution adds no value — a downscaled image with LOW media resolution is sufficient.
- **Selection** (best-of-set for Instagram/TikTok): Needs more detail to judge composition, sharpness, and quality for social media optimization. HIGH media resolution is appropriate.

The default model remains `gemini-3-flash-preview` for speed and cost. Wherever the Pro model is used explicitly (e.g., Phase 2 enhancement), DDR-064 already upgraded it to `gemini-3.1-pro-preview`.

## Decision

Three changes:

### 1. Downscale large photos and convert to WebP

| Condition | Action |
|-----------|--------|
| Longest edge > 2000px | Resize to 1920px longest edge |
| Longest edge ≤ 2000px AND not HEIC | Skip resize, use original |
| HEIC/HEIF (any size) | Always convert (HEIC → WebP) |

**Output format** — WebP when ffmpeg is available (Lambda), JPEG as pure-Go fallback (CLI):

| Environment | Input Formats | Output | Method |
|-------------|---------------|--------|--------|
| Lambda (ffmpeg available) | JPEG, PNG, HEIC/HEIF | **WebP quality 85** | ffmpeg + libwebp |
| CLI (no ffmpeg) | JPEG, PNG | JPEG quality 85 | Pure Go (`golang.org/x/image/draw`) |
| CLI (no ffmpeg) | HEIC/HEIF | Skip (use original) | — |
| Any | GIF, WebP | Skip (use original) | — |

WebP is ~30-40% smaller than JPEG at equivalent quality (benchmarks: ~300ms encode time vs ~1200ms for AVIF). Gemini natively supports `image/webp`.

Resized photos go to `{sessionId}/processed/{baseName}.webp` (or `.jpg` for JPEG fallback). `converted = true` is set on success.

### 2. Set `media_resolution` per pipeline stage

| Pipeline | `media_resolution` | Tokens/Image | Rationale |
|----------|--------------------|--------------|-----------|
| **Triage** | `LOW` | 280 | Keep/reject decisions don't need tile-level detail; avoids Gemini splitting the image into multiple tiles |
| **Selection** | `HIGH` | 1120 | Instagram/TikTok optimization needs composition and sharpness analysis |

### 3. Model unchanged

Default remains `gemini-3-flash-preview` for triage and selection (speed + cost). Wherever Pro is used explicitly (Phase 2 enhancement analysis), DDR-064 already ensures it is `gemini-3.1-pro-preview`.

## Rationale

### Why downscale at all if tokens are fixed?

The model internally downsamples images to fit its token budget regardless of input resolution. Downscaling client-side avoids sending megabytes of pixel data that the model will discard:
- **Bandwidth**: 7 MB → ~400 KB per photo (WebP at 1920px), ~94% reduction
- **Inline limit**: Stays comfortably under 7 MB
- **Latency**: Smaller payloads = faster API calls
- **S3 storage**: Smaller processed files until cleanup (DDR-059)

### Why WebP?

- **30-40% smaller** than JPEG at equivalent perceptual quality
- **Fast encoding**: ~300ms per image via ffmpeg libwebp — negligible in Lambda
- **Natively supported** by Gemini API (`image/webp` in supported MIME types)
- **ffmpeg always available** in the MediaProcess Lambda (heavy container, DDR-027)
- Pure-Go WebP encoding requires CGO (`chai2010/webp`), but ffmpeg sidesteps this entirely

### Why not AVIF?

AVIF would be ~50-60% smaller than JPEG (20-30% smaller than WebP), but encoding is ~4x slower (~1200ms vs ~300ms). For batch processing 15+ photos in a Lambda invocation, this adds up. WebP provides the best speed-to-compression ratio for this use case.

### Why LOW for triage?

Triage is a binary keep/reject decision. The model only needs to see whether the photo has a subject, isn't blurry, and isn't a duplicate. At LOW (280 tokens), the image is processed as a single tile — no splitting. This is 4x cheaper than the default HIGH (1120 tokens) with no quality loss for this task.

### Why HIGH for selection?

Selection picks the best photos for Instagram/TikTok. The model needs to evaluate composition, sharpness, lighting, and subject detail to rank photos against each other. HIGH (1120 tokens) provides the detail needed for these quality judgments.

### Why 1920px target?

Matched to the highest resolution either target social media platform actually displays:

| Platform | Format | Resolution | Longest Edge |
|----------|--------|-----------|-------------|
| Instagram | Portrait post (4:5) | 1080 x 1350 | 1350px |
| Instagram | Stories / Reels (9:16) | 1080 x 1920 | **1920px** |
| TikTok | Full-screen portrait (9:16) | 1080 x 1920 | **1920px** |
| TikTok | 4:5 portrait | 1080 x 1350 | 1350px |

Both platforms cap width at 1080px and recompress anything larger. 1920px is the ceiling — there is no benefit to Gemini analyzing pixels that will be discarded by the final platform. With WebP at quality 85, a 1920px photo is ~400 KB (~94% reduction from 7 MB).

### Why keep Flash as default?

- Triage and selection are high-throughput operations (15+ files per session). Flash is faster and cheaper.
- The `media_resolution` parameter (LOW for triage, HIGH for selection) is the primary quality lever — not the model.
- Pro is reserved for enhancement analysis (Phase 2) where deeper reasoning justifies the cost.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| JPEG output (original approach) | WebP is 30-40% smaller at same quality; ffmpeg is always available in Lambda |
| AVIF output | 4x slower encoding (~1200ms vs ~300ms); marginal size improvement over WebP doesn't justify the latency |
| Pure-Go WebP encoding | Requires CGO (`chai2010/webp`) — contradicts static binary strategy (DDR-027); ffmpeg libwebp avoids this |
| Upgrade default to Gemini 3.1 Pro | Flash is faster and cheaper for high-throughput triage/selection; Pro reserved for enhancement |
| MEDIUM for triage | LOW is sufficient for binary decisions; MEDIUM adds cost with marginal benefit |
| ULTRA_HIGH for selection | Only available as per-part setting (v1alpha API); HIGH is sufficient for social media quality assessment |
| Different downscale targets per pipeline | Downscaling happens in MediaProcess Lambda before pipeline is known; single target is simpler |
| No downscaling, rely only on `media_resolution` | Doesn't address bandwidth waste, inline limit, or S3 storage |

## Consequences

**Positive:**
- ~94% file size reduction per photo with WebP (7 MB → ~400 KB)
- 4x token savings on triage (LOW=280 vs default HIGH=1120 per image)
- Consistent "CONVERTED" badge for photos and videos
- HEIC converted to WebP (better compression than JPEG, supported by Gemini)
- Graceful degradation: CLI without ffmpeg falls back to pure-Go JPEG

**Trade-offs:**
- Original full-resolution photo not sent to Gemini (preserved in S3 for download/enhancement)
- WebP re-encoding is lossy (imperceptible at quality 85 for AI analysis)
- ffmpeg invocation per photo adds ~300ms Lambda processing time

## Implementation

### Files Modified

| File | Change |
|------|--------|
| `internal/chat/model.go` | Unchanged — default stays `gemini-3-flash-preview` |
| `internal/chat/triage.go` | Add `MediaResolution: genai.MediaResolutionLow` to all triage configs |
| `internal/chat/selection_media.go` | Add `MediaResolution: genai.MediaResolutionHigh` to all selection configs |
| `internal/chat/selection_photo.go` | Add `MediaResolution: genai.MediaResolutionHigh` |
| `internal/filehandler/image_resize.go` | New: `ResizeImageForGemini()` — ffmpeg WebP primary, pure-Go JPEG fallback |
| `cmd/media-process-lambda/processor.go` | Resize large images, derive output extension from MIME type, set `converted = true` |

## Related Documents

- DDR-018 (video compression for Gemini — no-upscale rule, 768px for video, AV1+Opus)
- DDR-027 (container image Lambda — ffmpeg availability, no-CGO constraint)
- DDR-061 (S3 event-driven per-file processing — processing architecture)
- DDR-064 (Gemini 3.1 Pro model upgrade — Pro model constant)
- DDR-067 (triage processing optimization — adaptive presets, Lambda resources)
