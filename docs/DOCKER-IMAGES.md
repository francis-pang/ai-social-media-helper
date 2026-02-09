# Docker Image Strategy

This document describes how Lambda container images are built, layered, and stored for the multi-Lambda architecture (DDR-035).

## Overview

The application uses **5 Lambda functions**, each deployed as its own container image. Images are built from **two parameterized Dockerfiles** and stored in **two ECR repositories**, using Docker layer deduplication to minimize storage cost.

## Dockerfiles

Both Dockerfiles accept a `CMD_TARGET` build argument that specifies which `cmd/` directory to build. Each invocation produces an image containing exactly **one Go binary**.

### `Dockerfile.light` — No ffmpeg

For Lambda functions that only need the Go binary (API handler, Enhancement processor):

```
┌──────────────────────────────────┐
│  Stage 1: golang:1.24 (builder)  │
│  COPY go.mod go.sum -> download  │
│  COPY . -> go build ./cmd/$TARGET│
├──────────────────────────────────┤
│  Stage 2: provided:al2023        │
│  COPY /handler -> /bootstrap     │
└──────────────────────────────────┘
```

Usage:

```bash
docker build --build-arg CMD_TARGET=media-lambda -f cmd/media-lambda/Dockerfile.light .
docker build --build-arg CMD_TARGET=enhance-lambda -f cmd/media-lambda/Dockerfile.light .
```

### `Dockerfile.heavy` — With ffmpeg

For Lambda functions that need ffmpeg/ffprobe for media processing (Thumbnail, Selection, Video):

```
┌──────────────────────────────────┐
│  Stage 1: golang:1.24 (builder)  │
│  COPY go.mod go.sum -> download  │
│  COPY . -> go build ./cmd/$TARGET│
├──────────────────────────────────┤
│  Stage 2: provided:al2023        │
│  COPY ffmpeg, ffprobe            │
│  COPY /handler -> /bootstrap     │
└──────────────────────────────────┘
```

Usage:

```bash
docker build --build-arg CMD_TARGET=thumbnail-lambda -f cmd/media-lambda/Dockerfile.heavy .
docker build --build-arg CMD_TARGET=selection-lambda -f cmd/media-lambda/Dockerfile.heavy .
docker build --build-arg CMD_TARGET=video-lambda -f cmd/media-lambda/Dockerfile.heavy .
```

## Layer Structure and Sharing

Docker images are composed of stacked layers. ECR deduplicates identical layers within a repository, storing each unique layer only once. This is why all images of the same type (light or heavy) are stored in the same ECR repository.

### Light Images (ECR: `ai-social-media-lambda-light`)

```
┌──────────────────────────────────────────────────────────────────────────┐
│ API Lambda Image                  │ Enhancement Lambda Image             │
├───────────────────────────────────┼──────────────────────────────────────┤
│ Layer 3: api-handler binary       │ Layer 3: enhance-handler binary      │
│          (~15 MB, UNIQUE)         │          (~15 MB, UNIQUE)            │
├───────────────────────────────────┴──────────────────────────────────────┤
│ Layer 1-2: public.ecr.aws/lambda/provided:al2023                        │
│            (~40 MB, SHARED — stored once in ECR)                        │
└─────────────────────────────────────────────────────────────────────────┘
```

**Storage**: 40 MB (base, once) + 15 MB x 2 (binaries) = **~70 MB unique**

### Heavy Images (ECR: `ai-social-media-lambda-heavy`)

```
┌───────────────────────────┬───────────────────────────┬──────────────────────────┐
│ Thumbnail Lambda Image    │ Selection Lambda Image    │ Video Lambda Image       │
├───────────────────────────┼───────────────────────────┼──────────────────────────┤
│ Layer 4: thumb-handler    │ Layer 4: select-handler   │ Layer 4: video-handler   │
│          (~15 MB, UNIQUE) │          (~18 MB, UNIQUE) │          (~15 MB, UNIQUE)│
├───────────────────────────┴───────────────────────────┴──────────────────────────┤
│ Layer 3: ffmpeg + ffprobe static binaries                                        │
│          (~120 MB, SHARED — stored once in ECR)                                  │
├──────────────────────────────────────────────────────────────────────────────────┤
│ Layer 1-2: public.ecr.aws/lambda/provided:al2023                                │
│            (~40 MB, SHARED — stored once in ECR)                                │
└──────────────────────────────────────────────────────────────────────────────────┘
```

