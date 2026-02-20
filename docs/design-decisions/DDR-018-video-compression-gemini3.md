# DDR-018: Video Compression for Gemini 3 Pro Optimization

**Date**: 2026-01-01  
**Status**: Accepted  
**Iteration**: 11.1

## Context

When uploading videos to Gemini for multimodal analysis, large video files (often 500MB-1GB from modern smartphones) create several problems:

1. **Cost**: Gemini bills by token count. A 4K 60fps video generates significantly more tokens than necessary for AI analysis.
2. **Upload time**: Large files take 2-5+ minutes to upload via the Files API.
3. **Processing time**: Gemini's processing queue is slower for larger files.
4. **Quota**: The Files API has a 20GB storage limit; large videos exhaust this quickly.

Gemini 3.1 Pro uses a tiling system for video frames:
- Frames ≤768px: Single tile (258 tokens/frame at MEDIUM, ~70 tokens at LOW)
- Frames >768px: Multiple tiles, significantly increasing token cost

Gemini 3.1 Pro has native multimodal audio understanding, making audio quality important but not requiring studio-grade fidelity.

## Decision

Implement **always-on video compression** before Files API upload using next-generation codecs optimized for Google's ecosystem.

### Video Compression Profile (AV1)

| Parameter | Value | Rationale |
|-----------|-------|-----------|
| Codec | **AV1 (libsvtav1)** | 30-50% smaller than H.265, Google-developed standard |
| Container | **WebM** | Native support for AV1+Opus, preferred by Google |
| CRF | 35 | AV1 handles higher CRF well (range 0-63) |
| Preset | 4 | Balance of speed and efficiency (0-13, lower=slower+better) |
| Max Resolution | 768px (longest edge) | Single-tile processing |
| Max Frame Rate | 5 FPS | Sufficient for temporal analysis |
| Pixel Format | yuv420p | Universal decoder compatibility |

### Audio Compression Profile (Opus)

| Parameter | Value | Rationale |
|-----------|-------|-----------|
| Codec | **Opus (libopus)** | Most efficient audio codec, optimized for Gemini Live |
| Channels | Mono | Sufficient for AI, halves audio size |
| Bitrate | 24kbps VBR | Opus excels at low bitrates, perfect for speech |
| VBR | On | Variable bitrate for extra efficiency |

### No-Upscaling Rule

**Critical**: Never upscale any attribute. If source quality is lower than target, preserve original values:

