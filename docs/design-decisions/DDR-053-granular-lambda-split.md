# DDR-053: Granular Lambda Split and Library Refactor

**Date**: 2026-02-10
**Status**: Accepted
**Iteration**: Phase 2 Cloud Deployment

## Context

The Worker Lambda (DDR-050) was introduced as a monolithic async processor handling 5 domains: triage, description, download, publish, and enhancement-feedback. While DDR-052 moved triage and publish into Step Functions polling loops, the Worker Lambda still serves as the runtime for all 10 handler types across these 5 domains.

Problems with the monolithic Worker Lambda:

1. **Troubleshooting difficulty**: All 10 handler types share a single CloudWatch log group. Filtering logs for a specific domain (e.g. publish errors) requires parsing the `type` field, making incident response slower.
2. **Blast radius**: A bug in the download handler's ZIP logic can crash the Lambda, affecting all in-flight triage, description, and publish jobs.
3. **Over-provisioned resources**: Download needs high memory (ZIP buffering) and ephemeral storage. Description needs neither. Both run at 2 GB / 2 GB ephemeral because the Worker Lambda must accommodate the most demanding handler.
4. **Credential sprawl**: The Worker Lambda loads both Gemini API key AND Instagram credentials at cold start, even though description/triage never use Instagram and download never uses Gemini.
5. **Binary bloat**: The Go binary includes all dependencies (genai SDK, Instagram client, zstd, etc.) even though each handler only uses a subset.
6. **Duplicated init code**: Every Lambda handler (`cmd/*/main.go`) independently implements the same AWS config loading, S3 client setup, DynamoDB store init, and SSM parameter fetch.

Additionally, the `internal/chat` package (15 files, ~2,900 lines) and `internal/filehandler` package (14 files, ~2,700 lines) have grown into large monoliths that are hard to navigate and force all consumers to pull in every dependency.

## Decision

### 1. Split Worker Lambda into 4 domain-specific Lambdas

| New Lambda | Handlers | Credentials | Key Dependencies |
|------------|----------|-------------|------------------|
| **triage-lambda** | triage-prepare, triage-check-gemini, triage-run | Gemini API key | chat, filehandler, S3, DynamoDB |
| **description-lambda** | description, description-feedback | Gemini API key | chat, filehandler, S3, DynamoDB |
| **download-lambda** | download | None (no AI, no Instagram) | S3, DynamoDB, zstd |
| **publish-lambda** | publish-create-containers, publish-check-video, publish-finalize | Instagram token + user ID | instagram, S3 (presign), DynamoDB |

### 2. Merge enhancement-feedback into enhance-lambda

Enhancement-feedback performs the same Gemini image editing as the initial enhancement pipeline. Merging it into the existing `enhance-lambda` consolidates all photo enhancement logic in one Lambda, shares the cold-started Gemini client, and eliminates one Lambda from the total count.

### 3. Delete Worker Lambda

After the split, no handler types remain on the Worker Lambda. It is deleted entirely.

### 4. Create shared bootstrap package (`internal/lambdaboot`)

Extract common init patterns into a shared package:
- `InitAWS()` — load default AWS config
- `InitS3(cfg, bucketEnvVar)` — create S3 client + presigner
- `InitDynamo(cfg, tableEnvVar)` — create DynamoDB session store
- `LoadGeminiKey(cfg)` — fetch Gemini API key from SSM
- `LoadInstagramCreds(cfg)` — fetch Instagram token + user ID from SSM

### 5. Split `internal/chat` into sub-packages

| Package | Contents | Primary consumers |
|---------|----------|-------------------|
| `chat` (core) | `NewGeminiClient`, model selection, `DefaultModelName` | All AI Lambdas |
| `chat/triage` | `AskMediaTriage`, `TriageResult`, prompt builder | triage-lambda |
| `chat/selection` | `AskMediaSelectionJSON`, `AskPhotoSelection`, `SelectionResult` | selection-lambda |
| `chat/description` | `GenerateDescription`, `RegenerateDescription`, `DescriptionResult` | description-lambda |
| `chat/enhancement` | `RunFullEnhancement`, `ProcessFeedback`, `EnhancementState`, Imagen | enhance-lambda |
| `chat/video` | `EnhanceVideo`, `VideoEnhancementConfig` | video-lambda |

