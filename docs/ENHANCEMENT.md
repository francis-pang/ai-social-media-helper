# Multi-Step Photo Enhancement Pipeline

**DDR-031** | Steps 4 & 5 of the Media Selection Flow

## Overview

The enhancement pipeline takes photos selected by the AI (Steps 2-3) and enhances them to professional quality through a multi-step process combining two complementary AI models:

- **Gemini 3 Pro Image** — Global creative enhancement (color, lighting, exposure, composition)
- **Imagen 3** — Localized surgical edits (object removal, background cleanup, inpainting)

## Enhancement Pipeline

Each photo goes through up to three automated phases:

```
Original Photo
    │
    ▼
┌─────────────────────────────────────────┐
│ Phase 1: Gemini 3 Pro Image             │
│ Global enhancement — color correction,  │
│ lighting, exposure, sharpness, contrast │
└────────────────────┬────────────────────┘
                     │
                     ▼
┌─────────────────────────────────────────┐
│ Phase 2: Gemini 3 Pro (Text Analysis)   │
│ "What else needs fixing?"              │
│ Returns structured JSON with:           │
│   - Professional quality score          │
│   - Remaining improvements needed       │
│   - Whether each needs surgical edit    │
└────────────────────┬────────────────────┘
                     │
        ┌────────────┴────────────┐
        │                         │
        ▼                         ▼
┌───────────────────┐  ┌──────────────────────┐
│ Global remaining  │  │ Phase 3: Imagen 3    │
│ → Second Gemini   │  │ Surgical mask-based  │
│   pass            │  │ edits (up to 3x)     │
└───────┬───────────┘  └──────────┬───────────┘
        │                         │
        └────────────┬────────────┘
                     │
                     ▼
              Enhanced Photo
```

### Phase 1: Global Enhancement

Gemini 3 Pro Image receives the original photo with a natural language instruction to apply all necessary improvements. This handles:

- Exposure and lighting optimization
- Color correction and white balance
- Contrast and vibrancy adjustment
- Sharpening and noise reduction
- Composition improvement (straightening, minor cropping)
- Portrait enhancements (natural skin, brightened eyes)
- Scene-specific adjustments (food warmth, sky vibrancy, etc.)

### Phase 2: Professional Quality Analysis

After Phase 1, the enhanced photo is sent to Gemini 3 Pro (text-only) for a structured analysis. The response includes:

```json
{
  "overallAssessment": "Good exposure and color. Minor distractions remain.",
  "remainingImprovements": [
    {
      "type": "object-removal",
      "description": "Trash can in bottom-right corner",
      "region": "bottom-right",
      "impact": "high",
      "imagenSuitable": true,
      "editInstruction": "Remove the trash can, fill with matching ground texture"
    }
  ],
  "professionalScore": 7.5,
  "targetScore": 9.0,
  "noFurtherEditsNeeded": false
}
```

- If `professionalScore >= 8.5`, the photo is considered publication-ready and Phase 3 is skipped.
- Improvements marked `imagenSuitable: true` are routed to Imagen 3 (Phase 3).
- Non-Imagen improvements (global adjustments) get a second Gemini pass.

### Phase 3: Surgical Edits (Imagen 3)

For localized improvements (object removal, background cleanup, blemish removal), the pipeline:

1. Generates a mask image based on the region description
2. Sends the image + mask + instruction to Imagen 3
3. Applies up to 3 iterations per photo

