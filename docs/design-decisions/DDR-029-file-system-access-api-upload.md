# DDR-029: File System Access API for Cloud Media Upload

**Date**: 2026-02-08  
**Status**: Accepted  
**Iteration**: 17

## Context

The application is being extended from a triage-only workflow to a full media selection and publishing pipeline (see [Media Selection Feature Plan](../../.cursor/plans/media_selection_feature_update_141c5fac.plan.md)). Step 1 of the new workflow requires users to upload media files (photos and videos) for AI-powered selection.

The current cloud mode upload experience uses `<input type="file" multiple>` with drag-and-drop (`FileUploader.tsx`). This works for the triage flow but has limitations for the new selection workflow:

1. **No folder picker**: The existing uploader only supports individual file selection and drag-and-drop. There is no way to select an entire folder of media files from a trip or event.
2. **No file filtering in folders**: The HTML `<input type="file" webkitdirectory>` attribute returns ALL files in the folder (including non-media files like `.DS_Store`, `.txt`, etc.), requiring post-hoc client-side filtering.
3. **No client-side thumbnails**: The existing uploader shows no media previews during upload — thumbnails are only generated later by the backend.
4. **No trip context**: The new selection workflow requires the user to describe the trip/event before proceeding.

The new workflow needs two distinct picker modes: "Choose Files" (individual media files) and "Choose Folder" (entire folder of media from a trip/event). The target environment is **Chrome on macOS** (confirmed by the user).

## Decision

Use the **File System Access API** (`window.showOpenFilePicker()` and `window.showDirectoryPicker()`) for the new media upload component (`MediaUploader.tsx`) in cloud mode.

### Implementation Details

1. **File picker**: `window.showOpenFilePicker({ multiple: true, types: [...] })` with media type filters (images: JPEG, PNG, GIF, WebP, HEIC/HEIF; videos: MP4, MOV, AVI, WebM, MKV).

2. **Folder picker**: `window.showDirectoryPicker()` recursively iterates directory entries and filters to media files only, skipping non-media files before reading them into memory.

3. **Drag-and-drop**: Retained as an additional input method alongside the File System Access API buttons. Uses existing HTML5 drag-and-drop API.

4. **Client-side thumbnails**: Generated in the browser using `<canvas>` for images and `<video>` frame extraction + `<canvas>` for videos. Displayed immediately in the file list during upload.

5. **S3 upload**: Unchanged — files are uploaded to S3 via presigned PUT URLs using the existing `getUploadUrl()` and `uploadToS3()` API functions.

6. **Trip context**: A text input field allows the user to describe the trip/event (e.g., "3-day trip to Tokyo, Oct 2025"), stored in a Preact signal for use by the AI selection step.

### Component: `MediaUploader.tsx`

This is a new component for the selection flow, separate from the existing `FileUploader.tsx` (which remains for the triage flow). Key differences:

| Aspect | FileUploader (triage) | MediaUploader (selection) |
|--------|----------------------|--------------------------|
| File picking | `<input type="file">` + drag-and-drop | File System Access API + drag-and-drop |
| Folder support | None | `showDirectoryPicker()` with recursive media filtering |
| Thumbnails | None (shown later in triage results) | Client-side thumbnail generation on upload |
| Trip context | Not applicable | Text input for event/trip description |
| Media type badge | None | Photo/Video badge per file |
| Overall progress | Per-file only | Per-file + overall progress bar |
| Proceed action | "Continue" → confirm files → triage | "Continue to Selection" → AI selection |

## Rationale

### Why File System Access API over HTML File Input?

