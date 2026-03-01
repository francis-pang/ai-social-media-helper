# DDR-074: Local File Deletion via File System Access API

**Date**: 2026-02-28  
**Status**: Accepted  
**Iteration**: Cloud triage — local drive cleanup

## Context

The cloud triage flow uploads media to S3 for Gemini AI evaluation, then lets the user confirm which "discard" files to delete. Currently, the "Delete" action only removes S3 objects and cleans up session artifacts. This is misleading — the user's actual goal is to clean up their local drive, not S3.

The browser's standard `<input type="file">` and drag-and-drop APIs do not retain references to local files. They return `File` objects with a `name` property but no path or handle, making local deletion impossible.

Additionally, the Go backend returned `null` for the `errors` field when no errors occurred (Go nil slice → JSON `null`), which crashed the frontend's `confirmResult.value.errors.length` access, causing the "Deleting..." button to hang even though the backend succeeded.

## Decision

### 1. Use `showOpenFilePicker()` for file selection

Replace the hidden `<input type="file">` with the [File System Access API](https://developer.mozilla.org/en-US/docs/Web/API/Window/showOpenFilePicker)'s `showOpenFilePicker({ multiple: true })`. This returns `FileSystemFileHandle` objects that retain a persistent reference to each file on disk.

Store these handles in a `Map<string, FileSystemFileHandle>` signal (`fileHandles` in `app.tsx`) keyed by filename. The map persists across steps (upload → processing → results → confirm).

Users can click "Add Files" multiple times; each batch appends to the map.

### 2. Delete from local filesystem on triage confirm

After the existing S3 cleanup API call succeeds, iterate the confirmed filenames:

1. Look up each `FileSystemFileHandle` from the stored map
2. Request write permission: `handle.requestPermission({ mode: 'readwrite' })` — Chrome batches this into a single browser prompt
3. Call `handle.remove()` to delete the file from the local filesystem
4. Track local deletion count and errors separately

### 3. Graceful fallback

- **Feature detection**: `showOpenFilePicker` is only available in Chromium browsers (Chrome, Edge). If unavailable, fall back to `<input type="file">`.
- **Drag-and-drop**: still works but does not produce handles. Files added via drag-and-drop cannot be deleted locally — the confirm screen shows their filenames for manual deletion.
- **Permission denied**: if the user denies write permission, report the error and show filenames.

### 4. Fix Go nil-slice JSON serialization

Change `var errMsgs []string` to `errMsgs := make([]string, 0)` in `handleTriageConfirm` so the JSON response contains `"errors": []` instead of `"errors": null`.

## Implementation

| File | Change |
|------|--------|
| `web/frontend/src/app.tsx` | Add `fileHandles` signal (`Map<string, FileSystemFileHandle>`), reset in `navigateToLanding()` |
| `web/frontend/src/components/FileUploader.tsx` | Replace `<input type="file">` with `showOpenFilePicker()`, store handles in map, fall back to `<input>` if API unavailable |
| `web/frontend/src/components/TriageView.tsx` | After S3 cleanup, use stored handles to delete locally; update confirmation screen |
| `cmd/media-lambda/triage.go` | `errMsgs := make([]string, 0)` to fix nil-slice serialization |

## Browser Support

| Browser | File picker | Local deletion | Fallback |
|---------|------------|----------------|----------|
| Chrome 86+ | `showOpenFilePicker` | `handle.remove()` | — |
| Edge 86+ | `showOpenFilePicker` | `handle.remove()` | — |
| Firefox | Not supported | Not supported | `<input type="file">` + manual deletion filenames |
| Safari | Not supported | Not supported | `<input type="file">` + manual deletion filenames |

## Consequences

- Users on Chrome/Edge can delete triage-discarded files directly from their local drive without leaving the app
- Drag-and-drop remains functional but without local deletion capability
- The `fileHandles` map is in-memory only; a page refresh loses the handles (user must re-add files)
- Write permission is requested at deletion time (lazy), not at upload time, to avoid unnecessary prompts when the user keeps all files
