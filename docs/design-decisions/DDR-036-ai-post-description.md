# DDR-036: AI Post Description Generation with Full Media Context

**Date**: 2026-02-08  
**Status**: Accepted  
**Iteration**: Step 8 of Media Selection Flow

## Context

After the user has uploaded media (Step 1), selected the best items via AI (Steps 2-3), enhanced them (Steps 4-5), grouped them into posts (Step 6), and prepared downloads (Step 7), the final step is generating an Instagram caption for each post group.

The application already has CLI-level social media description generation (`BuildSocialMediaPrompt()` in `internal/chat/chat.go`) that analyzes individual media files using the `social-media-image.txt` and `social-media-video.txt` prompt templates. However, these prompts are designed for **single-file analysis** — they generate descriptions for one photo or one video at a time and include platform-specific recommendations (LinkedIn, Twitter, YouTube Shorts) that are irrelevant for this use case.

The new description generation must handle:

1. **Multi-media context** — A post group contains 1-20 photos and videos. The caption should describe the collection holistically, not each item individually.
2. **Post group label as primary context** — In Step 6, the user wrote a descriptive label for each group (e.g., "First morning in Shibuya — the energy of the crossing, coffee at Blue Bottle, found a great vintage shop on Cat Street"). This label is the most important input for caption generation.
3. **Trip context** — The overall trip/event description entered in Step 1 (e.g., "3-day trip to Tokyo, Oct 2025") provides additional background.
4. **Iterative feedback** — The user must be able to request changes ("make it shorter", "more casual", "add more hashtags") and have Gemini regenerate with conversation context preserved.
5. **Consistent brand voice** — All generated captions should follow Francis's persona as a budding social media influencer with a vlog-style, authentic tone.

The existing per-file prompts (`social-media-image.txt`, `social-media-video.txt`) remain useful for the CLI `media-select` command and are not modified.

## Decision

### 1. Full Media Context with Gemini

Send the actual media (thumbnails for photos, compressed videos) to Gemini along with structured context. This produces the highest-quality captions because Gemini can reference specific visual details visible in the media (e.g., "that golden hour glow over the Shibuya crossing").

**API call structure:**
- **System instruction**: `description-system.txt` — contains Francis's persona, Instagram account style, caption structure rules
- **Media parts**: Thumbnails (inline blobs) + compressed videos (Files API) for all items in the post group
- **Text prompt**: Post group label + trip context + metadata (GPS, timestamps, scene names from selection) + user feedback (if regenerating)

### 2. Description System Prompt (`description-system.txt`)

A new prompt template containing constant information about the caption generation style:

- Francis's persona and background (budding social media influencer)
- Instagram account objective (build following via travel/lifestyle content)
- Tone: casual, enthusiastic, relatable — like talking to a friend
- Caption structure: hook line -> story/context -> call to action -> hashtags
- Hashtag strategy: mix of location-specific, activity, trending, and niche (15-20 hashtags)
- Emoji usage: moderate, natural, integrated into text
- Output format: structured JSON with `caption`, `hashtags[]`, and `locationTag`

### 3. Multi-Turn Feedback via Conversation History

Feedback loops use Gemini's multi-turn conversation capability:

1. **Initial generation**: System instruction + media + prompt -> Gemini returns caption JSON
2. **Feedback round 1**: User says "make it shorter" -> Previous messages + new user message -> Gemini returns updated caption
3. **Feedback round N**: Continues with full history

Conversation history is stored in-memory per description job (same pattern as enhancement feedback in DDR-031). Each entry records:
- User feedback text
- Model response (generated caption)

### 4. Post Group Label as Primary Context

The group label entered in Step 6 is passed as the primary user context in the prompt. This is the most important signal for caption generation because:
- It describes what the group is about in the user's own words
- It may include specific details, moods, or themes the user wants highlighted
- DDR-033 intentionally designed the label as a `<textarea>` to encourage rich descriptions

