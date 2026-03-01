# DDR-080: FB Prep Upload Step + Shared Upload Engine Refactor

**Date**: 2026-03-01  
**Status**: Accepted  
**Iteration**: Cloud — FB Prep upload flow + frontend upload code deduplication

## Context

### Problem 1: Facebook Prep Has No Upload Step

The Facebook Prep workflow (DDR-078) navigates directly from the landing page to `FBPrepView`, which requires `fbPrepMediaKeys` to be pre-populated. This signal is never set — the workflow has no upload step — so users always see "No media selected for Facebook prep." The feature is completely unusable.

### Problem 2: Three-Way Upload Code Duplication

Three upload components contain nearly identical S3 upload logic:

| Logic | FileUploader | MediaUploader | Duplication |
|-------|-------------|---------------|-------------|
| `uploadFile()` presigned + multipart | lines 245–272 | lines 186–214 | ~90 lines × 2 |
| `addFiles()` state management | lines 170–243 | lines 142–184 | ~60 lines × 2 |
| `updateFile()` signal update | lines 274–278 | lines 216–219 | ~5 lines × 2 |
| Speed tracking | lines 65–94 | absent | — |
| Content dedup (DDR-067) | lines 186–206 | absent | — |

Adding a third uploader (`FBPrepUploader`) without extraction would create a third copy of ~160 lines. Any future bug fix (retry logic, error handling, size limits) would require changes in three places.

### Problem 3: fb-prep-lambda Not in CDK

`cmd/fb-prep-lambda/` exists and has all three endpoints implemented (`/start`, `/results`, `/feedback`), but the Lambda function is not declared in CDK:
- No `fbPrepProcessor` in `processing-lambdas.ts`
- No `FB_PREP_LAMBDA_ARN` env var set on the API Lambda
- No build step in the CI/CD pipeline

The backend code is complete (routes registered in `main.go`, DynamoDB model in `store.go`, Gemini integration in `handler.go`) but the infrastructure is missing.

### Problem 4: Lambda Feedback Handler is Broken

The feedback event sent by the API (`{ type: "fb-prep-feedback", sessionId, jobId, itemIndex, feedback }`) is not handled by the Lambda. `normalizeFBPrepInput()` checks for `mediaKeys` and returns an error when it's absent — all feedback requests silently fail.

## Decision

### 1. Shared Upload Engine (`web/src/upload/uploadEngine.ts`)

Extract a `createUploadEngine()` factory that returns per-instance isolated signals and upload orchestration. Each consumer creates its own engine instance:

```typescript
export interface UploadedFile {
  name: string; size: number; key: string;
  status: "pending" | "uploading" | "done" | "error";
  progress: number; loaded: number; error?: string;
  thumbnailDataUrl?: string;
}

export interface UploadEngine {
  readonly files: Signal<UploadedFile[]>;
  readonly error: Signal<string | null>;
  readonly uploadSpeed: Signal<number>;
  addFiles(sessionId: string, newFiles: File[], opts?: AddFilesOpts): Promise<number>;
  updateFile(name: string, updates: Partial<UploadedFile>): void;
  removeFile(name: string): void;
  clearAll(): void;
  resetState(): void;
  getTotalLoaded(): number;
}

export function createUploadEngine(config?: {
  enableDedup?: boolean;      // DDR-067 content fingerprinting (default: false)
  enableSpeedTracking?: boolean;  // bytes/sec tracking (default: false)
}): UploadEngine
```

The engine owns: S3 presigned PUT upload, S3 multipart upload (>100 MB), progress callbacks, content dedup via `quickFingerprint`/`fullHash`, speed tracking.

The engine does NOT own: triage pipeline init/finalize/polling, thumbnail generation, navigation, workflow-specific signals.

### 2. FileUploader Refactor

