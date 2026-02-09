# DDR-035: Multi-Lambda Deployment Architecture

**Date**: 2026-02-08  
**Status**: Implementing  
**Iteration**: 17

## Context

The application has outgrown a single monolithic Lambda function. The current architecture uses one Lambda (`cmd/media-lambda`, 2GB memory, 5-minute timeout) to handle both fast API responses and long-running media processing (thumbnail generation, AI selection, photo enhancement, video processing). This creates several problems:

1. **API Gateway timeout**: API Gateway has a hard 30-second timeout. Long-running operations (Gemini AI selection can take minutes, ffmpeg video processing can take 10+ minutes) cannot run within an API request/response cycle.
2. **Over-provisioned API calls**: Simple operations (presigned URL generation, DynamoDB reads, status polling) use a 2GB Lambda because they share a container with ffmpeg and heavy processing code.
3. **No parallelism**: Processing N media files sequentially within one Lambda is slow. Generating thumbnails for 50 files could be 20x faster with parallel Lambda invocations.
4. **In-memory state**: Job state is stored in-memory (`var jobs = make(map[string]*triageJob)`), which is lost when Lambda containers are recycled.
5. **Single point of failure**: One OOM error or timeout in any processing step crashes the entire request.

The new multi-step media selection flow (Steps 1-8) requires persistent state, parallel processing, and operations that far exceed API Gateway's timeout.

## Decision

### 1. Five Specialized Lambda Functions

Split the monolithic Lambda into five functions, each with resource allocation matched to its workload:

| Lambda | Purpose | Memory | Timeout | Ephemeral | Container |
|--------|---------|--------|---------|-----------|-----------|
| `AiSocialMediaApiHandler` | HTTP API: DynamoDB R/W, presigned URLs, start Step Functions | 256 MB | 30s | 512 MB | Light |
| `AiSocialMediaThumbnailProcessor` | Per-file thumbnail generation (image resize, video frame extraction) | 512 MB | 2 min | 2 GB | Heavy |
| `AiSocialMediaSelectionProcessor` | Gemini AI media selection (all thumbnails + metadata) | 4 GB | 15 min | 4 GB | Heavy |
| `AiSocialMediaEnhancementProcessor` | Per-photo Gemini image editing with multi-turn feedback | 2 GB | 5 min | 2 GB | Light |
| `AiSocialMediaVideoProcessor` | Per-video ffmpeg enhancement | 4 GB | 15 min | 10 GB | Heavy |

The API Lambda memory drops from 2048 MB to 256 MB since it no longer does heavy processing. This reduces cost per API call by ~8x.

### 2. Per-Lambda Container Images with Two Parameterized Dockerfiles

Each Lambda gets its own container image containing exactly one Go binary. Images are built from two parameterized Dockerfiles using a `CMD_TARGET` build arg:

- **`Dockerfile.light`** — AL2023 base + single Go binary (no ffmpeg). Used for API Lambda and Enhancement Lambda.
- **`Dockerfile.heavy`** — AL2023 base + ffmpeg/ffprobe + single Go binary. Used for Thumbnail, Selection, and Video Lambdas.

Usage: `docker build --build-arg CMD_TARGET=enhance-lambda -f Dockerfile.light .`

Adding a new Lambda requires no Dockerfile changes — just a new `--build-arg` value.

**ECR layer sharing** keeps storage efficient. Two ECR repositories (`ai-social-media-lambda-light` and `ai-social-media-lambda-heavy`) maximize layer deduplication:

- All 5 images share the AL2023 base layer (~40 MB, stored once)
- 3 heavy images additionally share the ffmpeg layer (~120 MB, stored once)
- Each image's unique layer is just its Go binary (~15-20 MB)

Total ECR storage: ~235 MB unique layers x 5 image versions = ~1.2 GB = **~$0.12/month**

See [docker-images.md](../docker-images.md) for the full Docker image strategy documentation.

### 3. Two Step Functions State Machines

Step Functions orchestrates within-step parallel processing. User-driven step transitions (upload -> review -> enhance -> group -> publish) are NOT managed by Step Functions — those are controlled by the frontend via DynamoDB state.

