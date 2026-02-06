# DDR-024: Full-Image Preview and Filename Tooltip in Triage Web UI

**Date**: 2026-02-06  
**Status**: Accepted  
**Iteration**: 14

## Context

The triage web UI (DDR-022) displays a grid of thumbnail images (max 400px) for each media file categorized as keep or discard. Users rely on these thumbnails to verify the AI's decisions before confirming deletions.

Two usability gaps emerged during real-world use:

1. **Thumbnails are too small to verify content.** At 160px grid cells with 400px thumbnails, users cannot confidently judge whether a photo is truly blurry, accidental, or worth keeping. There is no way to view the full-resolution image for closer inspection.

2. **Filenames are truncated on small screens.** The filename label uses `text-overflow: ellipsis`, so long filenames (common with phone cameras, e.g., `IMG_20260205_142356_HDR.jpg`) are cut off. Users have no way to see the complete filename to cross-reference with their file system.

Both issues increase the risk of accidental deletions — the most consequential action in the triage flow.

## Decision

### 1. Full-Image Serving Endpoint

Add a new backend endpoint `GET /api/media/full?path=...` that serves the original file directly via `http.ServeFile`. Unlike the thumbnail endpoint (which downscales to 400px), this serves the raw file at full resolution. The browser handles rendering at native resolution in a new tab.

### 2. Click-to-Preview on Thumbnail

Make the thumbnail image area clickable. Clicking the image opens the full-resolution file in a new browser tab via `window.open()`. For cards in the "Discard" section (where clicking the card toggles selection), the image click uses `stopPropagation()` to avoid interfering with the selection toggle.

### 3. Native Tooltip on Filename

Add `title={item.filename}` to the filename `<div>` in `MediaCard`. The browser renders a native tooltip on hover showing the full, untruncated filename. No custom tooltip component is needed — the native `title` attribute is sufficient and requires zero additional code.

## Rationale

### Why `http.ServeFile` instead of another thumbnail size?

The goal is verification, not browsing. Users want to see exactly what the file looks like at full fidelity. Generating a larger thumbnail (e.g., 1600px) would add complexity and still not match the original. `http.ServeFile` handles range requests, MIME detection, and caching headers automatically.

### Why a new tab instead of a modal/lightbox?

A new tab is the simplest UX for "view the original file":
- No overlay state to manage in the SPA
- Browser-native zoom/pan controls for large images
- Works for both images and videos (browser plays videos natively)
- Does not interfere with the selection workflow in the original tab
- Zero additional frontend dependencies or components

### Why native `title` instead of a custom tooltip?

- Zero JavaScript overhead
- Consistent with OS-level tooltip behavior
- Works on every browser without polyfills
- The filename is short text — no rich formatting needed

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Lightbox/modal overlay | Adds UI state management complexity, requires building zoom/pan controls, blocks the main triage workflow |
| Larger thumbnails (e.g., 1600px) | Still not full resolution, doubles thumbnail generation time and cache size, doesn't help for videos |
| Custom tooltip component | Over-engineered for displaying a single line of text, adds bundle size |
| Download link instead of new tab | Worse UX — user wants to glance, not save a file |

## Consequences

**Positive:**

- Users can verify full-resolution images before confirming deletions, reducing accidental data loss
- Truncated filenames are always accessible via hover, improving usability on small screens
- Minimal code change — one new Go handler, a few lines of frontend changes
- No new dependencies (frontend or backend)
- Works for both images and videos (browser renders/plays natively)

**Trade-offs:**

- Full-resolution images served directly from disk — no size limit protection. Very large files (e.g., 50MB RAW) will take time to load in the browser. This is acceptable since the user is deliberately choosing to inspect a specific file.
- The `/api/media/full` endpoint exposes file serving from arbitrary paths on the local filesystem. This is the same security model as the existing `/api/media/thumbnail` endpoint and is acceptable for Phase 1 (localhost-only use). Phase 2 (remote hosting) will require authentication and path restriction.

## Related Documents

- [DDR-022: Web UI with Preact SPA and Go JSON API](./DDR-022-web-ui-preact-spa.md)
- [DDR-021: Media Triage Command](./DDR-021-media-triage-command.md)
- [DDR-014: Thumbnail-Based Multi-Image Selection Strategy](./DDR-014-thumbnail-selection-strategy.md)