| Criterion | File System Access API | HTML `<input type="file">` |
|-----------|----------------------|---------------------------|
| File type filtering in folder picker | Yes — iterate and filter before reading | No — `webkitdirectory` returns all files |
| Multiple picker types | `showOpenFilePicker()` and `showDirectoryPicker()` are separate, clear APIs | Single `<input>` element toggled with `webkitdirectory` attribute |
| Recursive folder iteration | Yes — can recurse into subdirectories and filter | No — `webkitdirectory` returns flat list of all files including nested |
| Native feel | More native picker dialog | Generic browser file dialog |
| Chrome macOS support | Full support | Full support |
| API standard status | WHATWG File System standard (Chrome, Edge, Opera) | Fully standardized (all browsers) |
| Cross-browser support | Chrome/Edge/Opera only | All browsers |

Since the app targets **Chrome on macOS exclusively**, the cross-browser limitation of the File System Access API is irrelevant. The API provides cleaner code (separate methods for file vs. folder picking), better UX (media-only folder iteration with recursive scanning), and a more native feel.

### Why a new component instead of modifying FileUploader?

The triage flow and selection flow have different UX requirements:

- **Triage**: quick upload → immediate AI evaluation → delete bad files
- **Selection**: upload with context → AI selection → enhancement → grouping → publishing

Keeping `FileUploader.tsx` untouched preserves the working triage flow and avoids regression risk. The new `MediaUploader.tsx` is purpose-built for the selection workflow with additional features (folder picker, thumbnails, trip context, type badges).

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| HTML `<input type="file" webkitdirectory>` (Option A) | Returns all files in folder including non-media; `webkitdirectory` is non-standard (though Chrome supports it); returns flat list with no control over directory iteration; less native-feeling dialog |
| Modify existing `FileUploader.tsx` | Different UX requirements for triage vs. selection; adding folder picker, thumbnails, trip context, and type badges would bloat the component; risk of regression in working triage flow |
| Third-party file picker library (Uppy, FilePond) | Unnecessary dependency for a single-user app targeting one browser; File System Access API is simpler and more native; avoids npm dependency management |

## Consequences

**Positive:**

- Clean separation between file and folder picking with distinct API methods
- Recursive media-only filtering when iterating folder contents — non-media files are never loaded into memory
- Client-side thumbnail generation provides immediate visual feedback during upload
- Trip context field enables richer AI selection in subsequent steps
- Drag-and-drop still works as an additional input method
- Existing `FileUploader.tsx` and triage flow remain completely unchanged
- S3 upload mechanism reused — no backend changes required for Step 1
- Overall progress bar gives clear upload status across all files

**Trade-offs:**

- Only works in Chrome/Edge/Opera — not Firefox or Safari. Acceptable because the app targets Chrome on macOS exclusively
- Requires TypeScript type declarations for `showOpenFilePicker` and `showDirectoryPicker` (not in standard DOM lib typings)
- Two upload components in the codebase (`FileUploader.tsx` for triage, `MediaUploader.tsx` for selection) — acceptable since they serve different workflows
- HEIC thumbnail generation depends on macOS system decoder — may silently fail on other platforms (handled gracefully with try/catch)

## Implementation

| File | Changes |
|------|---------|
| `web/frontend/src/components/MediaUploader.tsx` | **New**: File System Access API pickers, recursive folder scanning, client-side thumbnails, S3 upload with progress, trip context input, media type badges |
| `web/frontend/src/types/file-system-access.d.ts` | **New**: TypeScript type declarations for `showOpenFilePicker` and `showDirectoryPicker` on `Window` |
| `web/frontend/src/app.tsx` | Add selection flow step types (`upload`, `selecting`, `review-selection`, etc.); render `MediaUploader` for cloud mode `upload` step; add `tripContext` and `stepHistory` signals; add `navigateToStep` and `navigateBack` functions; update header title to "Media Selection" in cloud mode |

## Related Decisions

- [DDR-022](./DDR-022-web-ui-preact-spa.md): Web UI with Preact SPA — established the frontend architecture and component patterns
- [DDR-026](./DDR-026-phase2-lambda-s3-deployment.md): Phase 2 Cloud Deployment — established S3 upload via presigned URLs (reused by MediaUploader)
- [DDR-028](./DDR-028-security-hardening.md): Security Hardening — Cognito authentication and input validation that protect the upload flow
