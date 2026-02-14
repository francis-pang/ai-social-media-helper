# DDR-058: CloudConvert-Style File List and Processing UI Restyle

**Date**: 2026-02-14  
**Status**: Accepted  
**Iteration**: 1

## Context

The upload file list and processing indicator UIs use flat, tightly-packed rows separated only by `border-bottom` dividers, with tiny colored dots for status and full-text "Remove" buttons. While functional, this layout has several usability issues at the 1m viewing distance established in DDR-057:

1. **Low visual separation** — files blend together without card-like row boundaries, especially in long lists.
2. **Status readability** — small colored dots (6px) next to plain text ("Uploaded", "Pending") are hard to scan quickly. The dot-to-text mapping requires reading both elements.
3. **Per-file progress is invisible** — during upload, only the overall progress bar shows activity; individual file progress is a small percentage text.
4. **Remove button is oversized** — the full "Remove" text button takes significant horizontal space per row and is visually noisy.
5. **No persistent add-more affordance** — after initial file selection, the only way to add files is to re-use the drop zone (FileUploader) or the top picker buttons (MediaUploader). CloudConvert's persistent "Add more Files" button is more discoverable.
6. **Processing view lacks file-level detail** — the ProcessingIndicator shows only a spinner and overall progress, with no per-file status (except EnhancementView which renders its own inline item grid as children).

CloudConvert's file converter UI demonstrates effective UX patterns for these exact problems: spacious card-like rows, colored pill badges, inline progress bars, compact X remove buttons, and a persistent add-more button.

## Decision

Restyle the upload file lists and processing indicator to adopt CloudConvert's layout and interaction patterns while keeping the existing dark theme, CSS variables, and Preact architecture. Specifically:

### 1. Shared CSS Utility Classes (`style.css`)

Add reusable CSS classes that both uploaders and the processing indicator use:

- **`.file-row`** — spacious row with `--color-surface` background, subtle border, rounded corners, hover effect. Replaces `border-bottom` separators with discrete mini-card rows.
- **`.status-badge`** with variants (`.status-badge--pending`, `--uploading`, `--done`, `--error`, `--processing`) — pill-shaped badges using existing CSS variable colors. Replaces dot + text status.
- **`.file-progress-bar`** — thin (4px) inline progress bar that sits at the bottom edge of a file row during upload.
- **`.btn-remove`** — small circular X button (danger-tinted on hover). Replaces the full-text "Remove" button.
- **`.btn-add-more`** — outline button with + icon prefix for "Add more Files".

### 2. FileUploader (Triage Upload)

- Replace `border-bottom` file list rows with `.file-row` cards (file icon, filename, size, status badge, X button).
- Add `.file-progress-bar` inside each row during `status === "uploading"`.
- Replace "Remove" button with `.btn-remove`.
- Add "Add more Files" button at bottom-left of the actions bar.
- Keep drop zone but make it more compact/secondary.

### 3. MediaUploader (Selection Upload)

- Same structural changes as FileUploader, plus:
- Keep existing 40x40 thumbnails.
- Move media type badge (Photo/Video) next to status badge.
- Add "Add more Files" button (split between "Choose Files" / "Choose Folder") at bottom-left.
- Add `.file-progress-bar` per row during upload.

### 4. ProcessingIndicator

- Make spinner smaller and position it inline with the title (left of title text) instead of large centered.
- Add optional `items` prop (`Array<{ name: string; status: string }>`) for per-file processing status, rendered as `.file-row` cards with `.status-badge`.
- Keep overall progress bar above the file list.
- Keep elapsed timer, collapsible details panel, and cancel button unchanged.

## Rationale

1. **Proven UX patterns** — CloudConvert is a widely-used file converter; its file list UX has been refined over years of user feedback. Adopting these patterns is lower-risk than inventing new ones.
2. **Visual hierarchy** — card-like rows with pill badges are faster to scan than flat rows with dot indicators, especially at 1m distance.
3. **No new dependencies** — all changes are CSS + existing Preact; no runtime cost increase.
4. **CSS classes are reusable** — the shared `.file-row`, `.status-badge`, and `.file-progress-bar` classes can be used by future components.
5. **Callers of ProcessingIndicator benefit automatically** — the new `items` prop formalizes what EnhancementView was already doing via children, and enables TriageView/SelectionView to show per-file status in the future.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Full CloudConvert clone (colors, fonts, layout) | We have an established dark theme and design system; cloning colors/fonts would create inconsistency |
| Third-party file upload component library | Adds runtime dependency, bundle size, and styling integration effort for marginal benefit |
| Keep existing layout, just add progress bars | Does not address the row separation, status readability, or remove button issues |
| Move to a table-based layout | Tables are rigid; card-based rows are more flexible for responsive layouts and varying content |

## Consequences

**Positive:**
- File rows are visually distinct and easier to scan in long lists
- Status is immediately recognizable via colored pill badges (no need to parse dot + text)
- Per-file progress bars give clearer feedback during upload
- Compact X button saves horizontal space per row
- Persistent "Add more Files" button improves discoverability
- ProcessingIndicator can now show per-file status natively

**Trade-offs:**
- Slightly more vertical space per row (card padding) — offset by better readability
- CSS class additions increase `style.css` size by ~80 lines
- EnhancementView's custom children rendering still works but could be migrated to the new `items` prop in a future iteration

## Related Documents

- [DDR-056: Loading UX and URL Routing](./DDR-056-loading-ux-and-url-routing.md)
- [DDR-057: Desktop 1440p UI Optimization](./DDR-057-desktop-1440p-ui-optimization.md)
- [DDR-029: File System Access API for Cloud Media Upload](./DDR-029-file-system-access-api-upload.md)