**Storage**: 40 MB (base, once) + 120 MB (ffmpeg, once) + 15 MB x 3 (binaries) = **~205 MB unique**

### Why Layer Order Matters

In both Dockerfiles, the layers are ordered from most stable to least stable:

1. **Base image** (`provided:al2023`) — changes rarely (AWS updates)
2. **ffmpeg** (heavy only) — changes rarely (version bumps)
3. **Go binary** — changes on every code deploy

This means:

- When you deploy new code, only the Go binary layer (~15 MB) changes
- ECR push/pull only transfers the changed layer
- Lambda's image caching reuses the unchanged base + ffmpeg layers
- Cold starts are faster because most of the image is already cached on Lambda workers

## Container Registry Strategy (DDR-041)

Images are split across **ECR Private** (proprietary code) and **ECR Public** (generic utilities) to minimize cost while protecting sensitive business logic.

### ECR Private — Proprietary images ($0.10/GB/month)

| Repository | Images stored | Why private |
|---|---|---|
| `ai-social-media-lambda-light` | API | Auth, session management, prompt orchestration |
| `ai-social-media-lambda-heavy` | Selection | Proprietary AI selection algorithms and prompts |

### ECR Public — Generic images (free, 50 GB)

| Repository | Images stored | Why public |
|---|---|---|
| `public.ecr.aws/<alias>/lambda-light` | Enhancement | Generic Gemini API passthrough, no proprietary prompts |
| `public.ecr.aws/<alias>/lambda-heavy` | Thumbnail, Video | Generic ffmpeg processing, no business logic |

### Authentication

CI/CD requires two login commands:

```bash
# ECR Private (same region as deployment)
aws ecr get-login-password --region us-east-1 | docker login --username AWS --password-stdin <account-id>.dkr.ecr.us-east-1.amazonaws.com

# ECR Public (always us-east-1, regardless of deployment region)
aws ecr-public get-login-password --region us-east-1 | docker login --username AWS --password-stdin public.ecr.aws
```

### Tagging Convention

Each image is tagged with both the Lambda name and commit hash:

```
# Private
ai-social-media-lambda-light:api-abc1234
ai-social-media-lambda-light:api-latest
ai-social-media-lambda-heavy:select-abc1234
ai-social-media-lambda-heavy:select-latest

# Public
public.ecr.aws/<alias>/lambda-light:enhance-abc1234
public.ecr.aws/<alias>/lambda-light:enhance-latest
public.ecr.aws/<alias>/lambda-heavy:thumb-abc1234
public.ecr.aws/<alias>/lambda-heavy:thumb-latest
public.ecr.aws/<alias>/lambda-heavy:video-abc1234
public.ecr.aws/<alias>/lambda-heavy:video-latest
```

The `{name}-latest` tag is what Lambda functions reference. The `{name}-{commit}` tag provides traceability. ECR lifecycle rules keep only the last 5 images per tag pattern.

## Cost Analysis

With 5 image versions retained per tag (ECR lifecycle rule):

| Component | Size | Versions | Storage |
|---|---|---|---|
| AL2023 base (light repo) | 40 MB | 1 (deduplicated) | 40 MB |
| API binary | 15 MB | 5 | 75 MB |
| Enhancement binary | 15 MB | 5 | 75 MB |
| AL2023 base (heavy repo) | 40 MB | 1 (deduplicated) | 40 MB |
| ffmpeg layer | 120 MB | 1 (deduplicated) | 120 MB |
| Thumbnail binary | 15 MB | 5 | 75 MB |
| Selection binary | 18 MB | 5 | 90 MB |
| Video binary | 15 MB | 5 | 75 MB |
| **Total** | | | **~590 MB** |

### Private storage (paid)

| Component | Size | Versions | Storage |
|---|---|---|---|
| AL2023 base (light repo) | 40 MB | 1 (deduplicated) | 40 MB |
| API binary | 15 MB | 5 | 75 MB |
| AL2023 base (heavy repo) | 40 MB | 1 (deduplicated) | 40 MB |
| ffmpeg layer | 120 MB | 1 (deduplicated) | 120 MB |
| Selection binary | 18 MB | 5 | 90 MB |
| **Total private** | | | **~365 MB** |