- 480p source → Keep 480p (don't upscale to 768p)
- 3 FPS source → Keep 3 FPS (don't upscale to 5 FPS)
- 22kHz audio → Keep 22kHz (don't upscale to 44.1kHz)

Upscaling creates artificial data, wastes bandwidth, and provides no benefit to AI analysis.

### Aspect Ratio Preservation

Do NOT pad to square (768x768). Preserve native aspect ratio and only downscale if the longest edge exceeds 768px.

### Metadata Handling

Extract metadata from the **original** file before compression, as compression strips vendor-specific metadata (GPS, timestamps, device info). The prompt is built using original metadata; only the compressed file is uploaded.

## Rationale

### Why AV1 over H.265?

- **30-50% better compression** than H.265 at equivalent quality
- Google is a primary driver of the AV1 standard (AOMedia)
- Natively supported across Gemini API ecosystem
- `libsvtav1` is fast and efficient software encoder
- Encoding time is not a priority for this use case

### Why Opus over AAC?

- **World's most efficient audio codec** for speech and mixed content
- At 24-32kbps, Opus sounds better than AAC at 64kbps
- Default codec for Gemini Live
- Specifically optimized for AI speech-to-text processing

### Why WebM container?

- Native container for AV1 + Opus
- Preferred by Google's infrastructure
- Well-supported by Gemini API

### Why 768px resolution?

Gemini 3.1 Pro's tiling system treats frames ≤768px as single tiles. Exceeding this threshold causes frame splitting into multiple tiles, multiplying token cost with no benefit for typical social media analysis tasks.

### Why 5 FPS?

- Gemini 1.5 sampled at 1 FPS; Gemini 3.1 Pro can utilize higher frame rates
- 5 FPS provides good temporal resolution for motion/gesture detection
- Higher rates waste tokens without improving analysis quality
- A 35-second video at 5 FPS = 175 frames

### Why always-on compression?

- Prevents "bill shock" from accidentally uploading raw 4K files
- Consistent, predictable token costs
- Faster upload times
- Users can still analyze the full-quality original if needed via other tools

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Optional compression (--compress flag) | Risk of accidental large uploads, inconsistent costs |
| H.265 codec | AV1 is 30-50% smaller at same quality |
| AAC audio | Opus is ~50% more efficient at low bitrates |
| MP4 container | WebM is native for AV1+Opus, preferred by Google |
| Square padding (768x768) | Wastes tokens on letterbox/pillarbox regions |
| 1 FPS (like Gemini 1.5) | Gemini 3.1 Pro benefits from higher temporal resolution |
| Strip audio entirely | Loses valuable context for minimal token savings |
| Upscale low-quality sources | Wastes bandwidth, creates artificial data |

## Consequences

**Positive:**
- Consistent, predictable token costs regardless of source video size
- **60%+ smaller files** compared to H.265 approach
- Upload times reduced from minutes to seconds
- Files API quota lasts much longer
- High-quality AI analysis maintained
- Audio quality excellent at 24kbps with Opus

**Trade-offs:**
- FFmpeg becomes a required dependency (must include libsvtav1 and libopus)
- Compression adds processing time before upload (but encoding time is not a priority)
- Compressed temp files need cleanup (handled via cleanup function)
- Original video quality not preserved in upload (but metadata is)
- Some older FFmpeg builds may not have libsvtav1

## Implementation

### New File: `internal/filehandler/video_compress.go`

```go
// CompressVideoForGemini compresses a video for optimal Gemini upload.
// Uses AV1 video codec and Opus audio codec for maximum efficiency.
// Returns outputPath, outputSize, cleanup function, error.
// The cleanup function MUST be called to remove the temp file.
func CompressVideoForGemini(ctx context.Context, inputPath string, metadata *VideoMetadata) (
    outputPath string,
    outputSize int64,
    cleanup func(),
    err error,
)
```

### FFmpeg Command

```bash
ffmpeg -i input.mp4 \
    -c:v libsvtav1 -preset 4 -crf 35 \
    -vf "scale='min(768,iw)':-2,format=yuv420p" \
    -r 5 \                                    # or min(5, source_fps)
    -map 0:v:0 -map 0:a? \                    # safe audio mapping
    -c:a libopus -b:a 24k -vbr on -ac 1 \
    -y output.webm
```

### Integration Point

Compression is invoked in `chat.AskMediaQuestion()` before `uploadAndWaitForProcessing()`. The compressed temp file is deleted after upload completes.

## Related Decisions

- DDR-011: Video Metadata Extraction and Large File Upload (ffprobe dependency)
- DDR-012: Files API for All Uploads (streaming upload pattern)
- DDR-013: Unified Metadata Architecture (metadata from original file)

## Token Cost Analysis

Example: 35-second 4K 60fps video (typical iPhone recording)

| Stage | No Compression | H.265 | AV1+Opus |
|-------|----------------|-------|----------|
| File Size | ~600MB | ~5MB | **~2MB** |
| Upload Time | 3-5 minutes | 5-10 sec | **3-5 sec** |
| Video Tokens (MEDIUM) | ~540,000 | ~45,000 | ~45,000 |
| Video Tokens (LOW) | - | ~14,000 | **~14,000** |
| Audio Tokens | ~1,120 | ~1,120 | ~1,120 |
| **File Size Reduction** | - | 99.2% | **99.7%** |

## Codec Comparison

| Feature | H.265 (HEVC) | AV1 | Benefit |
|---------|--------------|-----|---------|
| Video Compression | Baseline | **30-50% better** | Smaller files |
| Audio (AAC vs Opus) | 128kbps typical | **24kbps** | 80% smaller audio |
| Google Support | Good | **Native/Preferred** | Better compatibility |
| Container | MP4 | **WebM** | Lighter container |