The label is combined with the trip context and media metadata to form the full prompt.

### 5. Backend Implementation

New file `internal/chat/description.go` with:

- `DescriptionResult` struct: `Caption`, `Hashtags[]`, `LocationTag`
- `GenerateDescription()`: Sends media + context to Gemini, returns structured caption
- `RegenerateDescription()`: Multi-turn feedback using conversation history
- `BuildDescriptionPrompt()`: Constructs the user prompt from group label, trip context, and media metadata

New API endpoints in `cmd/media-lambda/main.go`:

| Method | Path | Action |
|--------|------|--------|
| `POST` | `/api/description/generate` | Generate initial caption for a post group |
| `GET` | `/api/description/{id}/results` | Poll for generation results |
| `POST` | `/api/description/{id}/feedback` | Regenerate with user feedback |

The generation runs inline within the API Lambda (no Step Functions needed) because:
- Sending thumbnails + metadata to Gemini is fast (typically 5-15 seconds)
- No heavy media processing involved
- API Gateway's 30-second timeout is sufficient
- Avoids Step Functions overhead for a simple text generation task

### 6. Frontend Implementation

New component `DescriptionEditor.tsx` with:

- Displays generated caption in an editable `<textarea>`
- Shows hashtags as editable tags
- Shows location tag
- "Regenerate" button with feedback text input
- "Copy to Clipboard" button (for download flow where user posts manually)
- "Accept & Continue" button to proceed to the next group
- Navigation back to publish/download step

The component processes one post group at a time, proceeding through groups sequentially.

### 7. Model Selection

Uses `gemini-3-flash-preview` (the default model) for description generation. Flash is ideal because:
- Text generation is not as demanding as image editing
- Faster response times for interactive feedback loops
- Lower cost per request
- Sufficient quality for caption writing

## Rationale

### Why full media context instead of text-only?

Text-only captions (using just metadata and scene names) produce generic descriptions. With actual media, Gemini can:
- Reference specific visual details ("the misty morning temple gate")
- Identify food, landmarks, activities in the photos
- Note the mood and atmosphere from the visual content
- Describe what Francis is doing in the photos
- Create more engaging, specific hooks

The cost difference is minimal (~$0.05 for thumbnails vs ~$0.01 for text-only), and the quality improvement is substantial.

### Why inline API processing instead of Step Functions?

Description generation is a single Gemini API call with thumbnail-sized media. Unlike enhancement (which processes N files in parallel) or selection (which processes all session files), description operates on pre-generated thumbnails for at most 20 items. The total payload is small (~600KB for 20 thumbnails) and Gemini responds within 5-15 seconds — well within the API Gateway 30-second timeout.

### Why a new prompt template instead of adapting existing ones?

The existing `social-media-image.txt` and `social-media-video.txt` prompts are designed for single-file analysis with reverse geocoding, temporal analysis, platform-specific recommendations, and three caption variations (casual/professional/inspirational). The multi-media carousel caption needs a fundamentally different prompt structure — one caption for the whole group, no platform-specific variants, and group-level context rather than per-file analysis.

### Why store conversation history in-memory?

The enhancement feature (DDR-031) already stores feedback history in-memory per job. For a personal project with single-user sessions, in-memory storage is sufficient. DynamoDB persistence (DDR-035) will store the final accepted caption, not the intermediate conversation history.

### Why JSON output format for the caption?

