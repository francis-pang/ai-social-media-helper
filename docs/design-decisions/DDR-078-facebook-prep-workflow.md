# DDR-078: Facebook Prep Workflow — Session-Aware Captions with Google Maps Grounding

**Date**: 2026-03-01  
**Status**: Accepted  
**Iteration**: Cloud — Facebook content preparation

## Context

The Instagram caption pipeline (DDR-036) generates one caption per carousel group, where all media in a group share a single description. Facebook has no automation API — the Graph API `publish_actions` permission is deprecated, `POST /me/photos` returns a deprecation error, the `/feed` endpoint is read-only, and browser automation (Selenium/Puppeteer) violates TOS and breaks with every UI change. The only viable path is a "Facebook Prep" assistant that generates per-item captions, location suggestions, and date/time stamps for manual copy-paste during upload. Photos in a session are narrative-related (e.g., "a day in Seattle" progressing from Pike Place Market to the Space Needle to a waterfront dinner), so each caption must be unique yet part of a coherent story — the existing description-lambda approach of generating one caption per group does not apply, and generating captions independently per item produces repetitive, disconnected results.

## Decision

### 1. Session-Aware Batch Processing
All items in a session are sent to Gemini in a single API call (following the triage batch pattern). Gemini sees all photos/videos chronologically to produce non-repetitive captions that tell a coherent narrative. Output is a JSON array with one object per item containing `index`, `filename`, `caption`, and `locationName`.

### 2. Google Maps Grounding
Enable the `GoogleMaps` tool in the Gemini request with `ToolConfig.RetrievalConfig.LatLng` set to the session's representative GPS centroid. This provides verified location names from Google Maps' 250M+ places database rather than relying on Gemini's training data. The response includes `GroundingMetadata.GroundingChunks[].Maps` with `PlaceID`, `Title`, and `URI` for each location.

### 3. Caption-Grade Video Compression
Videos are compressed at 1 FPS, 768px, CRF 40, no audio (more aggressive than DDR-018's triage profile of 5 FPS, CRF 35). A single thumbnail cannot represent a video's content — a 15-second walking tour looks nothing like one frozen frame. At 1 FPS, a 30-second video costs ~2,100 tokens at MediaResolutionLow.

### 4. Per-Item Feedback Without Full Regeneration
When the user provides feedback on one caption, only that item's thumbnail + metadata is re-sent to Gemini, along with the existing captions for sibling items (text only, no thumbnails). This preserves the user's accepted captions while maintaining narrative coherence.

### 5. Two-Phase Batching for Large Sessions (>20 items)
Phase 1: Send all thumbnails + metadata to Gemini for a ~200-word session narrative summary. Phase 2: Split into batches of 20, each receiving the Phase 1 summary + full metadata for all items, generating captions only for the current batch. This ensures cross-batch narrative coherence.

### 6. EXIF Dates Displayed Directly
Date/time values come from EXIF metadata extracted by media-process-lambda and are shown in the UI without Gemini involvement. This prevents date hallucination — Gemini is not asked to generate date/time fields.

### 7. Chronological Ordering
Items are sorted by EXIF date/time before sending to Gemini. The prompt presents them in temporal order so Gemini understands the narrative arc (arrival, exploration, highlights, departure). Captions can reference temporal progression naturally.

### 8. MediaResolutionLow for Token Efficiency
Uses `genai.MediaResolutionLow` (280 tokens/image). Caption writing requires scene recognition (Pike Place Market, Space Needle), not pixel-level detail. For a batch of 20 photos: ~5,600 image tokens — well within context limits.

## Rationale

Session-aware batch processing is the key architectural decision. The alternative — generating captions independently per item — produces repetitive, disconnected captions because each call has no context about what other captions say. A single batch call costs the same as N individual calls but produces dramatically better narrative coherence.

Google Maps grounding replaces the DDR-009 approach of embedding GPS in the prompt text and hoping Gemini recognizes the location. With Maps grounding, location names are verified against Google's place database and include machine-readable Place IDs and Maps URIs.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Per-item independent Gemini calls | No narrative coherence; captions repeat same phrases; more expensive (N calls vs 1) |
| Google Places API for locations | Extra API integration; Gemini's GoogleMaps tool handles it natively via the SDK |
| Full-resolution photos to Gemini | 10x token cost; 400px thumbnails are sufficient for scene recognition |
| Single video thumbnail instead of compressed video | Loses video context entirely; a 15-second clip cannot be summarized from one frame |
| Gemini generates date/time fields | Prone to hallucination; EXIF data is authoritative |
| No batching limit | Gemini context window and response quality degrade with too many images in one call |

## Consequences

**Positive:**
- Coherent, non-repetitive captions across all items in a session
- Verified location names from Google Maps (250M+ places) with Place IDs and Maps URIs
- Efficient token usage: 280 tokens/image at MediaResolutionLow
- Per-item feedback preserves accepted captions
- Economy Mode eligible (DDR-077 Batch API, 50% cost reduction)

**Trade-offs:**
- Single-call architecture means one failure retries the entire batch
- >20 items requires two-phase processing (additional API call for session summary)
- Google Maps grounding uses a single representative GPS centroid per session, not per-item coordinates
- Per-item feedback still requires sending sibling caption text for context

## Related Documents

- [DDR-077](./DDR-077-cost-aware-vertex-ai-migration.md) — Cost-Aware Vertex AI Migration (Vertex AI backend, Economy Mode)
- [DDR-036](./DDR-036-ai-post-description.md) — AI Post Description Generation (Instagram caption approach)
- [DDR-071](./DDR-071-photo-downscaling-for-gemini.md) — Photo Downscaling and Media Resolution Strategy
- [DDR-018](./DDR-018-video-compression-gemini3.md) — Video Compression for Gemini (base video compression profile)
- [DDR-061](./DDR-061-s3-event-driven-per-file-processing.md) — S3 Event-Driven Per-File Processing (media-process-lambda reuse)
- [DDR-009](./DDR-009-gemini-reverse-geocoding.md) — Gemini Reverse Geocoding (superseded approach for location identification)