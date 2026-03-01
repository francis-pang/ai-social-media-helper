# Image Processing

Technical documentation for image handling: format support, metadata extraction, thumbnail generation, and AI enhancement.

## Supported Formats

| Extension | MIME Type | Common Source |
|-----------|-----------|---------------|
| `.jpg`, `.jpeg` | `image/jpeg` | Most cameras, web |
| `.png` | `image/png` | Screenshots, graphics |
| `.gif` | `image/gif` | Animated images |
| `.webp` | `image/webp` | Modern web format |
| `.heic` | `image/heic` | iPhone (iOS 11+) |
| `.heif` | `image/heif` | HEIC variant |

HEIC/HEIF support uses a pure Go library (`evanoberholster/imagemeta`) — no external tools needed. See [DDR-010](./design-decisions/DDR-010-heic-format-support.md).

## Metadata Extraction

EXIF metadata is extracted locally using a Go library and passed as text alongside the image to Gemini. The AI cannot reliably parse binary EXIF headers, so preprocessing is required.

```mermaid
flowchart LR
    File["Image file\n(HEIC/JPEG/PNG)"]
    MIME["MIME detection\n(extension-based)"]
    EXIF["EXIF extraction\n(evanoberholster/imagemeta)"]
    GPS["GPS coordinates\n(lat/lon)"]
    Geocode["Reverse geocoding\n(Gemini's Google Maps)"]
    Context["Metadata context\n(formatted text for prompt)"]

    File --> MIME --> EXIF --> GPS --> Geocode --> Context
    EXIF --> Context
```

**Extracted fields:**
- GPS coordinates (latitude, longitude) with Google Maps link
- Date/time taken (timezone-aware)
- Camera make and model
- Formatted as structured text appended to the Gemini prompt

**Reverse geocoding** uses Gemini's native Google Maps integration — no separate API key needed. The AI resolves GPS coordinates to place names, street addresses, and landmarks. See [DDR-009](./design-decisions/DDR-009-gemini-reverse-geocoding.md).

**Error handling:** If EXIF extraction fails (corrupted metadata, unsupported format), the pipeline continues without metadata. A warning is logged but processing is not blocked.

## Thumbnail Generation

Thumbnails are generated for two purposes:

| Context | Size | Purpose |
|---------|------|---------|
| Selection UI | 400px | Display in web UI grids; cached in S3 |
| Gemini API | 1024px | Sent to Gemini for AI analysis (in-memory) |

In cloud mode, 400px thumbnails are pre-generated during the selection step and stored in S3 at `{sessionId}/thumbnails/{filename}.jpg`. All downstream steps (review, enhancement, grouping) serve these cached thumbnails directly. See [DDR-014](./design-decisions/DDR-014-thumbnail-selection-strategy.md).

## Gemini Photo Downscaling (DDR-071)

Large photos (>2000px longest edge) are downscaled to 1920px and converted to **WebP** during the MediaProcess Lambda step. 1920px matches the highest resolution Instagram Stories/Reels and TikTok full-screen portrait actually display. This reduces file size by ~94% (7 MB iPhone JPEG → ~400 KB WebP) without affecting Gemini token cost, which is fixed by the `media_resolution` parameter.

| Environment | Output Format | Method |
|-------------|---------------|--------|
| Lambda (ffmpeg) | WebP quality 85 | ffmpeg + libwebp |
| CLI (no ffmpeg) | JPEG quality 85 | Pure Go fallback |

Processed photos are stored at `{sessionId}/processed/{baseName}.webp`. The `converted` flag is set on the FileResult, showing a green "CONVERTED" badge in the upload UI.

Photos already ≤2000px are used as-is. HEIC/HEIF photos are always converted (to WebP or JPEG) regardless of size. GIF and WebP inputs are skipped.

The `media_resolution` parameter controls how much detail Gemini uses per image:

| Pipeline | `media_resolution` | Tokens/Image | Rationale |
|----------|--------------------|--------------|-----------|
| Triage | LOW | 280 | Keep/reject — single tile, no splitting |
| Selection | HIGH | 1120 | Instagram/TikTok quality assessment |

## Enhancement Pipeline

The photo enhancement pipeline applies AI models in three automated phases to bring photos to professional quality. See [DDR-031](./design-decisions/DDR-031-multi-step-photo-enhancement.md) for the full design decision.

```mermaid
sequenceDiagram
    participant Original as Original Photo
    participant Gemini as Gemini 3 Pro Image
    participant Analysis as Gemini 3.1 Pro (Text)
    participant Imagen as Imagen 3 (Vertex AI)
    participant Enhanced as Enhanced Photo

    Original->>Gemini: Phase 1 — Global enhancement
    Note over Gemini: Color, lighting, exposure,<br/>contrast, sharpness, composition
    Gemini->>Analysis: Phase 2 — Quality analysis
    Note over Analysis: Returns JSON: score,<br/>remaining improvements,<br/>Imagen suitability flags
    alt Score >= 8.5
        Analysis->>Enhanced: Publication-ready, skip Phase 3
    else Needs localized edits
        Analysis->>Imagen: Phase 3 — Surgical edits (mask-based)
        Note over Imagen: Object removal, background cleanup,<br/>blemish removal (up to 3 iterations)
        Imagen->>Enhanced: Enhanced photo
    else Needs global adjustments
        Analysis->>Gemini: Second Gemini pass
        Gemini->>Enhanced: Enhanced photo
    end
```

**User feedback loop:** After automatic enhancement, users can request changes ("make the sky more blue", "remove the trash can"). Feedback is sent to Gemini first; if the result is insufficient, it falls back to Imagen 3 for surgical edits. Multi-turn conversation history is preserved.

**API endpoints:**

| Method | Path | Action |
|--------|------|--------|
| `POST` | `/api/enhance/start` | Start enhancement for selected photos |
| `GET` | `/api/enhance/{id}/results` | Poll enhancement progress and results |
| `POST` | `/api/enhance/{id}/feedback` | Re-enhance a photo with user feedback |

**Infrastructure:** All AI operations use `ai.NewAIClient(ctx)` with dual-backend support (Vertex AI primary, Gemini API fallback) per DDR-077. Imagen 3 requires Vertex AI; if Vertex AI is not configured, Phase 3 is skipped gracefully.

## Related DDRs

- [DDR-007](./design-decisions/DDR-007-hybrid-exif-prompt.md) — Hybrid EXIF prompt strategy
- [DDR-008](./design-decisions/DDR-008-pure-go-exif-library.md) — Pure Go EXIF library selection
- [DDR-009](./design-decisions/DDR-009-gemini-reverse-geocoding.md) — Gemini native reverse geocoding
- [DDR-010](./design-decisions/DDR-010-heic-format-support.md) — HEIC/HEIF format support
- [DDR-013](./design-decisions/DDR-013-unified-metadata-architecture.md) — Unified metadata architecture
- [DDR-014](./design-decisions/DDR-014-thumbnail-selection-strategy.md) — Thumbnail selection strategy
- [DDR-031](./design-decisions/DDR-031-multi-step-photo-enhancement.md) — Multi-step photo enhancement pipeline
- [DDR-071](./design-decisions/DDR-071-photo-downscaling-for-gemini.md) — Photo downscaling and media resolution strategy
- [DDR-077](./design-decisions/DDR-077-cost-aware-vertex-ai-migration.md) — Cost-Aware Vertex AI Migration

---

**Last Updated**: 2026-03-01
