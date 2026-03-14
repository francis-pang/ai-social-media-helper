# DDR-087: Start Over and Reset on Back — All Three Workflows

**Date**: 2026-03-14  
**Status**: Accepted  
**Iteration**: Cloud — consistent UX across Triage, Selection, FB Prep

## Context

Users needed a consistent way to (1) abandon the current run and return to the tool chooser (“Start Over”) and (2) avoid seeing stale results when going Back and then continuing with different or same media (“Reset on Back”). Triage already had Start Over and handleBack-based reset; Selection and FB Prep did not.

## Decision

### 1. Start Over button

- **Triage**: Unchanged (already has Start Over on results and ProcessingIndicator cancel).
- **FB Prep**: Add a “Start Over” button on the results page ActionBar. On click: `resetFBPrepState()`, `resetFBPrepUploaderState()`, `navigateToLanding()`.
- **Selection**: Add a “Start Over” button to the action bar of SelectionView, EnhancementView, PostGrouper, DownloadView, DescriptionEditor, and PublishView. Each calls `navigateToLanding()` (which already resets all workflow state).

### 2. Reset on Back

Centralize reset in `navigateBack()`: when the user leaves a step via Back, call that step’s reset so the next time they reach a later step they don’t see stale state. Only steps that currently call `navigateBack()` directly (no preceding handleBack reset) are added to a step-to-reset map:

- `fb-prep` → `resetFBPrepState`
- `group-posts` → `resetPostGrouperState`
- `publish` → `resetDownloadState`
- `description` → `resetDescriptionState`
- `instagram-publish` → `resetPublishState`

Triage (handleBack) and SelectionView/EnhancementView (handleBack) already reset before calling `navigateBack()`, so they are not added to the map to avoid double-reset.

### 3. navigateToLanding and FB Prep

Add `resetFBPrepState()` to `navigateToLanding()` so that going Home from any workflow always clears FB Prep job state.

## Files Changed

- `web/src/components/FBPrepView.tsx` — `startOver()`, Start Over button, import `navigateToLanding`, `resetFBPrepUploaderState`
- `web/src/components/SelectionView.tsx` — import `navigateToLanding`, Start Over button
- `web/src/components/EnhancementView.tsx` — import `navigateToLanding`, Start Over button
- `web/src/components/PostGrouper.tsx` — import `navigateToLanding`, Start Over button
- `web/src/components/DownloadView.tsx` — import `navigateToLanding`, Start Over button
- `web/src/components/DescriptionEditor.tsx` — import `navigateToLanding`, Start Over button (early return and ActionBar)
- `web/src/components/PublishView.tsx` — import `navigateToLanding`, Start Over button
- `web/src/app.tsx` — import `resetFBPrepState`; `RESET_ON_BACK` map in `navigateBack()`; `resetFBPrepState()` in `navigateToLanding()`

## Consequences

- Same UX across Triage, Selection, and FB Prep: Start Over returns to landing; Back clears the step being left.
- No stale FB Prep results when user goes Back to upload and then Continue.
- Landing always clears FB Prep job state when user clicks Home or Start Over.