`FileUploader.tsx` adopts `createUploadEngine({ enableDedup: true, enableSpeedTracking: true })`. The triage-specific code remains unchanged: `triageInitialized`, `triagePolling`, `triageFinalized`, `initTriageSession`, `pollTriageResults`. The `addFiles()` wrapper calls `engine.addFiles()` then triggers triage init/update.

Net reduction: ~150 lines removed from FileUploader (upload logic delegated to engine).

### 3. MediaUploader Migration

`MediaUploader.tsx` adopts `createUploadEngine({ enableDedup: false, enableSpeedTracking: false })`. Thumbnail generation remains in MediaUploader — it calls `generateThumbnail()` per file and sets `thumbnailDataUrl` via `engine.updateFile()`.

### 4. FBPrepUploader (`web/src/components/FBPrepUploader.tsx`)

New upload component using `createUploadEngine({ enableDedup: true, enableSpeedTracking: true })`. Reuses the same 2-column layout as FileUploader (drop zone + sidebar) but with FB prep-specific sidebar content and a "Continue to Facebook Prep" button.

On proceed: sets `fbPrepMediaKeys.value` with uploaded S3 keys, then navigates to `"fb-prep"` step.

File cards show 2-step pipeline (Upload → Done) without the server-side processing overlay — fb-prep-lambda processes files from S3 after navigation, not during upload.

### 5. New `fb-prep-upload` Step

```
Landing → fb-prep-upload → fb-prep
```

- URL: `/fb-prep/upload` (carries `?session=` query param)
- Workflow: `"fb-prep"` (existing)
- `LandingPage.tsx`: `startStep: "fb-prep-upload"` instead of `"fb-prep"`

### 6. fb-prep-lambda CDK Infrastructure

Add `fbPrepProcessor` Lambda to `ProcessingLambdas`:
- Runtime: ECR Private light, `fb-prep-latest` tag
- Memory: 2048 MB (Gemini response parsing + S3 downloads)
- Timeout: 5 minutes (real-time mode); economy mode is async via batch API
- Environment: `sharedEnv` (Gemini key, Vertex AI, S3, DynamoDB)
- IAM: S3 read/write, DynamoDB CRUD, SSM Gemini key, SSM GCP SA

Wire `FB_PREP_LAMBDA_ARN` into the API Lambda environment. Add to `lambda:InvokeFunction` policy, deploy list, and build pipeline (Wave 1, light image).

### 7. Lambda Feedback Handler Fix

Add feedback type detection before `normalizeFBPrepInput` in `handler.go`:

```go
func handler(ctx context.Context, event interface{}) (*FBPrepOutput, error) {
    // Check for feedback event type first
    if m, ok := event.(map[string]interface{}); ok {
        if t, _ := m["type"].(string); t == "fb-prep-feedback" {
            return handleFeedback(ctx, m)
        }
    }
    // Existing batch start logic...
    input, err := normalizeFBPrepInput(event)
    ...
}
```

`handleFeedback()` loads the job from DynamoDB, extracts the target item's S3 key and sibling captions (for narrative coherence per DDR-078 §4), re-runs Gemini for that one item, and updates the job's item in DynamoDB.

## Consequences

- FBPrepUploader is a new 300-line component (not a copy of FileUploader — it uses the shared engine)
- FileUploader shrinks by ~150 lines; MediaUploader shrinks by ~80 lines
- Adding a fourth upload workflow in the future requires only: a new 200-line component + engine instantiation — no upload logic to duplicate
- fb-prep-lambda ECR image must be pushed before first CDK deploy (bootstrap image)
- Feedback regeneration now correctly works end-to-end

## Rejected Alternatives

- **Copy FileUploader for FBPrepUploader**: Creates a third copy of upload logic, fails at the first bug fix
- **Make FileUploader configurable via props**: Props-based configuration on a 1087-line module-level-signal file creates complex state management; the engine factory pattern is cleaner
- **Keep fb-prep-lambda outside CDK**: Requires manual Lambda creation and IAM wiring; CDK must own all infrastructure