### 6. Split `internal/filehandler` into sub-packages

| Package | Contents | ffmpeg required? |
|---------|----------|-----------------|
| `filehandler` (core) | `MediaFile`, `MediaMetadata` interface, extension helpers, MIME types, `LoadMediaFile`, `GenerateThumbnail` | No (delegates to sub-packages) |
| `filehandler/image` | `ImageMetadata`, `ExtractImageMetadata` (imagemeta lib) | No |
| `filehandler/video` | `VideoMetadata`, `ExtractVideoMetadata`, `CompressVideoForGemini`, `ExtractFrames`, LUT | Yes |

### Lambda count: 8 → 11

| Before | After |
|--------|-------|
| API (media-lambda) | API (media-lambda) |
| Worker (worker-lambda) | *deleted* |
| Thumbnail (thumbnail-lambda) | Thumbnail (thumbnail-lambda) |
| Selection (selection-lambda) | Selection (selection-lambda) |
| Enhancement (enhance-lambda) | Enhancement (enhance-lambda) + feedback |
| Video (video-lambda) | Video (video-lambda) |
| Webhook (webhook-lambda) | Webhook (webhook-lambda) |
| OAuth (oauth-lambda) | OAuth (oauth-lambda) |
| | **Triage (triage-lambda)** — *new* |
| | **Description (description-lambda)** — *new* |
| | **Download (download-lambda)** — *new* |
| | **Publish (publish-lambda)** — *new* |

### API Lambda dispatch changes

| Operation | Before (DDR-050/052) | After (DDR-053) |
|-----------|---------------------|-----------------|
| Triage | SFN → Worker Lambda | SFN → triage-lambda (unchanged SFN, new Lambda target) |
| Description | `invokeWorkerAsync("description")` | `invokeAsync(descriptionLambdaArn, ...)` |
| Description feedback | `invokeWorkerAsync("description-feedback")` | `invokeAsync(descriptionLambdaArn, ...)` |
| Download | `invokeWorkerAsync("download")` | `invokeAsync(downloadLambdaArn, ...)` |
| Enhancement | SFN → enhance-lambda | SFN → enhance-lambda (no change) |
| Enhancement feedback | `invokeWorkerAsync("enhancement-feedback")` | `invokeAsync(enhanceLambdaArn, ...)` |
| Publish | SFN → Worker Lambda | SFN → publish-lambda (unchanged SFN, new Lambda target) |

## Rationale

- **Isolated CloudWatch log groups**: Each domain gets its own `/aws/lambda/<name>` log group. Searching for publish errors means looking at one log group, not filtering across all job types.
- **Independent sizing**: download-lambda can have 2 GB ephemeral storage without inflating triage-lambda's footprint. publish-lambda only needs 256 MB RAM.
- **Faster cold starts**: download-lambda's binary is ~8 MB (no genai SDK). publish-lambda's binary is ~10 MB (no genai SDK). Compare to the Worker Lambda's ~20 MB binary.
- **Least-privilege credentials**: download-lambda never touches Gemini or Instagram SSM params. publish-lambda never touches the Gemini API key.
- **Consistent architecture**: All long-running operations now have dedicated Lambdas (or Step Functions), matching the pattern established for selection, enhancement, and video.
- **Library readability**: 15-file, 2,900-line `chat` package becomes 6 focused sub-packages. Developers can navigate to `chat/triage/` instead of scanning 15 files.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Keep Worker Lambda with better log filtering | Doesn't solve blast radius, over-provisioning, or binary bloat |
| 3 Lambdas by credential group (Gemini, Instagram, none) | Still mixes unrelated domains (triage + description) in one Lambda |
| Lambda Layers for shared code | Go static binaries don't benefit from Lambda Layers (no shared .so) |
| Keep chat/filehandler as monolithic packages | 15+ files per package hinders navigation; consumers pull unnecessary deps |