### Public storage (free)

| Component | Size | Versions | Storage |
|---|---|---|---|
| Enhancement binary | 15 MB | 5 | 75 MB |
| Thumbnail binary | 15 MB | 5 | 75 MB |
| Video binary | 15 MB | 5 | 75 MB |
| Shared base + ffmpeg layers | ~160 MB | 1 | 160 MB |
| **Total public** | | | **~385 MB** (free) |

ECR Private pricing: **$0.10/GB/month** -> ~**$0.04/month** (down from ~$0.06/month with all-private)  
ECR Public pricing: **free** up to 50 GB storage

## Build Pipeline

The **Backend Pipeline** (CodePipeline) builds all 5 images in a single CodeBuild project:

```
Source (GitHub main)
  │
  ▼
Build (CodeBuild, privileged mode)
  ├── docker build --build-arg CMD_TARGET=media-lambda     -f Dockerfile.light  →  light:api-{commit}
  ├── docker build --build-arg CMD_TARGET=enhance-lambda   -f Dockerfile.light  →  light:enhance-{commit}
  ├── docker build --build-arg CMD_TARGET=thumbnail-lambda -f Dockerfile.heavy  →  heavy:thumb-{commit}
  ├── docker build --build-arg CMD_TARGET=selection-lambda -f Dockerfile.heavy  →  heavy:select-{commit}
  └── docker build --build-arg CMD_TARGET=video-lambda     -f Dockerfile.heavy  →  heavy:video-{commit}
  │
  ▼
Deploy (CodeBuild)
  ├── aws lambda update-function-code --function AiSocialMediaApiHandler           --image light:api-{commit}
  ├── aws lambda update-function-code --function AiSocialMediaEnhancementProcessor --image light:enhance-{commit}
  ├── aws lambda update-function-code --function AiSocialMediaThumbnailProcessor   --image heavy:thumb-{commit}
  ├── aws lambda update-function-code --function AiSocialMediaSelectionProcessor   --image heavy:select-{commit}
  └── aws lambda update-function-code --function AiSocialMediaVideoProcessor       --image heavy:video-{commit}
```

Docker's build cache means builds 2-5 reuse the Go module download layer from build 1. The `go mod download` step (~30s) only runs once; subsequent builds only re-run `go build` (~5-10s each).

## Adding a New Lambda

To add a new Lambda function:

1. Create `cmd/new-lambda/main.go` with the Lambda handler
2. Decide if it needs ffmpeg (most don't)
3. In the CDK backend stack, add a new Lambda function pointing to the appropriate ECR repo
4. In the backend pipeline build spec, add one line: `docker build --build-arg CMD_TARGET=new-lambda -f Dockerfile.{light|heavy} .`
5. In the backend pipeline deploy spec, add: `aws lambda update-function-code --function ... --image ...`

**No Dockerfile changes required.**

## Backward Compatibility

The original `Dockerfile` (used by the current single-Lambda pipeline) is preserved alongside the new parameterized Dockerfiles until the pipeline switch is complete:

```
cmd/media-lambda/
├── Dockerfile        # Original (DDR-027), used by current pipeline
├── Dockerfile.light  # New (DDR-035), parameterized, no ffmpeg
├── Dockerfile.heavy  # New (DDR-035), parameterized, with ffmpeg
└── main.go           # API Lambda entry point
```

## Related Documents

- [DDR-027](design-decisions/DDR-027-container-image-lambda-local-commands.md): Original container image Lambda deployment
- [DDR-035](design-decisions/DDR-035-multi-lambda-deployment.md): Multi-Lambda deployment architecture decision
- [DDR-041](design-decisions/DDR-041-container-registry-strategy.md): Container registry strategy (ECR Private + ECR Public)
- [ARCHITECTURE.md](ARCHITECTURE.md): Overall system architecture

---

**Last Updated**: 2026-02-09  
**Updated for**: DDR-041 (Container Registry Strategy — ECR Private + ECR Public)
