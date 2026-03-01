# DDR-073: Light Theme UI Redesign with Purple Accent

**Date**: 2026-02-28  
**Status**: Implemented  
**Iteration**: Visual design system overhaul

## Context

The web frontend used a dark color scheme (near-black backgrounds, light text, blue primary accent) established organically during development. While functional, this presented several issues:

1. **Readability on bright monitors** — at the target 1440p/1m viewing distance (DDR-057), dark-on-dark card boundaries were hard to distinguish, especially in ambient-lit rooms.
2. **Thumbnail contrast** — dark/underexposed photos (which are a primary triage target) blended into the dark UI background, making it harder to evaluate whether a photo was genuinely too dark or just looked dark against the dark UI.
3. **Modern expectations** — most productivity tools (Google Photos, Lightroom Web, CloudConvert) default to light themes. The dark theme felt inconsistent with the content-review workflow where users need maximum media visibility.
4. **Limited layout vocabulary** — the existing UI used single-column layouts with no sidebar panels, no streaming log console, no step pipeline indicators, and no masonry grids. The triage results view showed uniform grids with minimal AI reasoning context, making it hard for users to understand *why* the AI flagged each item.

## Decision

Redesign the entire web frontend from dark to light theme with a purple accent (`#7c3aed`), and introduce new layout patterns to improve information density and AI transparency.

### 1. Color System

Replace all `:root` CSS variables from dark values to light:

| Variable | Old (dark) | New (light) |
|----------|-----------|-------------|
| `--color-bg` | `#0f1117` | `#f8f9fc` |
| `--color-surface` | `#1a1d27` | `#ffffff` |
| `--color-surface-hover` | `#242836` | `#f3f4f6` |
| `--color-border` | `#2e3348` | `#e2e5ef` |
| `--color-text` | `#e4e6f0` | `#1a1d2e` |
| `--color-text-secondary` | `#8b8fa8` | `#6b7280` |
| `--color-primary` | `#6c8cff` (blue) | `#7c3aed` (purple) |
| `--color-primary-hover` | `#5a7aee` | `#6d28d9` |
| `--color-danger` | `#ff6b6b` | `#ef4444` |
| `--color-danger-hover` | `#ee5a5a` | `#dc2626` |
| `--color-success` | `#51cf66` | `#22c55e` |

