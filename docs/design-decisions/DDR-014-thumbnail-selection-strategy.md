# DDR-014: Thumbnail-Based Multi-Image Selection Strategy

**Date**: 2025-12-31  
**Status**: Accepted  
**Iteration**: 8

## Context

Iteration 8 introduces directory-level photo processing, where a directory may contain many images (potentially 50+). The user needs to select up to 20 representative photos for an Instagram post. Uploading all full-resolution images would be:
- Expensive in API tokens
- Slow due to upload time
- Unnecessary for selection tasks (Gemini doesn't need 4K resolution to evaluate composition)

## Decision

Use a two-phase approach for multi-image selection:

1. **Iteration 8 (Selection)**: Upload low-resolution thumbnails (max 1024px) with extracted metadata to Gemini
2. **Iteration 10 (Refinement)**: Define "most representative" criteria with time diversity, location clustering, visual deduplication, and quality signals

For Iteration 8, Gemini returns a ranked list with justification using its own judgment for "representative."

## Rationale

1. **Token Efficiency**: Thumbnails use ~10-20x fewer tokens than full-resolution images
2. **Metadata Compensation**: GPS coordinates, timestamps, and camera info provide context that thumbnails may lack
3. **Selection ≠ Analysis**: Choosing representative photos requires understanding composition and variety, not pixel-level detail
4. **Gemini's Vision Capabilities**: Works effectively at lower resolutions for classification/selection tasks

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Upload all full-resolution images | Expensive, slow, unnecessary for selection |
| Local pre-filtering (heuristics) | Misses visual content—can't evaluate composition |
| Random sampling | Doesn't guarantee representative selection |
| File metadata only (no thumbnails) | Can't assess visual content or composition |

## Thumbnail Generation Strategy

| Format | Approach |
|--------|----------|
| JPEG, PNG | Resize using `golang.org/x/image/draw` (pure Go, fast) |
| HEIC | Use macOS `sips` tool to convert to JPEG thumbnail |
| GIF, WebP | Send original (typically small files) |

**Target dimensions**: 1024px maximum on longest edge, maintaining aspect ratio.

## Consequences

**Positive:**
- Significant reduction in API costs for large directories
- Faster processing due to smaller upload sizes
- Metadata ensures accurate location/time context despite lower resolution
- Enables processing 50+ images in a single API call

**Trade-offs:**
- HEIC requires external tool (`sips`) on macOS—not cross-platform
- Very fine details may be lost in thumbnails (acceptable for selection)
- Two-phase approach adds complexity for full analysis (future iteration)

## Iteration 10: Quality-Agnostic Photo Selection

**Status**: Implemented - See [DDR-016](DDR-016-quality-agnostic-photo-selection.md)

The definition of "most representative" was refined in Iteration 10 with a **quality-agnostic approach**:

### Key Changes from Original Plan

| Original Criteria | Iteration 10 Criteria |
|-------------------|----------------------|
| Quality signals (focus, exposure, composition) | Quality is NOT a criterion (user has Google enhancement tools) |
| Time-based diversity | Time of day as tiebreaker only |
| Location clustering | Hybrid scene detection (visual + time 2hr+ + GPS 1km+) |
| Perceptual hashing | Gemini-based visual deduplication |
| Configurable weighting | Fixed priority: Subject > Scene > Enhancement Potential > People > Time |

### Selection Priorities (Finalized)

1. **Subject/Scene Diversity** (Highest): Different subjects (food, architecture, landscape, people, activities)
2. **Scene Representation**: Ensure each sub-event/location is represented
3. **Enhancement Potential** (Duplicates Only): Pick photo requiring least enhancement
4. **People Variety** (Lower): Different groups/individuals
5. **Time of Day** (Tiebreaker): Only to break ties

### Quality Threshold

Only exclude photos that are:
- Completely unusable (extremely blurry, corrupt, accidental)
- Cannot be enhanced to Instagram quality even with Google's tools

### User Context

Trip description is now required/recommended to help Gemini understand:
- Sub-events to look for
- Priority subjects
- Narrative structure

### Output Format

Three-part structured output:
1. Ranked list with justification
2. Scene grouping explanation
3. Detailed exclusion report for every non-selected photo

## Related Documents

- [DDR-012: Files API for All Uploads](DDR-012-files-api-for-all-uploads.md)
- [DDR-013: Unified Metadata Architecture](DDR-013-unified-metadata-architecture.md)
- [DDR-016: Quality-Agnostic Photo Selection](DDR-016-quality-agnostic-photo-selection.md)
- [docs/media_analysis.md](../media_analysis.md)
