# Video Enhancement Pipeline

**DDR-032**: Multi-Step Frame-Based Video Enhancement

## Overview

The video enhancement pipeline achieves AI-quality visual enhancement for video by decomposing footage into individual frames, enhancing representative frames using Gemini 3 Pro Image and Imagen 3, then reassembling the video with enhancements propagated consistently across all frames.

This approach leverages the same AI models used for photo enhancement (DDR-031) and applies them to video — something no single Google AI service can do natively.

## How It Works

```
┌─────────────────────────────────────────────────────────────────┐
│                    VIDEO ENHANCEMENT PIPELINE                    │
│                                                                  │
│  ┌──────────┐   ┌──────────┐   ┌──────────────┐                │
│  │  Phase 1  │   │  Phase 2  │   │   Phase 3    │                │
│  │  Extract  │──▶│  Group   │──▶│   Gemini 3   │                │
│  │  Frames   │   │  Frames   │   │  Pro Image   │                │
│  │ (ffmpeg)  │   │(histogram)│   │ Enhancement  │                │
│  └──────────┘   └──────────┘   └──────┬───────┘                │
│                                        │                         │
│                                        ▼                         │
│  ┌──────────┐   ┌──────────────────────────────┐                │
│  │  Phase 5  │   │         Phase 4              │                │
│  │ Reassemble│◀──│  Gemini Analysis + Imagen 3  │                │
│  │  Video    │   │  (iterative improvement)     │                │
│  │ (ffmpeg)  │   └──────────────────────────────┘                │
│  └──────────┘                                                    │
└─────────────────────────────────────────────────────────────────┘
```

### Phase 1: Frame Extraction

Frames are extracted from the video using ffmpeg at the highest quality (JPEG quality 2). The extraction FPS is automatically reduced for longer videos to keep the total frame count manageable:

| Video Length | Extraction FPS | Approx. Frames |
|-------------|---------------|----------------|
| 0-30s       | Original (≤30) | Up to 900      |
| 30-60s      | 15fps         | 450-900        |
| 60-120s     | 10fps         | 600-1,200      |
| >120s       | 5fps          | Varies         |

### Phase 2: Frame Grouping

Consecutive frames are grouped by color histogram similarity. A 3D RGB color histogram (32 bins per channel) is computed for each frame, and consecutive frames with Pearson correlation ≥ 0.92 are placed in the same group.

Each group selects its **middle frame** as the representative for enhancement. This approach ensures:
- Only ~15-30 AI enhancement calls per 30-second video (instead of 900)
- Temporal consistency within each group
- Scene changes are properly detected and handled

### Phase 3: Gemini 3 Pro Image Enhancement

Each group's representative frame is sent to Gemini 3 Pro Image for AI enhancement:
- Exposure and lighting optimization
- Color correction and white balance
- Contrast and vibrancy boost
- Sharpness and clarity improvement
- Noise reduction
- Professional color grading appropriate for the scene type

The enhancement uses a video-specific prompt that emphasizes color and lighting improvements (which propagate best across frames) over spatial edits.

### Phase 4: Analysis + Imagen 3 Iteration

After initial enhancement, each representative frame is analyzed by Gemini 3 Pro (text model) to identify remaining improvements. The analysis returns structured JSON with:
- Remaining issues and their locations
- Whether each issue needs Imagen 3 (localized edit) or another Gemini pass (global edit)
- Professional quality score (1-10)
- Whether the frame is safe for propagation across the video

Improvements are applied iteratively until:
- The professional score reaches 8.5+
- No further edits are needed
- Maximum 3 iterations are reached

Only improvements flagged as `safeForPropagation: true` are applied — this prevents issues like removing moving objects that would look inconsistent across frames.

### Phase 5: Frame Reassembly

The enhancement from each representative frame is propagated to all frames in its group using a **3D color Look-Up Table (LUT)**:

1. A color mapping is computed between the original and enhanced representative frame
2. The LUT is applied to every frame in the group using ffmpeg's `lut3d` filter
3. All enhanced frames are stitched back into a video with original audio preserved

This LUT approach guarantees:
- Zero flickering between consecutive frames
- Identical color grading across each scene
- Original audio is preserved exactly (no re-encoding)

## Architecture

### Files

| File | Purpose |
|------|---------|
| `internal/chat/video_enhance.go` | Orchestrator — runs the 5-phase pipeline |
| `internal/filehandler/video_frames.go` | Frame extraction and video reassembly (ffmpeg) |
| `internal/filehandler/video_histogram.go` | Color histogram computation, frame grouping, LUT generation |
| `internal/assets/prompts/video-enhancement-system.txt` | System prompt for video frame enhancement |
| `internal/assets/prompts/video-enhancement-analysis.txt` | Analysis prompt for identifying further improvements |
| `internal/assets/prompts.go` | Embedded prompt declarations |

