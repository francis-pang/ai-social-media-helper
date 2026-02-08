# DDR-037: Step Navigation UI and Downstream State Invalidation

**Date**: 2026-02-08  
**Status**: Accepted  
**Iteration**: Selection flow navigation

## Context

The cloud selection flow has 8 steps (Upload → AI Selection → Review Selection → Enhancement → Review Enhanced → Group Posts → Download → Description). Users need:

1. **Visual orientation** — know where they are in the process and what comes next.
2. **Back navigation** — revisit previous steps to review decisions.
3. **State consistency** — if a user goes back and changes something (e.g., re-runs selection at step 2), all downstream results (enhancement, grouping, downloads, descriptions) that depended on the old selection must be invalidated.

The current implementation has `navigateToStep()` and `navigateBack()` in `app.tsx` using a signal-based step history stack, but no visual step indicator and no downstream state invalidation.

### Design Inspiration

The step navigator combines two visual patterns from the user's reference screenshots:

- **Arrow connectors between steps** (orange-themed reference): Each step is connected to the next by an arrow indicator, clearly showing the flow direction.
- **Minimalist labeling** (blue-themed reference): Each step shows only a short label (1-3 words) with an icon — no lengthy descriptions.

## Decision

### 1. Step Navigator Component

A horizontal step bar rendered below the app header in cloud mode. Each step is a compact pill showing:

- A step number
- A short label (e.g., "Upload", "Select", "Enhance")
- Visual state: **completed** (filled), **current** (highlighted ring), **future** (dimmed)

Steps are connected by arrow separators (`→`). Completed steps are clickable to navigate back. The component is responsive — wraps gracefully on narrow viewports.

### 2. Color Scheme

Uses the app's existing dark-theme CSS variables with a warm accent gradient for step progression:

| State | Color | Purpose |
|-------|-------|---------|
| Completed | `#51cf66` (existing success green) | Confirmed step, clickable |
| Current | `#6c8cff` (existing primary blue) | Active step, highlighted |
| Future | `var(--color-text-secondary)` | Not yet reached, dimmed |
| Arrow (completed) | `#51cf66` | Flow direction for completed path |
| Arrow (future) | `var(--color-border)` | Upcoming flow direction |

### 3. Downstream State Invalidation

When a user navigates back and triggers re-processing at a step, all downstream state is invalidated. The invalidation is **cascading**: each step depends on the one before it.

**Dependency chain:**

```
Upload → Selection → Enhancement → Grouping → Download → Description
  S1        S2           S3           S4         S5          S6
```

If user changes step N, steps N+1 through S6 are invalidated.

**Frontend invalidation**: Each component's signals are reset to initial values. Job IDs, results, and cached data are cleared.

**Backend invalidation**: A new `POST /api/session/invalidate` endpoint accepts a session ID and a "from step" parameter. It:

1. Deletes in-memory job entries for the invalidated steps (selection, enhancement, download, description jobs associated with the session).
2. Optionally deletes S3 artifacts under `{sessionId}/thumbnails/` and `{sessionId}/enhanced/` prefixes (for enhancement invalidation).

This keeps the backend consistent with the frontend state. Since the backend currently uses in-memory maps, invalidation simply removes entries from the maps.

### 4. Step Definitions for Cloud Mode

Only the 6 user-visible steps are shown in the navigator. "Selecting" and "Enhancing" are processing sub-states within their parent steps (Selection and Enhancement) and are not separate navigator items.

| # | Navigator Label | Step values | Description |
|---|----------------|-------------|-------------|
| 1 | Upload | `upload` | Upload media files to S3 |
| 2 | Select | `selecting`, `review-selection` | AI analysis + review results |
| 3 | Enhance | `enhancing`, `review-enhanced` | Photo/video enhancement |
| 4 | Group | `group-posts` | Organize into post groups |
| 5 | Download | `publish` | Download or publish bundles |
| 6 | Caption | `description` | AI-generated captions |

### 5. Back Navigation Behavior

- Clicking a **completed step** navigates back to that step.
- All steps after the clicked step remain in their current state until the user re-triggers processing.
- If the user re-triggers processing at step N (e.g., clicks "Start Selection" again at step 2), the frontend calls `POST /api/session/invalidate` with `fromStep: "selection"`, then resets all downstream component signals.
- The user sees fresh empty states for steps N+1 onward and must work through them again.
- Simply viewing a completed step (without changing anything) does NOT invalidate downstream state.

## Rationale

- **Minimal navigation**: 6 steps with short labels keeps the bar compact and scannable, following the blue-themed reference's minimalist approach.
- **Arrow connectors**: Visually borrowed from the orange-themed reference — clearly shows the linear flow without cluttering the UI.
- **Cascading invalidation**: The simplest correct approach. Partial invalidation (e.g., "only re-enhance the 3 photos that changed") adds significant complexity for marginal benefit in v1. Full cascade is easy to reason about and implement.
- **Backend invalidation endpoint**: Even though state is in-memory and ephemeral, explicitly clearing stale jobs prevents the frontend from accidentally polling old results after navigating back.
- **Processing states hidden from navigator**: "Selecting" and "Enhancing" are transient processing phases, not decision points. Showing them as separate steps would inflate the bar and confuse users.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Breadcrumb text trail (e.g., "Upload > Selection > ...") | Too text-heavy, doesn't show future steps, doesn't match reference designs |
| Vertical sidebar stepper | Wastes horizontal space on a wide-layout app; horizontal bar is more compact |
| No backend invalidation (frontend-only) | Stale in-memory jobs would return old results if polled; backend must be consistent |
| Partial/surgical invalidation | Too complex for v1; would need dependency tracking per media item |
| Separate "Reset" button instead of automatic invalidation | Poor UX — users may forget to reset, leading to inconsistent state |

## Consequences

**Positive:**

- Users always know where they are in the workflow and what's next.
- Back navigation is safe — downstream data is cleaned up automatically when re-processing.
- The design is visually consistent with the app's dark theme and minimalist aesthetic.
- Step navigator works on both desktop and mobile widths (wraps on small screens).

**Trade-offs:**

- Cascading invalidation means users lose ALL downstream work when going back and re-processing. This is documented to users via the step state (steps go from completed back to future state).
- The invalidation endpoint adds a small amount of backend complexity (one new route, one new function).
- S3 artifact cleanup is best-effort — if the Lambda times out mid-deletion, orphaned files are cleaned by the bucket's 24-hour lifecycle policy.

## Related Documents

- [DDR-029](./DDR-029-file-system-access-api-upload.md) — File upload step
- [DDR-030](./DDR-030-cloud-selection-backend.md) — Selection backend
- [DDR-031](./DDR-031-multi-step-photo-enhancement.md) — Enhancement pipeline
- [DDR-033](./DDR-033-post-grouping-ui.md) — Post grouping
- [DDR-034](./DDR-034-download-zip-bundling.md) — Download bundling
- [DDR-036](./DDR-036-ai-post-description.md) — Description generation
