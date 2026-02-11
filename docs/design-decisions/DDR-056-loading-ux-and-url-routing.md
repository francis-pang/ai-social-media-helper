# DDR-056: Loading UX and URL-Based Routing

**Date**: 2026-02-11  
**Status**: Accepted  
**Iteration**: Phase 2 Cloud Deployment

## Context

The application uses an in-memory `currentStep` signal with no URL changes. Users who navigate to a long-running processing step (triage, AI selection, enhancement, caption generation) see only a single static line of text ("Analyzing media with AI... This may take a minute.") with no indication of elapsed time, job status, or system activity. This leads to a poor perceived experience during operations that can take 1-3 minutes.

Additionally, the browser URL never changes as users move through the multi-step workflow. This means:

1. Browser back/forward buttons have no effect.
2. URLs are not shareable — there is no way to link to a specific step or session.
3. Refreshing the page always resets to the landing page.

## Decision

### Part 1: URL-Based Routing

Add path-based routing using `history.pushState` / `popstate`. No router library is needed — the Go server and CloudFront already serve `index.html` for unknown paths.

**URL scheme:**

| Path | Step | Query |
|------|------|-------|
| `/` | landing | |
| `/triage/upload` | triage-upload | |
| `/triage/processing` | processing | `?session=SESSION_ID` |
| `/triage/results` | results | `?session=SESSION_ID` |
| `/select/upload` | upload | |
| `/select/ai` | selecting | `?session=SESSION_ID` |
| `/select/review` | review-selection | `?session=SESSION_ID` |
| `/select/enhance` | enhancing | `?session=SESSION_ID` |
| `/select/enhanced` | review-enhanced | `?session=SESSION_ID` |
| `/select/groups` | group-posts | `?session=SESSION_ID` |
| `/select/download` | publish | `?session=SESSION_ID` |
| `/select/description` | description | `?session=SESSION_ID` |
| `/select/publish` | instagram-publish | `?session=SESSION_ID` |

Local-mode steps (`browse`, `confirm-files`) remain at `/` since they are local-only and not shareable.

**Implementation:**

- **New file: `src/router.ts`** — contains `STEP_TO_PATH` / `PATH_TO_STEP` maps, `syncUrlToStep()`, `parseUrlToState()`, and `initRouter()`.
- **New function: `setStep(step)`** (in `app.tsx`) — replaces direct `currentStep.value = ...` assignments; updates the signal and syncs the URL in one call.
- `navigateToStep()`, `navigateBack()`, and `navigateToLanding()` all call `syncUrlToStep()` after updating state.
- `initRouter()` is called once in `main.tsx` before the initial render. It parses the current URL to restore state and registers a `popstate` listener for browser back/forward.

### Part 2: Enhanced Waiting Screens

Replace all static waiting-screen text with a shared `ProcessingIndicator` component that provides:

1. **Elapsed time stopwatch** — "0:42" counting up every second (M:SS format). Gives users clear feedback that the app is still working, even when the backend is churning on a long AI operation.
2. **Status badge** — shows `pending` / `processing` with a colored dot.
3. **Animated spinner** — reuses the existing CSS `@keyframes spin` pattern.
4. **Technical info panel** — collapsible monospace panel showing job ID, session ID, poll interval, file count, and current status. Collapsed by default with a "Show details" toggle.
5. **Optional progress bar** — for views that report `completedCount` / `totalCount` (enhancement).
6. **Optional child slot** — for custom content (e.g. per-item status grid in enhancement).
7. **Optional cancel button** — for views that support cancellation.

Additionally, a lightweight `ElapsedTimer` component is exported for views that already have their own layout (PublishView).

**Updates per view:**

| View | Changes |
|------|---------|
| `TriageView` | Static text → `ProcessingIndicator` with title, description, status, job ID, session ID, poll interval, file count |
| `SelectionView` | Static text + spinner → `ProcessingIndicator` with title, description, status, job ID, session ID, cancel button, error child |
| `EnhancementView` | Static text + inline progress → `ProcessingIndicator` with progress bar and per-item status as children |
| `DescriptionEditor` | `GeneratingSpinner` component → `ProcessingIndicator` with title, description, session ID, job ID |
| `PublishView` | Added `ElapsedTimer` next to the existing progress counter |

## Rationale

- **Elapsed timer** — the single highest-impact UX improvement for long operations. Users no longer wonder "is this stuck?" when the AI takes 90 seconds.
- **Shared component** — `ProcessingIndicator` replaces five separate inline waiting screens with one consistent, reusable component (~200 lines). Future waiting screens get the full treatment automatically.
- **No router library** — `history.pushState` + `popstate` is ~130 lines. Adding `preact-router` or `wouter` would bring dependencies for a feature that only needs path ↔ step mapping.
- **Session ID in URL** — enables shareable links and URL persistence across page refreshes (limited by backend state availability).
- **Cloud-mode only routing** — local mode has no meaningful URLs to share, so the router is a no-op.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| `preact-router` / `wouter` | Adds a dependency for ~130 lines of pushState logic; the step-to-path mapping is a simple lookup table |
| Hash-based routing (`#/triage/processing`) | Less clean URLs; pushState is well-supported and CloudFront already handles SPA routing |
| `setInterval` polling indicator (polling server for ETA) | Backend doesn't expose ETA; elapsed time + status badge provides sufficient feedback |
| Toast-style notifications | Doesn't solve the problem — users need a persistent, visible indicator during long waits |

## Consequences

**Positive:**

- Users see elapsed time, status, and technical details during all long-running operations
- Browser back/forward buttons work within the workflow
- URLs can be bookmarked or shared (with the caveat that backend state may expire)
- Consistent waiting-screen UX across all views
- Five separate waiting-screen implementations replaced by one shared component

**Trade-offs:**

- URL routing restores step and session ID, but not full component state (e.g. triage results, enhancement progress). A page refresh during processing will show the processing indicator but won't restore the actual polling connection — the user may need to restart the operation.
- The `@keyframes spin` declaration is duplicated in the ProcessingIndicator and PublishView inline styles. A future cleanup could move it to `style.css`.

## Files Changed

- **New:** `src/router.ts` — URL routing logic
- **New:** `src/components/ProcessingIndicator.tsx` — shared waiting screen component
- **Edit:** `src/app.tsx` — integrate router (`syncUrlToStep`), add `setStep()` helper
- **Edit:** `src/main.tsx` — call `initRouter()` before render
- **Edit:** `src/components/TriageView.tsx` — use `ProcessingIndicator`, use `setStep()`
- **Edit:** `src/components/SelectionView.tsx` — use `ProcessingIndicator`, use `setStep()`
- **Edit:** `src/components/EnhancementView.tsx` — use `ProcessingIndicator`, use `setStep()`
- **Edit:** `src/components/DescriptionEditor.tsx` — replace `GeneratingSpinner` with `ProcessingIndicator`
- **Edit:** `src/components/PublishView.tsx` — add `ElapsedTimer` to publishing progress

## Related Documents

- [DDR-037: Step Navigation UI and State Invalidation](./DDR-037-step-navigation-and-state-invalidation.md) — step navigation system
- [DDR-042: Landing Page Workflow Switcher](./DDR-042-landing-page-workflow-switcher.md) — workflow routing and landing page
