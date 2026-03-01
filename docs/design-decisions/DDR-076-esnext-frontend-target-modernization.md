# DDR-076: ESNext Target and ES2023–2025 Frontend Idioms

**Date**: 2026-03-01  
**Status**: Accepted  
**Iteration**: Frontend toolchain — language modernization

## Context

The Preact frontend (`web/frontend/`) was created in DDR-022 with `tsconfig.json` targeting `ES2020` and `lib: ["ES2020", "DOM", "DOM.Iterable"]`. Since then:

- TypeScript 5.9.3 (in use) fully supports ES2023, ES2024, and ES2025 features.
- Vite 7 with Rolldown already targets modern browsers natively — no polyfill step needed.
- ES2023 introduced `.toReversed()`, `.toSorted()`, `.toSpliced()`, `.with()` (non-mutating array methods) and `.findLast()` / `.findLastIndex()`.
- ES2024 introduced `Promise.withResolvers()`, `Set.prototype.union/intersection/difference/symmetricDifference`, `Object.groupBy()`, and `Array.prototype.toSorted()`.
- ES2025 (finalized June 2025) introduced Iterator Helpers, `Promise.try()`, and `RegExp.escape()`.

The existing codebase has several call sites where new idioms apply directly:

- **6** instances of the `new Promise((resolve, reject) => { ... })` anti-pattern where `resolve`/`reject` are only used as callbacks passed into event handlers or legacy callback APIs — the ideal case for `Promise.withResolvers()`.
- **5** instances of `arr[arr.length - 1]` / `.split(...).pop()` that `.at(-1)` expresses more clearly.
- **1** mutating `.sort()` that should be `.toSorted()` (S3 multipart parts list in `uploadToS3Multipart`).
- **1** `new Set([...a, ...b])` set union that `Set.prototype.union()` handles directly.
- **1** manual `for...of` map-building loop that the `Map` constructor with entries replaces.

Using a fixed target like `ES2024` or `ES2025` requires a manual bump as the spec advances. Using `ESNext` means new features become available automatically as TypeScript adds support, matching the project's practice of always using the latest TypeScript (`^5.9.3`).

## Decision

Two decisions:

### 1. Switch `tsconfig.json` `target` and `lib` from `ES2020` to `ESNext`

TypeScript emits code targeting the latest ECMAScript, and the lib includes all standard built-in types. No manual bumps needed when ES2026 ships. Vite/Rolldown handles bundling for modern browsers.

As a side effect, `useDefineForClassFields` can be removed from `tsconfig.json` — it becomes the default when `target` is `ESNext`.

### 2. Adopt ES2023–2025 idioms at existing call sites

Refactor 14 specific call sites across 9 files to use `Promise.withResolvers()`, `.toSorted()`, `.at(-1)`, `Set.prototype.union()`, and the `Map` constructor with entries.

## Alternatives Considered

| Approach | Rejected Because |
|----------|-----------------|
| Keep ES2020 | Blocks use of non-mutating array methods, `Promise.withResolvers`, Set operations, and `.at(-1)` despite TypeScript 5.9 supporting them all |
| Fix to ES2024 | Would need another manual bump for ES2025 features; `ESNext` is already the practice for the TS version pin (`^5.9.3`) |
| Fix to ES2025 | Same manual-bump problem; `ESNext` is more honest about the intent |
| Polyfill older browsers | No browserslist is configured; Vite 7 targets modern browsers by default; the user base is the developer themselves |

## Consequences

**Positive:**

- All ES2023/2024/2025 built-in types are available without tsconfig changes
- `Promise.withResolvers()` separates promise lifecycle from executor, making XHR/callback wrappers easier to read and extend (e.g. adding abort support)
- `.toSorted()` prevents accidental mutation of `completedParts` before the S3 `CompleteMultipartUpload` call
- `.at(-1)` is semantically clear ("last element") vs index arithmetic
- `Set.prototype.union()` removes spread-and-reconstruct boilerplate for set merges
- Future ES2026+ features (e.g. Iterator Helpers becoming stable) arrive automatically
- TypeScript version pin (`^5.9.3`) and `ESNext` target are now philosophically aligned — both track latest

**Trade-offs:**

- `ESNext` output is not transpiled down — requires a modern browser (Chrome 120+, Firefox 130+, Safari 17+). Acceptable given the user base.
- `Promise.withResolvers()` is slightly more verbose at the declaration site (3 lines vs 1) — compensated by removing the nested executor closure
- `useDefineForClassFields` can be removed from tsconfig (becomes default for ESNext), minor config cleanup

## Implementation

### Files Modified

| File | Change |
|------|--------|
| `web/frontend/tsconfig.json` | `target` and `lib` → `ESNext`; remove `useDefineForClassFields` |
| `web/frontend/src/api/client.ts` | `uploadToS3`: `Promise.withResolvers()`; `uploadChunk`: `Promise.withResolvers()`; `uploadToS3Multipart`: `.toSorted()`; `isVideoFile`: `.at(-1)` |
| `web/frontend/src/components/media-uploader/thumbnailGenerator.ts` | `generateImageThumbnail`: `Promise.withResolvers()`; `generateVideoThumbnail`: `Promise.withResolvers()` |
| `web/frontend/src/utils/fileSystem.ts` | `readAllEntries`: `Promise.withResolvers()`; `entryToFile`: `Promise.withResolvers()` |
| `web/frontend/src/components/FileBrowser.tsx` | set union → `Set.prototype.union()`; `basename` → `.at(-1)` |
| `web/frontend/src/app.tsx` | `navigateBack` → `history.at(-1)` |
| `web/frontend/src/components/SelectedFiles.tsx` | `basename` → `.at(-1)` |
| `web/frontend/src/components/TriageView.tsx` | filename extraction → `.at(-1)` |
| `web/frontend/src/components/FileUploader.tsx` | `getFilesWithLifecycle` serverMap → `Map` constructor |

## Related Documents

- DDR-022 (Web UI with Preact SPA and Go JSON API — original frontend setup, tsconfig, Vite choice)
- DDR-054 (S3 Multipart Upload Acceleration — `uploadToS3Multipart`, the `.toSorted` site)
- DDR-029 (File System Access API Upload — `fileSystem.ts` and `thumbnailGenerator.ts` origins)
