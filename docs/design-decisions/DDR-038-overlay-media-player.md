# DDR-038: Overlay Media Player for Inline Previews

**Date**: 2026-02-08  
**Status**: Accepted  
**Iteration**: Media viewing UX

## Context

Multiple steps in the selection flow display media thumbnails (Steps 3, 5, 6, 7). When a user clicks a thumbnail to view the full-resolution media, the current implementation calls `openFullImage()` which opens the media in a new browser tab via `window.open()`.

This has several UX problems:

1. **Context loss**: Opening a new tab loses the user's place in the workflow. They must switch back to the original tab manually.
2. **No video playback**: The new-tab approach opens a raw URL — videos display as a download link in some browsers or play in the browser's minimal built-in player with no controls context.
3. **No quick dismiss**: Users cannot quickly glance at a photo and dismiss it — they must close or switch tabs.
4. **Inconsistent with modern apps**: Photo/video apps (Instagram, Google Photos, Lightroom) all use overlay/modal viewers for inline media preview.

## Decision

Replace the "open in new tab" behavior with a **full-screen overlay media player** that renders on top of the current page. The overlay handles both photos and videos, and retains an "Open in New Tab" button for users who want the original behavior.

### 1. Component Architecture

A single global `<MediaPlayer />` component is rendered at the root of `app.tsx`. Any component can trigger it by calling `openMediaPlayer(key, type, filename)`. This avoids duplicating overlay markup across multiple components.

**State management**: Module-level Preact signals control the overlay's visibility and content:

- `isOpen` — whether the overlay is visible
- `mediaKey` — S3 key (or local path) of the media to display
- `mediaType` — `"Photo"` or `"Video"` to determine which HTML element to render
- `mediaFilename` — display filename for the header bar
- `resolvedUrl` — the resolved full-resolution URL (fetched asynchronously in cloud mode)
- `loading` — whether the URL is being resolved

### 2. Overlay Design

| Element | Behavior |
|---------|----------|
| **Backdrop** | Semi-transparent dark overlay (`rgba(0, 0, 0, 0.85)`). Clicking the backdrop closes the overlay. |
| **Header bar** | Fixed at the top. Shows the filename (monospace) and two buttons: "Open in New Tab" and "Close" (×). |
| **Media content** | Centered. Photos render as `<img>` with `object-fit: contain`, capped at `90vw × 80vh`. Videos render as `<video>` with native controls and autoplay. |
| **Keyboard shortcut** | Pressing `Escape` closes the overlay. A subtle hint at the bottom reminds the user. |
| **Body scroll lock** | When the overlay is open, `document.body.style.overflow = "hidden"` prevents background scrolling. |

### 3. URL Resolution

In cloud mode, full-resolution media URLs are presigned S3 URLs that must be fetched from the backend (`GET /api/media/full?key=...`). A new `getFullMediaUrl()` API helper resolves this asynchronously, showing a loading state in the overlay while the URL is fetched. In local mode, the URL is constructed synchronously.

### 4. Integration Points

The overlay replaces `openFullImage()` calls in these components:

| Component | Step | Change |
|-----------|------|--------|
| `SelectionView.tsx` | Review Selection (3) | Thumbnail `onClick` → `openMediaPlayer(key, type, filename)` |
| `EnhancementView.tsx` | Review Enhanced (5) | Original/enhanced image `onClick` → `openMediaPlayer(key, "Photo", filename)` |
| `TriageView.tsx` | Triage Results | Thumbnail `onClick` → `openMediaPlayer(key, type, filename)` with type inferred from file extension |

The `openFullImage()` function in `api/client.ts` is retained for the "Open in New Tab" button inside the overlay.

### 5. Media Type Detection

Components that have an explicit `type` field (e.g., `SelectionItem.type`) pass it directly. For `TriageView`, which lacks a type field, a helper function `isVideoFile(filename)` checks the file extension against known video formats (`.mp4`, `.mov`, `.avi`, `.mkv`, `.webm`).

## Rationale

- **Inline viewing is faster**: Users can view and dismiss media in under a second without losing their workflow context. This is critical when reviewing 20-50+ thumbnails in Steps 3 and 5.
- **Unified video playback**: The HTML5 `<video>` element provides consistent, controllable playback with native browser controls (play/pause, volume, scrubbing, fullscreen).
- **Global component**: A single overlay instance avoids duplicating modal markup in every component. Signal-based state makes it trivially callable from anywhere.
- **"Open in New Tab" preserved**: Users who want to inspect media in full resolution outside the app flow can still do so — no functionality is removed.
- **Escape key + click-outside**: Standard modal dismissal patterns that users expect from overlay UIs.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Keep "open in new tab" as primary action | Poor UX — context loss, no inline preview, bad video handling |
| Third-party lightbox library (e.g., PhotoSwipe, Fancybox) | Unnecessary dependency for what is achievable with ~120 lines of Preact + CSS-in-JS |
| Per-component inline expansion (expand thumbnail in place) | Disrupts the grid layout; complex to implement per component; doesn't work well for videos |
| `<dialog>` element | Good accessibility defaults but styling is inconsistent across browsers; requires polyfill considerations; CSS-in-JS overlay is simpler for this dark-themed app |

## Consequences

**Positive:**

- Users can quickly preview full-resolution photos and videos without leaving the page.
- Videos play inline with native controls, volume, and fullscreen support.
- A single reusable component serves all steps — no code duplication.
- "Open in New Tab" is still available for users who need it.
- Keyboard-accessible (Escape to close).

**Trade-offs:**

- Overlay media is still limited to the browser viewport size — users cannot zoom/pan on large photos (they can use "Open in New Tab" for that).
- Video streaming depends on presigned S3 URL validity (1-hour expiry) — long-idle overlays may fail to play if the URL expires.
- Body scroll lock is a simple `overflow: hidden` approach — on iOS Safari this may not fully prevent background scroll (acceptable given Chrome-on-macOS target).

## Related Documents

- [DDR-024](./DDR-024-full-image-preview-tooltip.md) — Original full-image preview approach (triage UI)
- [DDR-030](./DDR-030-cloud-selection-backend.md) — Selection backend (provides thumbnail URLs)
- [DDR-031](./DDR-031-multi-step-photo-enhancement.md) — Enhancement pipeline (side-by-side comparison)
- [DDR-037](./DDR-037-step-navigation-and-state-invalidation.md) — Step navigation (steps 3, 5 use thumbnails)