New variables added: `--color-surface-alt` (#f1f3f9), `--color-primary-light` (rgba(124, 58, 237, 0.08)), `--color-warning` (#f59e0b), `--color-shadow` (rgba(0, 0, 0, 0.06)).

Cards gain `box-shadow` for depth instead of relying on border contrast. Outline buttons use purple border/text.

### 2. New Layout Patterns

| Class | Purpose |
|-------|---------|
| `.layout-sidebar` | 2-column grid (1fr + 320px) for main content + right sidebar |
| `.sidebar-panel` | White card with shadow for sidebar sections, auto-styled `h3` headers |
| `.drop-zone` | Dashed-border container for file upload, with hover state (purple border + tint) |
| `.log-console` | Scrollable monospace container for streaming log entries |
| `.step-pipeline` | Horizontal step indicator with connecting lines (done/active/pending states) |
| `.masonry-grid` | CSS columns layout for variable-height triage result cards |
| `.reason-filters` + `.reason-pill` | Filter pill row for triage reason categories |
| `.sticky-action-bar` | Sticky bottom bar for batch actions |
| `.progress-top-bar` | Thin fixed-top progress indicator |

### 3. Component Changes

**App shell** (`app.tsx`):
- Header → proper `<nav>` with brand icon, breadcrumb, nav links, purple outline Sign Out
- Triage flow steps wrapped in `.layout-sidebar` for sidebar support
- Footer with version string and system status

**FileUploader** (`FileUploader.tsx`):
- Empty state: drop zone with Tips & Guidance sidebar
- In-flight state: thumbnail grid with Pipeline Summary + Batch Statistics sidebar
- Replaces flat `.file-row` list with grid cards with status dot indicators

**ProcessingIndicator** (`ProcessingIndicator.tsx`):
- AI Analysis Dashboard with 3-stage step pipeline (Upload → Video Processing → AI Evaluation)
- Collapsible streaming logs console with synthetic log entries from state changes
- Job Telemetry + Resource Usage sidebar panels

**TriageView + TriageMediaCard** (`TriageView.tsx`, `TriageMediaCard.tsx`):
- Reason filter pills for category filtering (Blurry, Dark, Accidental, Duplicate)
- Masonry grid for variable-height discard cards
- Larger card format with AI reasoning text prominently displayed, reason badges, Review link
- Collapsible keep section with smaller thumbnails
- Sticky multi-button action bar (Back, Delete Selected, Confirm & Archive)

**MediaReviewModal** (`MediaReviewModal.tsx` — new component):
- Full-screen charcoal backdrop (`#1a1a2e`) — intentional dark exception for media visibility
- AI Vision toggle with exposure slider (CSS filter, non-destructive)
- Floating AI reasoning card with confidence score and expandable metadata
- Thumbnail queue strip with keyboard navigation

**Selection flow** (StepNavigator, LandingPage, MediaUploader, SelectionView, etc.):
- Step navigator: purple for current step, green for completed, gray for future
- All hardcoded dark-theme rgba values replaced with new-palette equivalents
- Drop-zone pattern applied to MediaUploader

**MediaPlayer** (`MediaPlayer.tsx`):
- Text colors in dark overlay changed to hardcoded `#ffffff` (same pattern as MediaReviewModal — dark overlay requires light text regardless of theme)

### 4. Documentation

`ui-guidelines.md` rewritten to document the new light color palette, all new CSS classes, layout patterns, component conventions, and an extended new-component checklist.

### 5. Multi-Agent Execution

Implementation used a 4-wave multi-agent strategy to parallelize work across independent files:

| Wave | Agents | Files | Gate |
|------|--------|-------|------|
| 1 | 1 (sequential) | `style.css` | Build passes, variables finalized |
| 2 | 4 (parallel) | `app.tsx`, `FileUploader`, `ProcessingIndicator`, `MediaReviewModal` | All render independently |
| 3 | 3 (parallel) | `TriageView`+`TriageMediaCard`, `StepNavigator`+`LandingPage`+`MediaUploader`, `SelectionView`+12 remaining files | Full flows render |
| 4 | 2 (parallel) | `ui-guidelines.md`, hardcoded-color sweep | Zero old hex values remain |

Conflict avoidance: one agent per file per wave, CSS variables frozen after Wave 1, new components in new files.

## Rationale

- **CSS variable architecture preserved** — all 20+ components inherit the theme via `:root` variables. Changing ~15 variable values in one file cascaded the light theme everywhere. Only intentional dark overlays (MediaPlayer, MediaReviewModal) required hardcoded overrides.
- **Purple accent** over blue — distinguishes interactive elements (buttons, badges, links) more clearly against the light background. Blue accent on white can be confused with default browser link styles.
- **Charcoal modal exception** — dark/underexposed media (a primary triage target) becomes invisible against a white modal background. The charcoal backdrop ensures all media content is evaluable regardless of exposure level.
- **Masonry over uniform grid** — triage cards contain variable-length AI reasoning text (1-3 sentences). A uniform grid wastes space below short-reason cards. CSS columns layout adapts naturally.
- **Streaming logs** — the ProcessingIndicator previously showed only a spinner. Users had no visibility into what the backend was doing during the 30-120 second Gemini analysis. Synthetic log entries derived from status polling make the process transparent without requiring backend WebSocket changes.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Dark/light toggle (user preference) | Doubles the CSS surface area and testing matrix; the light theme is a deliberate design choice, not a preference |
| Tailwind CSS for new utilities | Adds a build dependency and learning curve; the existing CSS variable + utility class pattern is sufficient |
| Separate `light-theme.css` overlay | CSS specificity conflicts; cleaner to update `:root` values directly since the variable names are unchanged |
| Real-time WebSocket logs | Requires backend changes (WebSocket endpoint, log forwarding); synthetic logs from polling are sufficient for v1 and can be upgraded later |
| Side panel for AI reasoning in modal | Wastes horizontal space on wide monitors; a floating card overlay is less intrusive and can be dismissed |

## Consequences

**Positive:**
- Media content (especially dark/underexposed photos) is easier to evaluate against a light background
- AI reasoning is prominently visible in triage results — users understand *why* each item was flagged
- New layout patterns (sidebar, masonry, step pipeline) support denser information display
- Multi-agent execution parallelized the implementation across 10 agents in 4 waves
- Zero dark-theme hex values remain in components (verified by automated sweep)

**Trade-offs:**
- MediaPlayer and MediaReviewModal use hardcoded dark colors, creating two exceptions to the CSS variable convention
- `style.css` grew from ~284 to ~541 lines with the new utility classes
- Streaming logs are synthetic (derived from state polling), not real server-side logs — may show less granular detail than users expect
- The masonry grid uses CSS `columns` which can produce uneven column heights with small item counts

## Related Documents

- [DDR-057](./DDR-057-desktop-1440p-ui-optimization.md) — Typography and layout sizing (unchanged by this redesign)
- [DDR-058](./DDR-058-cloudconvert-style-file-list-restyle.md) — File list restyle (layout patterns partially superseded)
- [DDR-037](./DDR-037-step-navigation-and-state-invalidation.md) — Step navigation (color scheme updated)
- [DDR-038](./DDR-038-overlay-media-player.md) — Overlay media player (dark overlay convention preserved)
- [DDR-063](./DDR-063-split-processing-ui-screens.md) — Split processing UI (ProcessingIndicator redesigned)
- [`ui-guidelines.md`](../ui-guidelines.md) — Updated design system documentation
- [`style.css`](../../web/frontend/src/style.css) — Source of truth for CSS custom properties
