# DDR-031: Multi-Step Photo Enhancement Pipeline

**Date**: 2026-02-08  
**Status**: Accepted  
**Iteration**: Steps 4 & 5 of Media Selection Flow

## Context

After AI selection (Steps 2-3, DDR-030), selected photos need enhancement before publication. The enhancement step requires a multi-pass approach because no single AI model handles all enhancement scenarios optimally:

1. **Global creative edits** (color correction, lighting, exposure, white balance, composition) are best handled by Gemini 3 Pro Image, which understands natural language instructions and can perform broad creative adjustments.
2. **Localized surgical edits** (object removal, background replacement, inpainting, outpainting) are best handled by Imagen 3's mask-based editing, which excels at precise region-specific modifications.
3. **Analysis and gap identification** requires Gemini 3 Pro's reasoning capability to evaluate the current state of an image and determine what further enhancements would bring it to professional quality.

Key constraints:
- The current SDK (`github.com/google/generative-ai-go v0.19.0`) does not support image output from Gemini — Gemini 3 Pro Image editing requires direct REST API calls
- Imagen 3 mask-based editing requires Vertex AI (GCP project, service account) — also via REST API
- Multi-turn feedback loops need persistent conversation state
- Lambda has a 30-second API Gateway timeout — enhancement must run asynchronously
- Each photo enhancement may take 10-30+ seconds across multiple passes

## Decision

### 1. Three-Phase Enhancement Pipeline

Each photo goes through up to three phases of automated enhancement:

```
Phase 1: Gemini 3 Pro Image — Global Enhancement
    ↓ (enhanced image)
Phase 2: Gemini 3 Pro (Text) — Professional Quality Analysis
    ↓ (structured JSON: remaining improvements)
Phase 3: Imagen 3 Mask-Based — Localized Surgical Edits
    ↓ (may iterate multiple times)
    → Final Enhanced Image
```

**Phase 1 — Gemini 3 Pro Image: Global Enhancement**

Send the original photo with an enhancement instruction to `gemini-3-pro-image-preview`. The model returns an edited image with global improvements:
- Color correction and white balance
- Exposure and lighting optimization
- Contrast and saturation adjustment
- Sharpening and noise reduction
- Composition improvement (straightening, minor cropping)

This uses the Gemini REST API (`generateContent` with `responseModalities: ["TEXT", "IMAGE"]`), avoiding the SDK migration.

**Phase 2 — Gemini 3 Pro: Professional Quality Analysis**

Send the Phase 1 result to Gemini 3 Pro (text-only analysis) asking: "What further enhancements would bring this photo to professional quality?" The response is structured JSON:

```json
{
  "overallAssessment": "Good exposure, strong composition. Minor distractions.",
  "remainingImprovements": [
    {
      "type": "object-removal",
      "description": "Trash can in bottom-right corner",
      "region": "bottom-right",
      "impact": "high",
      "imagenSuitable": true
    },
    {
      "type": "color-grading",
      "description": "Slight blue cast in shadows",
      "region": "global",
      "impact": "medium",
      "imagenSuitable": false
    }
  ],
  "professionalScore": 7.5,
  "targetScore": 9.0
}
```

Each improvement is tagged with `imagenSuitable: true/false` to determine whether it needs Imagen 3 (localized mask-based edit) or another Gemini pass (global adjustment).

**Phase 3 — Imagen 3: Localized Surgical Edits**

For each `imagenSuitable: true` improvement from Phase 2:
1. Generate a mask image based on the region description (using programmatic mask generation from region coordinates)
2. Send to Imagen 3's `editImage` endpoint with the mask and instruction
3. Iterate: re-analyze, re-edit if quality target not met

Non-Imagen improvements (global adjustments remaining after Phase 1) are batched and sent back to Gemini 3 Pro Image for a second pass.

### 2. Feedback Session Strategy

When the user provides feedback (e.g., "make the sky more blue", "remove the person on the left"):

```
User Feedback
    ↓
Try Gemini 3 Pro Image first (global or creative edit)
    ↓ (success? → done)
    ↓ (insufficient? → continue)
Analyze gap with Gemini 3 Pro (text)
    ↓
Apply Imagen 3 iterations for localized fixes
    ↓
Return result to user
```

The feedback is always attempted with Gemini 3 Pro Image first because:
- It handles natural language instructions directly
- It can make both global and semi-localized changes
- It's faster (single API call vs mask generation + Imagen API)
- Most user feedback is expressible as global/creative instructions

If Gemini 3 Pro Image can't fully address the feedback (detected by re-analysis), the system falls back to Imagen 3 for precise surgical edits.

### 3. SDK Approach: REST API (SDK-C)

Both Gemini 3 Pro Image and Imagen 3 are accessed via direct REST API calls, keeping the existing `github.com/google/generative-ai-go` SDK unchanged for selection and triage:

**Gemini 3 Pro Image REST endpoint:**
```
POST https://generativelanguage.googleapis.com/v1beta/models/gemini-3-pro-image-preview:generateContent?key={API_KEY}
```

**Imagen 3 REST endpoint (Vertex AI):**
```
POST https://{region}-aiplatform.googleapis.com/v1/projects/{project}/locations/{region}/publishers/google/models/imagen-3.0-capability-001:predict
```

Benefits:
- Zero changes to existing SDK usage
- Surgical addition of new capabilities
- Both APIs are well-documented REST endpoints
- Can be migrated to SDK calls later when full SDK migration happens

