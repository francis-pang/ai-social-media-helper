# DDR-016: Quality-Agnostic Metadata-Driven Photo Selection

**Date**: 2025-12-31  
**Status**: Accepted  
**Iteration**: 10

## Context

Iteration 8 introduced thumbnail-based photo selection where Gemini evaluates visual quality, composition, and metadata to select up to 20 photos for an Instagram carousel. The original criteria prioritized visual quality as a key selection factor.

However, the user has access to Google's comprehensive photo enhancement suite, which can fix most quality issues post-selection:

| Tool | Capability |
|------|------------|
| Google Magic Editor | AI-powered comprehensive editing |
| Google Photo Reimagine | Creative transformations |
| Google Photos Help Me Edit | Natural language editing commands |
| Google Photo Artistic Templates | Style transfers and artistic effects |
| Photo Unblur | Sharpening and deblurring |
| Google Magic Eraser | Object removal |
| Google Portrait Light | Relighting portraits |
| Google Best Take | Combining best expressions from multiple shots |
| Face Retouch | Facial enhancement |
| Auto-Enhance / Dynamic | Automatic improvements |
| Portrait Blur | Background blur effects |
| Sky & Color Pop | Sky replacement and color enhancement |

Given these capabilities, photo quality should not be a primary selection criterion. Instead, selection should focus on **scene representation, subject diversity, and narrative completeness**.

## Decision

Redefine "most representative" photo selection to be **quality-agnostic and metadata-driven**:

1. **Subject/Scene Diversity (Highest Priority)**: Different subjects (food, architecture, landscape, people, activities)
2. **Scene Representation**: Ensure each sub-event/location is represented using hybrid scene detection
3. **Enhancement Potential (For Duplicates Only)**: When choosing between similar photos, pick the one requiring least enhancement
4. **People Variety (Lower Priority)**: Different groups/individuals
5. **Time of Day (Tiebreaker Only)**: Use only to break ties between otherwise equal photos

**Quality Threshold (Minimal)**: Only exclude photos that are completely unusable (extremely blurry, corrupt, accidental shots) and cannot be enhanced to Instagram quality even with Google's tools.

**Scene Detection (Hybrid)**: Combine visual similarity + time gaps (2+ hours) + location gaps (1km+) + reverse geocoding for venue names.

**User Context**: Require brief trip description to help Gemini understand sub-events and priorities.

**Output Format**: Three-part structured output:
1. Ranked list with justification
2. Scene grouping explanation
3. Detailed exclusion report for every non-selected photo

## Rationale

1. **Quality is Fixable**: Google's enhancement tools can address exposure, blur, composition, and lighting issues
2. **Scene Representation is Not Fixable**: If a key moment wasn't photographed, no tool can recreate it
3. **Metadata Provides Context**: GPS coordinates and timestamps enable intelligent scene clustering
4. **User Context Improves Accuracy**: Brief description helps identify which scenes matter most
5. **Transparency**: Detailed exclusion report explains every decision

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Quality-first selection | Excludes salvageable photos of important moments |
| Visual-only scene detection | Misses temporal/spatial relationships (same venue, different times) |
| Automatic clustering without context | Cannot prioritize user's key moments |
| Simple ranked list output | Lacks transparency on exclusions and scene structure |

## Consequences

**Positive:**
- Prioritizes narrative completeness over technical quality
- Leverages metadata for intelligent scene grouping
- Provides full transparency with exclusion reports
- User context enables personalized selection
- Allows more photos to be considered (lower quality threshold)

**Trade-offs:**
- Requires user to provide trip context (additional input)
- Reverse geocoding adds API calls and latency
- More complex prompt engineering for structured output
- May include photos that need significant enhancement

## Implementation Details

### Selection Criteria Weights

| Criterion | Weight | Role |
|-----------|--------|------|
| Subject/Scene Diversity | Highest | Primary selection driver |
| Scene Representation | High | Ensure coverage of all sub-events |
| Enhancement Potential | Medium | Tiebreaker for duplicates |
| People Variety | Lower | Secondary consideration |
| Time of Day | Lowest | Final tiebreaker only |

### Scene Detection Thresholds

| Signal | Threshold | Purpose |
|--------|-----------|---------|
| Visual similarity | AI judgment | Same backgrounds, subjects indicate same scene |
| Time gaps | 2+ hours | Longer gaps suggest new sub-event |
| Location gaps | 1km+ | Distance suggests new venue/location |
| Reverse geocoding | N/A | Provides venue/landmark names |

### Quality Exclusion Criteria

Only exclude if:
- Completely unusable (extremely blurry, corrupt, black/white frame)
- Accidental shots (pocket shots, ground shots, motion blur with no subject)
- Cannot be enhanced even with Google's tools

### Output Structure

```
1. RANKED LIST
   RANK | PHOTO | SCENE | JUSTIFICATION
   -----|-------|-------|---------------
   1    | filename.jpg | Scene Name | Reason for selection

2. SCENE GROUPING
   Scene 1: Name (GPS: Venue, Time Range)
   - Selected photos with brief description

3. EXCLUSION REPORT
   | Photo | Reason |
   |-------|--------|
   | filename.jpg | Specific exclusion reason |
```

## Related Documents

- [DDR-014: Thumbnail-Based Multi-Image Selection Strategy](DDR-014-thumbnail-selection-strategy.md)
- [DDR-009: Gemini Native Reverse Geocoding](DDR-009-gemini-reverse-geocoding.md)
- [DDR-013: Unified Metadata Architecture](DDR-013-unified-metadata-architecture.md)