**SelectionPipeline:**
- Map state: invoke Thumbnail Lambda per file (MaxConcurrency 20, retry 2x with backoff)
- Then: invoke Selection Lambda (timeout 900s, retry 1x)

**EnhancementPipeline:**
- Parallel state with two branches:
  - Branch 1: Map over photos -> Enhancement Lambda (MaxConcurrency 10, retry 2x)
  - Branch 2: Map over videos -> Video Lambda (MaxConcurrency 5, retry 1x)

Cost: ~$0.002 per session (~$0.60/month at 10 sessions/day).

### 4. DynamoDB Session State Store

Single-table design (`media-selection-sessions`) replaces in-memory state:

- Partition key: `PK` (string) — `SESSION#{sessionId}`
- Sort key: `SK` (string) — varies by record type (`META`, `SELECTION#{jobId}`, etc.)
- TTL: `expiresAt` — auto-delete old sessions after 24 hours
- Billing: PAY_PER_REQUEST (serverless, no capacity planning)

Cost: ~$0.001 per session.

### 5. Two Separate CodePipelines

Split the single CodePipeline into two independent pipelines:

- **Frontend Pipeline**: Source -> Preact SPA build (Node 22) -> S3 sync + CloudFront invalidation
- **Backend Pipeline**: Source -> 5 Docker builds (2 light + 3 heavy, parallel) -> 5 Lambda function updates

This means frontend-only changes (CSS, component logic, copy) do not trigger Docker builds, and backend-only changes do not trigger frontend rebuilds.

## Rationale

### Why per-Lambda images instead of shared multi-binary images?

Bundling multiple Go binaries into one container image wastes space — each Lambda carries binaries it never uses. As more Lambda functions are added, this gets worse. With Docker layer deduplication in ECR, per-Lambda images have virtually identical storage cost to shared images (~$0.12/month vs ~$0.11/month) because all images within an ECR repo share the same base layers.

### Why two Dockerfiles instead of one?

Docker's `COPY` instruction cannot be made conditional on a build arg without workarounds (multi-stage hacks or shell scripting in `RUN`). Two Dockerfiles cleanly separate "needs ffmpeg" from "doesn't need ffmpeg" with no tricks. Each Dockerfile is ~12 lines. Adding a new Lambda never requires changing either Dockerfile — just pass a different `CMD_TARGET`.

### Why two ECR repos instead of five?

ECR deduplicates layers within a single repository. Putting all light images in one repo and all heavy images in another maximizes layer sharing:

- Light repo: API + Enhancement images share the AL2023 base layer
- Heavy repo: Thumbnail + Selection + Video images share both AL2023 base AND ffmpeg layers

Five repos would mean zero layer sharing, tripling storage cost for heavy images.

### Why split pipelines instead of change detection?

Change detection (checking `git diff` to skip unchanged builds) adds complexity and fragile logic. Two pipelines are simpler, independently triggerable, and make it obvious what each pipeline does. The trade-off is two CodeStar connections watching the same repo, but this has no cost impact.

### Why 256 MB for API Lambda?

The API Lambda only does: DynamoDB reads/writes, presigned URL generation, Step Functions `StartExecution`, and status polling. All of these are network I/O operations that need minimal CPU and memory. 256 MB provides 1/6 of a vCPU, which is more than sufficient. Previous 2048 MB was only needed because the same Lambda ran ffmpeg.

### Why 4 GB for Selection and Video Lambdas?