### 4. Enhancement State Persistence

Enhancement state is stored in-memory (consistent with existing triage/selection patterns), structured for future DynamoDB migration:

```go
type enhancementJob struct {
    id            string
    sessionID     string
    status        string
    items         []enhancementItem
    totalCount    int
    completedCount int
}

type enhancementItem struct {
    key              string      // S3 key of original
    filename         string
    phase            string      // "phase1", "phase2", "phase3", "feedback", "complete"
    originalKey      string      // S3 key of original
    enhancedKey      string      // S3 key of current enhanced version
    originalThumbKey string
    enhancedThumbKey string
    analysis         *analysisResult  // Phase 2 analysis
    feedbackHistory  []feedbackEntry  // Multi-turn feedback
    error            string
}
```

Each item tracks its current phase, allowing the frontend to show incremental progress ("12 of 25 photos enhanced").

### 5. Conversation State for Multi-Turn Feedback

Gemini 3 Pro Image supports multi-turn conversations where each turn builds on the previous context. The enhancement state stores the conversation history:

```go
type feedbackEntry struct {
    role    string // "user" or "model"
    text    string // instruction or response text
    imageKey string // S3 key of the image at this point
}
```

When the user gives feedback, the full conversation history (including images) is sent to Gemini, enabling contextual understanding like "now make it even brighter" or "undo the last change and try a different approach."

## Rationale

### Why Gemini 3 Pro Image first (not Imagen 3)?

Gemini 3 Pro Image excels at understanding the *intent* behind enhancement — it can analyze a photo holistically and make coordinated adjustments across exposure, color, composition, and style simultaneously. Imagen 3 is surgical but cannot reason about what the photo *needs*.

Starting with Gemini ensures the broadest possible improvement in a single pass, leaving only specific localized issues (which Imagen excels at) for subsequent passes.

### Why multi-phase (not single-pass)?

No single model handles all enhancement scenarios:
- Gemini 3 Pro Image cannot do precise inpainting/outpainting (it may alter surrounding areas)
- Imagen 3 cannot do global color grading (it only edits masked regions)
- The combination covers both broad creative adjustments and surgical precision

### Why try Gemini first during feedback?

User feedback is typically expressible as natural language instructions ("make the sky bluer", "brighten the shadows"). Gemini handles these natively. Imagen 3 requires a mask, making it slower and more complex for simple requests. Only when Gemini can't satisfy the request do we escalate to Imagen 3.

### Why REST API instead of SDK migration?

The SDK migration (SDK-A) touches 5-7 files across the entire codebase and requires testing all existing triage/selection functionality. The enhancement feature is additive — using REST API adds the new capability without risking regression in working features. The REST API approach also works for Imagen 3, which requires Vertex AI (a different SDK entirely).

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Gemini Flash Image (P1) instead of Pro (P2) | Lower quality output; Pro provides better results for the first critical enhancement pass |
| Imagen 3 as primary enhancer | Cannot do global adjustments (color, exposure, lighting); requires masks for every edit |
| Single-pass Gemini only (no Imagen) | Misses surgical edits like object removal that Gemini handles imprecisely |
| Full SDK migration before enhancement | High risk of regression in working triage/selection; orthogonal to enhancement logic |
| Go image processing (P5) | Vastly lower quality; limited to basic brightness/contrast; no AI intelligence |

## Consequences

**Positive:**
- Professional-grade enhancement combining two complementary AI models
- Natural feedback loop: Gemini first (fast, natural language), Imagen fallback (precise, surgical)
- Multi-turn conversation preserves context across feedback iterations
- REST API approach adds capabilities without risking existing functionality
- Pipeline phases can run independently, enabling partial success
- Enhancement state structured for future DynamoDB migration

**Trade-offs:**
- Phase 3 (Imagen 3) requires Vertex AI GCP project setup and service account — not available until infrastructure is provisioned
- REST API has more boilerplate than SDK calls (manual JSON marshaling, auth handling)
- Multi-phase pipeline is slower than single-pass (10-30s per phase per photo)
- In-memory state is lost if Lambda container is recycled (consistent with triage/selection; DynamoDB migration planned)
- Gemini 3 Pro Image output resolution may not match input (~1024px typical output vs 4032px phone photos)

## Infrastructure Requirements

**Gemini 3 Pro Image (Phase 1, Feedback):**
- Uses existing Gemini API key (same as triage/selection)
- No additional infrastructure needed
- Model: `gemini-3-pro-image-preview`

**Imagen 3 (Phase 3, Feedback Fallback):**
- Requires GCP project with Vertex AI API enabled
- Requires service account with `roles/aiplatform.user` role
- Service account key stored in SSM Parameter Store: `/ai-social-media/prod/vertex-ai-service-account`
- Environment variables: `VERTEX_AI_PROJECT`, `VERTEX_AI_REGION`
- Model: `imagen-3.0-capability-001`

## Related Documents

- [DDR-016](./DDR-016-quality-agnostic-photo-selection.md) — Quality-Agnostic Photo Selection
- [DDR-030](./DDR-030-cloud-selection-backend.md) — Cloud Selection Backend (Steps 2 & 3)
- [Media Selection Feature Plan](../../.cursor/plans/media_selection_feature_update_141c5fac.plan.md) — Full feature plan