Structured JSON output (`{"caption": "...", "hashtags": [...], "locationTag": "..."}`) allows the frontend to:
- Display hashtags as individual editable tags (not embedded in caption text)
- Show the location tag separately with a map icon
- Allow users to add/remove individual hashtags without editing the full caption
- Copy just the caption vs caption+hashtags separately

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Text-only metadata (no media) | Captions are generic — cannot reference visual details; users expect AI to "see" the photos |
| Reuse existing social-media-image/video prompts | Designed for single-file analysis; wrong structure for multi-media carousel captions |
| Step Functions for description generation | Over-engineered — single Gemini call completes in 5-15 seconds, well within API timeout |
| Generate descriptions for individual files, then combine | Loses holistic narrative; produces disjointed captions; more expensive (N API calls vs 1) |
| Client-side Gemini API call (no backend) | Exposes API key to browser; no server-side validation; can't use Francis reference photo |
| GPT-4 / Claude for description | Additional API key management; Gemini already integrated; same Gemini key for all features |
| Pre-built Instagram caption templates (fill-in-the-blank) | Too rigid; cannot adapt to the specific visual content; defeats the purpose of AI generation |

## Consequences

**Positive:**
- Captions reference actual visual content — significantly more engaging than text-only
- Group label provides rich user-directed context — captions align with user's intent
- Multi-turn feedback allows iterative refinement until the user is satisfied
- Consistent brand voice across all posts (persona defined in system prompt)
- Structured JSON output enables rich frontend editing experience
- Inline processing keeps the architecture simple (no Step Functions, no async polling needed for initial generation)
- Reuses existing patterns: Gemini client, thumbnail generation, inline blobs, conversation history

**Trade-offs:**
- Sending media increases API cost (~$0.05 per generation vs ~$0.01 for text-only)
- Video compression needed if group contains videos (adds processing time)
- In-memory conversation history lost if Lambda container recycles mid-session (acceptable for v1)
- Francis reference photo adds ~30KB per request (negligible)
- 30-second API Gateway timeout limits the number of media items per group to ~20 (matches Instagram carousel limit anyway)

## Implementation

### Changes to Application Repo

| File | Changes |
|------|---------|
| `internal/chat/description.go` | New: `GenerateDescription()`, `RegenerateDescription()`, `BuildDescriptionPrompt()`, `DescriptionResult` type |
| `internal/assets/prompts/description-system.txt` | New: System prompt with Francis's persona, caption style, JSON output format |
| `internal/assets/prompts.go` | Add `DescriptionSystemPrompt` embedded variable |
| `cmd/media-lambda/main.go` | Add `/api/description/generate`, `/api/description/{id}/results`, `/api/description/{id}/feedback` endpoints; register routes |
| `web/frontend/src/components/DescriptionEditor.tsx` | New: Caption display, editing, feedback, copy-to-clipboard, accept |
| `web/frontend/src/api/client.ts` | Add `generateDescription()`, `getDescriptionResults()`, `submitDescriptionFeedback()` |
| `web/frontend/src/types/api.ts` | Add `DescriptionGenerateRequest`, `DescriptionResults`, `DescriptionFeedbackRequest` types |
| `web/frontend/src/app.tsx` | Wire `DescriptionEditor` component to the `"description"` step |
| `docs/design-decisions/index.md` | Add DDR-036 entry |
| `docs/architecture.md` | Move Step 8 from "Planned" to implemented; add endpoint table |

## Related Documents

- [DDR-019](./DDR-019-externalized-prompt-templates.md) — Externalized Prompt Templates (pattern for new prompt file)
- [DDR-030](./DDR-030-cloud-selection-backend.md) — Cloud Selection Backend Architecture (media-to-Gemini pattern)
- [DDR-031](./DDR-031-multi-step-photo-enhancement.md) — Multi-Step Photo Enhancement Pipeline (feedback loop pattern)
- [DDR-033](./DDR-033-post-grouping-ui.md) — Post Grouping UI (group label as caption context)
- [DDR-034](./DDR-034-download-zip-bundling.md) — Download ZIP Bundling (preceding step in the flow)
- [DDR-035](./DDR-035-multi-lambda-deployment.md) — Multi-Lambda Deployment Architecture (API Lambda handles description endpoint)
- [Media Selection Feature Plan](../../.cursor/plans/media_selection_feature_update_141c5fac.plan.md) — Full feature plan with Step 8 details