Imagen 3 requires Vertex AI infrastructure (see [Infrastructure Requirements](#infrastructure-requirements)).

## User Feedback Loop

After automatic enhancement, users can review results and request changes:

```
User: "Make the sky more blue"
    │
    ▼
┌───────────────────────────────────┐
│ Try Gemini 3 Pro Image first      │
│ (handles natural language well)   │
└──────────────┬────────────────────┘
               │
          Success? ───── Yes ──→ Done
               │
               No
               │
               ▼
┌───────────────────────────────────┐
│ Analyze gap with Gemini 3 Pro     │
│ Determine what localized edit     │
│ is needed                         │
└──────────────┬────────────────────┘
               │
               ▼
┌───────────────────────────────────┐
│ Fall back to Imagen 3             │
│ Apply surgical edits with masks   │
└──────────────┬────────────────────┘
               │
               ▼
           Return to User
```

Feedback preserves conversation history for multi-turn context, so instructions like "now make it even brighter" or "undo that and try something different" work naturally.

## API Endpoints

### POST /api/enhance/start

Start an enhancement job for selected photos.

**Request:**
```json
{
  "sessionId": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "keys": [
    "a1b2c3d4-e5f6-7890-abcd-ef1234567890/IMG_001.jpg",
    "a1b2c3d4-e5f6-7890-abcd-ef1234567890/IMG_002.jpg"
  ]
}
```

**Response (202 Accepted):**
```json
{
  "id": "enh-abc123..."
}
```

### GET /api/enhance/{id}/results?sessionId=...

Poll for enhancement results. Items appear incrementally as each photo completes.

**Response:**
```json
{
  "id": "enh-abc123...",
  "status": "processing",
  "totalCount": 10,
  "completedCount": 4,
  "items": [
    {
      "key": "session/IMG_001.jpg",
      "filename": "IMG_001.jpg",
      "phase": "complete",
      "originalKey": "session/IMG_001.jpg",
      "enhancedKey": "session/enhanced/IMG_001.jpg",
      "originalThumbKey": "session/thumbnails/IMG_001.jpg",
      "enhancedThumbKey": "session/thumbnails/enhanced-IMG_001.jpg",
      "phase1Text": "Applied exposure correction, boosted vibrancy...",
      "analysis": {
        "overallAssessment": "Professional quality achieved",
        "professionalScore": 8.8,
        "targetScore": 9.2,
        "noFurtherEditsNeeded": true,
        "remainingImprovements": []
      },
      "imagenEdits": 0,
      "feedbackHistory": []
    }
  ]
}
```

### POST /api/enhance/{id}/feedback

Submit feedback for a specific photo.

**Request:**
```json
{
  "sessionId": "a1b2c3d4-...",
  "key": "session/IMG_001.jpg",
  "feedback": "Make the sky more blue and remove the trash can on the right"
}
```

**Response (202 Accepted):**
```json
{
  "status": "processing"
}
```

Poll `/results` after ~5 seconds to see the updated photo.

## Frontend UI

### Step 4: Enhancement Processing

While enhancement runs, the UI shows:
- Overall progress bar (e.g., "4 of 10 photos")
- Per-photo status indicators showing current phase
- Cancel button to abort

### Step 5: Review Enhanced

After enhancement completes:
- **Photo grid** with thumbnails, phase badges, and quality scores
- **Side-by-side comparison** for selected photo (original vs enhanced)
- **Enhancement details** showing what was changed
- **Analysis details** showing remaining improvements and their impact
- **Feedback history** showing previous feedback rounds
- **Feedback input** for requesting additional changes

## File Structure

```
internal/
  chat/
    enhancement.go       # Multi-step pipeline orchestration
    gemini_image.go      # Gemini 3 Pro Image REST API client
    imagen.go            # Imagen 3 Vertex AI REST API client
  assets/
    prompts/
      enhancement-system.txt    # Phase 1 system instruction
      enhancement-analysis.txt  # Phase 2 analysis prompt

cmd/
  media-lambda/
    main.go              # Enhanced with /api/enhance/* endpoints

web/frontend/src/
  components/
    EnhancementView.tsx  # Steps 4 & 5 UI component
  api/
    client.ts            # Enhanced with enhancement API functions
  types/
    api.ts               # Enhanced with enhancement types
```

## Infrastructure Requirements

### Gemini 3 Pro Image (Phase 1, Feedback)

- **API**: `generativelanguage.googleapis.com/v1beta`
- **Model**: `gemini-3-pro-image-preview`
- **Authentication**: Existing Gemini API key (same as triage/selection)
- **No additional infrastructure needed**

### Imagen 3 (Phase 3, Feedback Fallback)

- **API**: Vertex AI (`{region}-aiplatform.googleapis.com/v1`)
- **Model**: `imagen-3.0-capability-001`
- **Requirements**:
  - GCP project with Vertex AI API enabled
  - Service account with `roles/aiplatform.user`
  - Service account key in SSM Parameter Store: `/ai-social-media/prod/vertex-ai-service-account`
  - Environment variables: `VERTEX_AI_PROJECT`, `VERTEX_AI_REGION`, `VERTEX_AI_TOKEN`

If Vertex AI is not configured, Phase 3 is gracefully skipped and the pipeline completes with Phase 1+2 results only.

## Cost Estimates

Per photo:
- Phase 1 (Gemini 3 Pro Image): ~$0.40-1.00
- Phase 2 (Gemini 3 Pro text analysis): ~$0.01-0.05
- Phase 3 (Imagen 3, if applicable): ~$0.02-0.04 per edit
- Feedback round (Gemini): ~$0.40-1.00
- Feedback round (Imagen fallback): ~$0.02-0.04

For a typical session of 25 photos: ~$10-25 for initial enhancement, plus ~$1-2 per feedback round.

## Related Documents

- [DDR-031](./design-decisions/DDR-031-multi-step-photo-enhancement.md) — Architecture Decision Record
- [DDR-030](./design-decisions/DDR-030-cloud-selection-backend.md) — Cloud Selection Backend (Steps 2 & 3)
- [ARCHITECTURE.md](./ARCHITECTURE.md) — Overall system architecture