### Dependencies

| Dependency | Purpose | Type |
|-----------|---------|------|
| ffmpeg | Frame extraction, LUT application, video reassembly | External binary |
| Gemini 3 Pro Image API | AI image editing (google.golang.org/genai SDK) | SDK |
| Gemini 3 Pro API | Enhancement analysis (google.golang.org/genai SDK) | SDK |
| Imagen 3 API (Vertex AI) | Mask-based surgical edits (REST, optional) | API |

### Reused Components (from DDR-031)

- `GeminiImageClient.EditImage()` — Gemini 3 Pro Image editing
- `GeminiImageClient.AnalyzeImage()` — Gemini 3 Pro text analysis
- `ImagenClient.EditWithMask()` — Imagen 3 mask-based editing
- `GenerateRegionMask()` — Mask generation for Imagen edits

## Usage

### Basic Enhancement

```go
config := chat.VideoEnhancementConfig{
    GeminiAPIKey: "your-api-key",
}

result, err := chat.EnhanceVideo(ctx, "input.mp4", "output.mp4", metadata, config)
if err != nil {
    log.Fatal().Err(err).Msg("Enhancement failed")
}

fmt.Printf("Enhanced %d frames in %d groups (%.1fs)\n",
    result.TotalFrames, result.TotalGroups, result.TotalDuration.Seconds())
```

### With Imagen 3 (Vertex AI)

```go
config := chat.VideoEnhancementConfig{
    GeminiAPIKey:        "your-gemini-api-key",
    VertexAIProject:     "your-gcp-project",
    VertexAIRegion:      "us-central1",
    VertexAIAccessToken: "your-oauth-token",
}
```

### With User Feedback

```go
config := chat.VideoEnhancementConfig{
    GeminiAPIKey: "your-api-key",
    UserFeedback: "Make the colors warmer and increase contrast",
}

// Re-enhance with feedback
result, err := chat.EnhanceVideo(ctx, "enhanced.mp4", "re-enhanced.mp4", metadata, config)
```

### Custom Thresholds

```go
config := chat.VideoEnhancementConfig{
    GeminiAPIKey:            "your-api-key",
    SimilarityThreshold:     0.90, // More aggressive grouping (fewer groups)
    MaxAnalysisIterations:   5,    // More iterations for higher quality
    TargetProfessionalScore: 9.0,  // Higher quality target
}
```

## Cost Estimates

| Video Length | Groups | Gemini Cost | Imagen Cost | Total |
|-------------|--------|-------------|-------------|-------|
| 15s         | ~10    | ~$4         | ~$1         | ~$5   |
| 30s         | ~20    | ~$8         | ~$2         | ~$10  |
| 60s         | ~30    | ~$12        | ~$3         | ~$15  |
| 120s        | ~40    | ~$16        | ~$4         | ~$20  |

Costs assume Gemini 3 Pro Image at ~$0.40/image and 1-2 analysis iterations per group.

## Limitations

1. **Long videos**: Videos over 2 minutes may exceed Lambda's 15-minute timeout
2. **Moving objects**: Object removal only works for static elements visible across all frames in a group
3. **Resolution**: Gemini may output frames at a different resolution than the original (~1024px typical)
4. **Localized edits**: Imagen 3 edits only apply to the representative frame; spatial changes don't propagate via LUT
5. **Motion blur**: If the representative frame has momentary motion blur, the enhancement quality for that group may be reduced
6. **Cost**: AI enhancement is significantly more expensive than ffmpeg-only processing

## Processing Time Estimates

| Video Length | Est. Time | Breakdown |
|-------------|-----------|-----------|
| 15s         | ~3 min    | Extract: 3s, Group: 1s, Enhance: 2.5min, Reassemble: 5s |
| 30s         | ~5 min    | Extract: 5s, Group: 2s, Enhance: 4min, Reassemble: 10s |
| 60s         | ~8 min    | Extract: 5s, Group: 3s, Enhance: 7min, Reassemble: 10s |
| 120s        | ~12 min   | Extract: 8s, Group: 5s, Enhance: 11min, Reassemble: 15s |

## Related Documents

- [DDR-032: Multi-Step Frame-Based Video Enhancement](design-decisions/DDR-032-multi-step-video-enhancement.md) — Design decision record
- [DDR-031: Multi-Step Photo Enhancement](design-decisions/DDR-031-multi-step-photo-enhancement.md) — Photo enhancement (reused by video pipeline)
- [DDR-027: Container Image Lambda](design-decisions/DDR-027-container-image-lambda-local-commands.md) — ffmpeg bundling in Lambda
- [DDR-018: Video Compression](design-decisions/DDR-018-video-compression-gemini3.md) — Video compression for Gemini