- **Selection Lambda**: Must hold all thumbnails (50 files x 400KB = 20MB) plus compressed videos in memory, and construct a large Gemini API request. 4 GB provides ~2.5 vCPU.
- **Video Lambda**: ffmpeg transcoding is CPU-bound. Lambda allocates CPU proportional to memory — 4 GB provides ~2.5 vCPU. The 10 GB ephemeral storage handles large video files in `/tmp`.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Single container image for all Lambdas | Every Lambda carries ffmpeg (~120 MB) even if it doesn't need it; larger cold starts for API Lambda |
| Two shared images (light + heavy, multi-binary) | Each image carries binaries it doesn't use; grows linearly as Lambdas are added; ECR layer sharing makes per-Lambda images equally cheap |
| One parameterized Dockerfile with conditional ffmpeg | Docker `COPY` cannot be conditional on build args without multi-stage hacks; two clean Dockerfiles are simpler |
| Five ECR repositories (one per Lambda) | No layer sharing between images; 3x storage cost for heavy images compared to shared repo |
| Single pipeline with change detection | `git diff`-based skipping is fragile (shared internal packages affect all Lambdas); two pipelines are simpler and independently triggerable |
| Keep monolithic Lambda | API Gateway 30s timeout prevents long-running operations; 2 GB for simple API calls wastes money; no parallelism |
| ECS/Fargate instead of Lambda | Over-engineered for a personal project with bursty, infrequent usage; Lambda's pay-per-invocation model is far cheaper |

## Consequences

**Positive:**

- API responses are fast and cheap (256 MB Lambda, sub-second DynamoDB reads)
- Media processing runs in parallel (50 thumbnails generated simultaneously vs sequentially)
- Each Lambda is right-sized (no ffmpeg in API Lambda, no wasted memory)
- Step Functions provides built-in retry, concurrency throttling, and visual monitoring
- DynamoDB state persists across Lambda container recycling
- Adding new Lambda functions requires no Dockerfile changes
- Frontend and backend deploy independently (no unnecessary rebuilds)
- ECR layer sharing keeps storage cost minimal (~$0.12/month)

**Trade-offs:**

- More infrastructure to manage (5 Lambdas, 2 Step Functions, DynamoDB, 2 ECR repos, 2 pipelines)
- CDK stack complexity increases significantly
- IAM permissions must be configured per-Lambda (more granular but more verbose)
- 5 Docker builds per backend deploy (~15 minutes total, but parallel within CodeBuild)
- Step Functions adds ~$0.002 per session execution cost
- Cold starts for infrequently-used processing Lambdas (mitigated by Step Functions retry)

## Implementation

### Changes to Deploy Repo (`ai-social-media-helper-deploy`)

| File | Changes |
|------|---------|
| `cdk/lib/storage-stack.ts` | Add DynamoDB table (`media-selection-sessions`) with PK/SK, TTL, PAY_PER_REQUEST |
| `cdk/lib/backend-stack.ts` | Add 4 new Lambdas, second ECR repo, 2 Step Functions state machines, IAM per Lambda, environment variables |
| `cdk/lib/frontend-pipeline-stack.ts` | New: Frontend-only pipeline (Source -> Preact build -> S3 + CloudFront) |
| `cdk/lib/backend-pipeline-stack.ts` | New: Backend-only pipeline (Source -> 5 Docker builds -> 5 Lambda updates) |
| `cdk/lib/pipeline-stack.ts` | Deleted (replaced by two pipeline stacks) |
| `cdk/bin/cdk.ts` | Wire new stacks, add dependencies |
| `cdk/test/cdk.test.ts` | Update tests for new stack structure |

### Changes to Application Repo (`ai-social-media-helper`)

| File | Changes |
|------|---------|
| `cmd/media-lambda/Dockerfile.light` | New: Parameterized Dockerfile for Lambdas without ffmpeg |
| `cmd/media-lambda/Dockerfile.heavy` | New: Parameterized Dockerfile for Lambdas with ffmpeg |
| `cmd/media-lambda/Dockerfile` | Kept as-is for backward compatibility until pipelines switch over |
| `docs/docker-images.md` | New: Docker image strategy documentation |
| `docs/architecture.md` | Update cloud architecture section with multi-Lambda diagram |
| `README.md` | Update project structure and roadmap |

## Related Decisions

- [DDR-027](./DDR-027-container-image-lambda-local-commands.md): Container Image Lambda — current single-image deployment model being replaced
- [DDR-028](./DDR-028-security-hardening.md): Security Hardening — IAM permissions pattern extended to new Lambdas
- [DDR-030](./DDR-030-cloud-selection-backend.md): Cloud Selection Backend — backend logic being split into separate Lambdas
- [DDR-034](./DDR-034-download-zip-bundling.md): Download ZIP Bundling — download pipeline planned as third Step Functions state machine
