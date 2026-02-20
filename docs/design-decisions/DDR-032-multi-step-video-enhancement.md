# DDR-032: Multi-Step Frame-Based Video Enhancement Pipeline

**Date**: 2026-02-08  
**Status**: Accepted  
**Iteration**: Steps 4 & 5 of Media Selection Flow (Video)

## Context

DDR-031 established the multi-step photo enhancement pipeline using Gemini 3 Pro Image (Phase 1), Gemini 3.1 Pro analysis (Phase 2), and Imagen 3 mask-based editing (Phase 3). However, no Google AI service can directly enhance an existing video — Gemini image editing works on still images only, and Veo 3.1 generates new videos rather than enhancing existing footage.

The plan document (Step 4) originally proposed two options for video: Gemini Analysis + FFmpeg (V1, limited to signal processing) or skipping video enhancement (V4). Both fall short of the quality achievable with AI image enhancement.

This DDR proposes a novel approach: **decompose the video into frames, enhance frames as images using the same Gemini 3 Pro Image + Imagen 3 pipeline from DDR-031, then reassemble into a video**. This achieves AI-quality visual enhancement for video by treating each frame as a photo.

Key constraints:
- Videos can have thousands of frames (30fps × 60s = 1,800 frames) — enhancing every frame individually is cost-prohibitive and slow
- Consecutive frames in video are highly similar — identical enhancement should apply to groups of similar frames for visual consistency
- Frame-by-frame enhancement must not introduce flickering or temporal inconsistencies
- Lambda has 15-minute timeout and 10GB `/tmp` — processing must fit within these limits
- Audio must be preserved from the original video
- The existing Gemini 3 Pro Image SDK client and Imagen 3 REST client (DDR-031) should be reused

## Decision

### 1. Five-Phase Frame-Based Video Enhancement Pipeline

Each video goes through five phases of enhancement:

```
Phase 1: Frame Extraction (ffmpeg)
    ↓ (individual JPEG frames)
Phase 2: Frame Grouping (color histogram similarity)
    ↓ (groups of similar frames)
Phase 3: Gemini 3 Pro Image — Representative Frame Enhancement
    ↓ (enhanced representative frame per group)
Phase 4: Gemini 3.1 Pro Analysis + Imagen 3 — Further Enhancement
    ↓ (professionally enhanced representative frames, iterated)
Phase 5: Frame Reassembly (ffmpeg)
    ↓ (final enhanced video with original audio)
```

**Phase 1 — Frame Extraction**

Extract all frames from the video using ffmpeg:

```
ffmpeg -i input.mp4 -qscale:v 2 -vsync 0 frames/frame_%06d.jpg
```

- `-qscale:v 2`: High-quality JPEG output (minimal compression artifacts)
- `-vsync 0`: Preserve exact frame timing (no duplication/dropping)
- Output: Individual JPEG frames named sequentially

For long videos (>60s) or high frame-rate videos, we extract at a reduced rate (e.g., 10fps) to keep frame count manageable, then interpolate during reassembly.

**Phase 2 — Frame Grouping via Color Histogram**

Group consecutive frames by visual similarity using normalized color histograms:

1. For each frame, compute a 3-channel (RGB) color histogram with 32 bins per channel
2. Normalize the histogram to account for exposure variations
3. Compare consecutive frames using histogram correlation (OpenCV-style `cv2.compareHist` with `HISTCMP_CORREL`)
4. Frames with correlation ≥ 0.92 are grouped together (same "scene segment")
5. Scene changes (correlation < 0.92) start a new group

This is implemented in pure Go using the standard `image` package — no external dependencies. The histogram comparison is O(N) per frame and negligible compared to AI enhancement time.

Each group selects a **representative frame** (the middle frame) for enhancement. The middle frame best represents the group's visual characteristics, avoiding transition artifacts at group boundaries.

**Phase 3 — Gemini 3 Pro Image Enhancement (per group)**

For each frame group's representative frame:

1. Send to Gemini 3 Pro Image with the enhancement system prompt (same as DDR-031 Phase 1)
2. Receive enhanced image
3. The enhancement instructions target the same priorities: exposure, color, contrast, sharpness, etc.
4. Store the enhanced representative frame

This reuses `GeminiImageClient.EditImage()` from `internal/chat/gemini_image.go`.

**Phase 4 — Gemini 3.1 Pro Analysis + Imagen 3 Iteration (per group)**

For each enhanced representative frame:

1. Send to Gemini 3.1 Pro (text) for professional quality analysis (same as DDR-031 Phase 2)
2. Parse structured JSON response with remaining improvements
3. For `imagenSuitable: true` improvements: apply Imagen 3 mask-based edits
4. For global improvements: send back to Gemini 3 Pro Image for another pass
5. Repeat until `professionalScore >= 8.5` or `noFurtherEditsNeeded: true` or max 3 iterations

This reuses `GeminiImageClient.AnalyzeImage()` and `ImagenClient.EditWithMask()` from DDR-031.

**Phase 5 — Frame Reassembly**

Apply the representative frame's enhancement to all frames in its group, then stitch back into video:

1. For each group, compute the color transformation between the original and enhanced representative frames
2. Apply that same transformation to all other frames in the group using ffmpeg's LUT (Look-Up Table) filter
3. Reassemble all enhanced frames into a video using ffmpeg:
   ```
   ffmpeg -framerate {original_fps} -i enhanced/frame_%06d.jpg -i original.mp4 -map 0:v -map 1:a -c:v libx264 -crf 18 -preset slow -c:a copy output.mp4
   ```
4. `-map 1:a -c:a copy`: Copies original audio stream without re-encoding
5. `-crf 18 -preset slow`: High-quality H.264 encoding for the output

The color transformation approach ensures temporal consistency — all frames in a group receive the exact same adjustment, eliminating flickering.

### 2. Color Transformation Propagation

The key challenge is applying the AI enhancement (designed for one frame) consistently across all frames in a group. We use a **3D color LUT** approach:

1. Compare the original representative frame and its enhanced version pixel-by-pixel
2. Build a 3D color mapping table: for each (R,G,B) input, compute the average (R',G',B') output
3. Apply this LUT to every frame in the group using ffmpeg's `lut3d` filter or Go's image processing

This ensures:
- Identical scenes get identical color grading
- No flickering between consecutive frames
- Smooth transitions at group boundaries (groups are already visually similar)

For localized edits (Imagen 3), we apply the edit only to the representative frame and use the surrounding unedited frames as context — localized edits like object removal only make sense for static objects that appear in every frame of the group, and the mask-based approach handles this naturally.

### 3. Lambda Architecture

Video enhancement requires significant compute and storage. The pipeline maps to Lambda as follows:

**Option A: Single Video Lambda (Recommended for v1)**

A single Lambda function (`cmd/video-lambda/main.go`) processes one video through all five phases:

- Memory: 4GB+ (more memory = more CPU in Lambda)
- Timeout: 15 minutes
- `/tmp` storage: up to 10GB (sufficient for extracted frames)
- Contains ffmpeg binary (bundled in Docker container image, per DDR-027)

For a 30-second video at 30fps (900 frames):
- Frame extraction: ~5s
- Histogram grouping: ~2s
- ~15-30 groups (typical for phone video)
- Gemini enhancement: ~15-30 groups × 10s = 150-300s (2.5-5 min)
- Imagen iteration: ~15-30 groups × 5s = 75-150s (1-2.5 min)
- Frame reassembly: ~10s
- Total: ~4-9 minutes (within 15-minute Lambda timeout)

**Option B: Multi-Lambda with Step Functions (Future)**

For videos longer than ~60 seconds, fan out enhancement to parallel Lambda invocations:
- Frame Extraction Lambda: extract frames, group them, upload groups to S3
- Enhancement Lambda (per group): enhance one group's representative frame
- Reassembly Lambda: download all enhanced frames, stitch video

This is the Step Functions Map state pattern from the plan document. Deferred to a future iteration since most phone videos are under 60 seconds.

### 4. Feedback Loop

When the user provides feedback on an enhanced video:

1. Re-extract frames from the current enhanced video
2. Re-group (groups may differ due to enhancement changes)
3. Apply user feedback as additional Gemini instruction (e.g., "make the colors warmer", "increase contrast")
4. Re-run Phase 3-5 with the feedback incorporated into the system prompt
5. Return new enhanced video

The feedback loop reuses the same pipeline, with the user's feedback appended to the enhancement instruction. Multi-turn conversation history is maintained per video in the enhancement state.

### 5. Frame Rate and Duration Limits

To keep processing within Lambda limits and cost-reasonable:

| Video Length | Extraction FPS | Max Frames | Est. Groups | Est. Time | Est. Cost |
|-------------|---------------|------------|-------------|-----------|-----------|
| 0-15s       | Original (30) | ~450       | ~10-15      | ~3 min    | ~$2-5     |
| 15-30s      | Original (30) | ~900       | ~15-30      | ~5 min    | ~$5-10    |
| 30-60s      | 15fps         | ~900       | ~20-40      | ~7 min    | ~$8-15    |
| 60-120s     | 10fps         | ~1,200     | ~30-50      | ~10 min   | ~$12-20   |
| >120s       | 5fps          | ~600/min   | Varies      | May exceed| High      |

Videos over 2 minutes are warned about in the UI (long processing time and high cost). Videos over 5 minutes are not recommended for frame-based enhancement.

### 6. Histogram Similarity Algorithm

The frame grouping uses normalized color histogram correlation, implemented in pure Go:

```go
// Compute histogram: 32 bins per RGB channel (32×32×32 = 32,768 bins)
func computeHistogram(img image.Image) [32][32][32]float64

// Compare two histograms using Pearson correlation coefficient
// Returns value in [-1, 1] where 1 = identical, 0 = uncorrelated, -1 = inverse
func compareHistograms(h1, h2 [32][32][32]float64) float64

// Group consecutive frames where correlation >= threshold
func groupFrames(frames []string, threshold float64) []FrameGroup
```

The 32-bin resolution provides enough granularity to detect scene changes while being robust to noise, motion blur, and minor lighting variations within a scene.

Threshold of 0.92 was chosen because:
- 0.95+: Too sensitive — splits on minor camera movement/lighting changes
- 0.90: Reasonable — groups continuous scenes together
- 0.85-: Too loose — may group different scenes with similar color palettes

## Rationale

### Why frame-by-frame enhancement instead of ffmpeg filters?

FFmpeg filters (brightness, contrast, saturation, color grading) are deterministic signal processing — they apply mathematical transformations without understanding the image content. Gemini 3 Pro Image understands what's in the frame (sky, faces, food, architecture) and applies context-aware enhancement. The difference is comparable to Instagram filters vs professional retouching.

### Why histogram grouping instead of enhancing every frame?

Enhancing every frame would be:
- **Prohibitively expensive**: 900 frames × $0.40/image = $360 per 30-second video
- **Temporally inconsistent**: Each Gemini call may produce slightly different results, causing flickering
- **Extremely slow**: 900 × 10s = 2.5 hours per video

Grouping reduces this to ~20 groups × $0.40 = $8, takes ~5 minutes, and ensures temporal consistency within each group.

### Why color histogram instead of perceptual hashing or neural embeddings?

- **Color histogram**: Pure Go, no dependencies, O(N) per frame, well-understood behavior, excellent for detecting scene changes in video
- **Perceptual hashing (pHash)**: Requires DCT computation, less sensitive to gradual color changes (which are common in video pans)
- **Neural embeddings**: Requires ML model, overkill for sequential frame comparison, adds dependency
- **Frame difference (MSE/SSIM)**: Too sensitive to camera shake and minor object movement

Color histograms are the standard approach in video segmentation and are well-suited for the goal of grouping visually similar consecutive frames.

### Why propagate via LUT instead of enhancing every frame individually?

The LUT approach:
- Guarantees temporal consistency (all frames in a group get the exact same color mapping)
- Is computationally trivial (lookup table, no AI needed per-frame)
- Preserves the AI's creative decisions exactly across the group
- Eliminates flickering risk entirely

### Why single Lambda instead of Step Functions for v1?

Most phone videos are under 60 seconds. A single Lambda with 15-minute timeout and 4GB memory can process these comfortably. Step Functions adds deployment complexity (state machine definition, IAM roles, multiple Lambda deployments) for a marginal benefit. The architecture is designed to decompose into Step Functions later if needed.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| FFmpeg-only enhancement (V1 from plan) | Signal processing only — no AI intelligence, vastly lower quality |
| Skip video enhancement (V4 from plan) | Inconsistent experience — photos enhanced but videos not |
| Enhance every frame individually | Cost-prohibitive ($360/30s video), slow (2.5 hours), temporal flickering |
| Optical flow for frame interpolation | Complex, error-prone, unnecessary when LUT propagation works |
| Neural style transfer | Wrong tool — style transfer changes artistic style, not professional quality |
| Veo 3.1 video generation | Cannot enhance existing video — only generates new content |
| Per-frame pHash grouping | Less sensitive to gradual color changes common in video pans |

## Consequences

**Positive:**
- Achieves AI-quality visual enhancement for video by leveraging the existing photo enhancement pipeline
- Temporal consistency guaranteed by LUT-based color propagation within groups
- Cost-effective: ~$5-15 per video instead of $360+ for per-frame enhancement
- Reuses all DDR-031 infrastructure (Gemini 3 Pro Image client, Imagen 3 client, prompts, analysis)
- Frame grouping is pure Go — no external dependencies beyond ffmpeg
- Original audio preserved exactly (no re-encoding)
- Feedback loop uses the same pipeline, extending naturally

**Trade-offs:**
- Quality limited by representative frame selection — if the representative frame has momentary motion blur, the whole group inherits that enhancement
- Localized edits (object removal) only effective for static elements visible across all frames in a group
- Long videos (>2 minutes) may exceed Lambda timeout or be cost-prohibitive
- LUT propagation cannot handle localized spatial edits (only global color mapping) — Imagen edits only apply to representative frame
- Frame extraction and reassembly add processing overhead (~15-20s total)
- Larger `/tmp` usage (~2-5GB for extracted frames from a 30-second video)

## Infrastructure Requirements

**Same as DDR-031, plus:**

- Lambda: 4GB+ memory, 15-minute timeout (existing video processing profile)
- `/tmp`: Up to 10GB (for extracted + enhanced frames)
- ffmpeg: Already bundled in Docker container image (DDR-027)
- No new AWS services or infrastructure required

## Related Documents

- [DDR-031](./DDR-031-multi-step-photo-enhancement.md) — Multi-Step Photo Enhancement Pipeline
- [DDR-027](./DDR-027-container-image-lambda-local-commands.md) — Container Image Lambda with Local Commands
- [DDR-018](./DDR-018-video-compression-gemini3.md) — Video Compression for Gemini 3
- [Media Selection Feature Plan](../../.cursor/plans/media_selection_feature_update_141c5fac.plan.md) — Full feature plan