## Consequences

**Positive:**
- 5 separate CloudWatch log groups for the 5 job domains
- Independent memory/timeout/storage sizing per Lambda
- Smaller binaries for download-lambda (~8 MB) and publish-lambda (~10 MB)
- Credentials loaded only where needed (least privilege)
- Shared bootstrap eliminates ~100 lines of duplicated init code per Lambda
- Chat and filehandler sub-packages improve code navigation

**Trade-offs:**
- 11 Lambdas to maintain (vs 8) — mitigated by shared bootstrap and parameterized Dockerfiles
- 4 additional ECR image tags to push during deploy
- API Lambda needs 3 new Lambda ARN environment variables
- CDK stack grows by ~80 lines for new Lambda definitions

## Implementation

### Phase 1: Lambda Split (this DDR)

| Component | Change |
|-----------|--------|
| `internal/lambdaboot/` | New shared bootstrap package |
| `cmd/triage-lambda/main.go` | New Lambda handler |
| `cmd/description-lambda/main.go` | New Lambda handler |
| `cmd/download-lambda/main.go` | New Lambda handler |
| `cmd/publish-lambda/main.go` | New Lambda handler |
| `cmd/enhance-lambda/main.go` | Add enhancement-feedback handler |
| `cmd/worker-lambda/` | Deleted |
| `cmd/media-lambda/dispatch.go` | Generalize `invokeWorkerAsync` → `invokeAsync(arn, ...)` |
| `cmd/media-lambda/globals.go` | Add `descriptionLambdaArn`, `downloadLambdaArn`, `enhanceLambdaArn`; remove `workerLambdaArn` |
| `cdk/lib/backend-stack.ts` | Add 4 new Lambdas, remove Worker, update SFN targets, update IAM |
| `Makefile` | Add push-triage, push-description, push-download, push-publish; remove push-worker |

### Phase 2: Library Sub-packages (follow-up)

The `chat` and `filehandler` packages have complex internal cross-references that require careful refactoring:
- `triage.go` depends on `selection.go` (`uploadVideoFile`) and `selection_prompt.go` (`formatVideoDuration`)
- `truncateString` (gemini_image.go) is used by 5 other files across all domain areas
- `gemini_image.go` and `imagen.go` are shared by enhancement, video, and description

The recommended approach for Phase 2:
1. Move shared helpers (`uploadVideoFile`, `formatVideoDuration`, `truncateString`) to the core `chat` package
2. Create sub-packages: `chat/triage`, `chat/selection`, `chat/description`, `chat/enhancement`, `chat/video`
3. Split `filehandler` into core/image/video sub-packages

| Component | Change |
|-----------|--------|
| `internal/chat/triage/` | New sub-package (from `chat/triage.go`) |
| `internal/chat/selection/` | New sub-package (from `chat/selection*.go`) |
| `internal/chat/description/` | New sub-package (from `chat/description.go`) |
| `internal/chat/enhancement/` | New sub-package (from `chat/enhancement*.go`, `gemini_image.go`, `imagen.go`) |
| `internal/chat/video/` | New sub-package (from `chat/video_enhance*.go`) |
| `internal/filehandler/image/` | New sub-package (from `image.go`) |
| `internal/filehandler/video/` | New sub-package (from `video*.go`) |

## Related Decisions

- [DDR-035](./DDR-035-multi-lambda-deployment.md): Multi-Lambda Deployment — the architecture being refined
- [DDR-050](./DDR-050-replace-goroutines-with-async-dispatch.md): Async Dispatch — introduced the Worker Lambda being split
- [DDR-052](./DDR-052-step-functions-polling-for-long-running-ops.md): Step Functions Polling — triage/publish pipelines now target new Lambdas
