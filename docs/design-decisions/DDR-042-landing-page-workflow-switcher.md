# DDR-042: Landing Page Workflow Switcher

**Date**: 2026-02-09  
**Status**: Accepted  
**Iteration**: Phase 2 Cloud Deployment

## Context

The application supports two distinct workflows:

1. **Media Triage** — AI identifies unsaveable media (blurry, dark, accidental) for cleanup
2. **Media Selection** — full AI pipeline for Instagram posts (select, enhance, group, caption, publish)

Prior to this change, the workflow was determined at **build time** via the `VITE_CLOUD_MODE` environment variable:

- `VITE_CLOUD_MODE` not set (local mode): shows Media Triage with local file browser
- `VITE_CLOUD_MODE=1` (cloud mode): shows Media Selection with S3 upload

This meant the cloud deployment at `d10rlnv7vz8qt7.cloudfront.net` could **only** access the selection workflow. Users who wanted to triage media in the cloud had no way to reach that functionality — the triage UI was exclusive to local mode.

## Decision

Replace the build-time workflow switch with a **runtime landing page** that lets the user choose which workflow to enter.

### Changes

1. **New `"landing"` step** — added to the `Step` type union as the default starting step in cloud mode.

2. **`activeWorkflow` signal** — tracks which workflow the user chose: `"triage"`, `"selection"`, or `null` (on landing page). In local mode, defaults to `"triage"`.

3. **`LandingPage` component** — displays two cards:
   - **Media Triage** (red accent) — navigates to `"triage-upload"` step
   - **Media Selection** (blue accent) — navigates to `"upload"` step (existing selection flow)

4. **New `"triage-upload"` step** — reuses the existing `FileUploader` component (drag-and-drop S3 upload) for cloud triage. After upload, starts a triage job and navigates to `"processing"`.

5. **`navigateToLanding()` function** — resets all workflow state and returns to the landing page. Called by the "Home" header button or the clickable app title.

6. **Header changes** — app title is now clickable in cloud mode (returns to landing), and a "Home" button appears next to "Sign Out".

7. **`TriageView` "Start Over"** — in cloud mode, now returns to the landing page instead of the file browser.

### Navigation Flow

```
Cloud Mode:
  Login → Landing Page → [Media Triage]  → triage-upload → processing → results → [landing]
                        → [Media Selection] → upload → select → enhance → group → download → caption → publish

Local Mode (unchanged):
  browse → confirm-files → processing → results
```

### Component Reuse

- `FileUploader` (existing) — reused for cloud triage upload. Updated to start triage jobs directly (via `startTriage` API) instead of the previous `confirm-files` navigation.
- `TriageView` (existing) — already supports cloud mode (S3 keys, sessionId ownership verification). No changes needed to the triage results UI.
- `MediaUploader` (existing) — unchanged, still used for the selection workflow.

## Rationale

- **Runtime flexibility** — users can access both triage and selection from a single deployed URL without separate builds or deployments.
- **Zero backend changes** — the triage API (`/api/triage/start`) already accepts `sessionId` for cloud-mode triage. The landing page is purely a frontend routing change.
- **Minimal new code** — `LandingPage` is ~170 lines. All upload and triage logic is reused from existing components.
- **Clean separation** — the `activeWorkflow` signal keeps workflows isolated. The step navigator only appears for the selection workflow.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| URL-based routing (e.g., `/triage`, `/selection`) | Adds a router dependency; CloudFront SPA routing already handles 404→index.html, but path-based routing adds unnecessary complexity for a single-user tool |
| Query parameter switching (`?mode=triage`) | Less discoverable; no visual indicator of available features |
| Tab-based UI (always visible) | Workflows are long multi-step processes; tabs suggest quick switching between views, not multi-step wizards |
| Keep build-time switching | Requires separate deployments for each workflow; the original problem |

## Consequences

**Positive:**

- Both workflows accessible from a single CloudFront URL
- No new build-time configuration required
- Users can switch between workflows without reloading
- Landing page provides clear descriptions of each tool
- Existing component reuse minimizes new code surface

**Trade-offs:**

- One extra click to reach a workflow (landing page selection) compared to going directly to upload
- App title now serves double duty as navigation (clickable to return home) — may not be immediately discoverable, mitigated by the explicit "Home" button
- `FileUploader` now has triage-specific logic (starting triage jobs) — slightly less pure as a generic uploader

## Related Documents

- [DDR-026: Phase 2 Lambda + S3 Cloud Deployment](./DDR-026-phase2-lambda-s3-deployment.md) — original cloud deployment architecture
- [DDR-029: File System Access API for Cloud Media Upload](./DDR-029-file-system-access-api-upload.md) — selection workflow upload
- [DDR-037: Step Navigation UI and State Invalidation](./DDR-037-step-navigation-and-state-invalidation.md) — step navigation system
