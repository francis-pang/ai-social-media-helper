# DDR-027: Container Image Lambda for Local OS Command Dependencies

**Date**: 2026-02-07  
**Status**: Accepted  
**Iteration**: 15

## Context

DDR-026 deployed the Lambda function as a zip-based package using the `provided.al2023` runtime. This works for image-only triage, but DDR-026 explicitly notes:

> "Video triage is not supported in Lambda (no `ffmpeg`/`ffprobe`) — images only for now"

The codebase depends on four local OS commands executed via `exec.Command`:

| Command | File | Purpose |
|---------|------|---------|
| `ffmpeg` | `internal/filehandler/video_compress.go` | AV1+Opus video compression for Gemini upload (DDR-018) |
| `ffprobe` | `internal/filehandler/video.go` | Video metadata extraction: GPS, timestamps, resolution, codec, device info (DDR-011) |
| `sips` | `internal/filehandler/directory.go` | macOS-only HEIC-to-JPEG thumbnail conversion |
| `gpg` | `internal/auth/auth.go` | API key decryption from encrypted credentials file (DDR-003) |

AWS Lambda's zip-based deployment has constraints that prevent bundling these binaries:

- **250MB unzipped deployment package limit** — a static ffmpeg build with AV1 support is ~100MB alone, leaving limited room for the Go binary and other dependencies
- **No pre-installed binaries** — the `provided.al2023` runtime contains a minimal Amazon Linux filesystem with no media processing tools
- **Read-only filesystem** — only `/tmp` is writable (configurable up to 10GB)

### The Pure Go Library Alternative

A zip-based deployment using only Go library dependencies was evaluated as an alternative. This would replace `exec.Command` calls with pure Go implementations:

- **ffprobe replacement**: Use `abema/go-mp4` to parse MP4 box trees directly. However, this is a low-level atom parser requiring manual implementation of vendor-specific GPS extraction — Apple's `©xyz` atom (ISO 6709), Samsung's `com.android.gps_latitude`/`com.android.gps_longitude`, DJI custom atoms, and GoPro GPMF telemetry. This is the exact complexity cited in DDR-011 as the reason ffprobe was chosen over pure Go libraries.

- **ffmpeg replacement**: No pure Go AV1 encoder exists. SVT-AV1 (`libsvtav1`) is hand-optimized C + assembly — no one has reimplemented this in Go. The only Go option is CGO bindings via `go-astiav` (wrapping libav C libraries), which requires `CGO_ENABLED=1`, a C cross-compilation toolchain, and bundling ~50-80MB of `.so` files in the zip. This partially defeats the purpose of a "pure Go" approach and introduces a fragile build pipeline. Dropping compression entirely contradicts DDR-018's rationale: a 600MB 4K video becomes 2MB with compression — without it, Gemini token costs increase ~300x and uploads take minutes instead of seconds.

The ~700ms cold start improvement from zip-based deployment (~300-500ms vs ~1-2s for container images) does not justify rewriting `video_compress.go` and `video.go`, losing AV1 support, and taking on ongoing maintenance of vendor-specific metadata parsing.

## Decision

### 1. Container Image Lambda

Deploy the Lambda function as a **custom Docker container image** instead of a zip package. The container bundles the Go binary alongside statically-compiled ffmpeg and ffprobe binaries, preserving all existing `exec.Command` calls unchanged.

### 2. Multi-Stage Dockerfile with AWS Base Image

Use a multi-stage Dockerfile:

- **Build stage**: `golang:1.24` — compile the Go binary with `CGO_ENABLED=0` and `-ldflags="-s -w"` for a minimal static binary
- **Runtime stage**: `public.ecr.aws/lambda/provided:al2023` — AWS's own base image, which is proactively cached on Lambda workers for faster cold starts

Copy statically-linked ffmpeg and ffprobe binaries (with `libsvtav1` and `libopus` compiled in) into the runtime image. The `mwader/static-ffmpeg` project provides pre-built static binaries with AV1+Opus support.

```dockerfile
# Stage 1: Build Go binary
FROM golang:1.24 AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /handler ./cmd/media-lambda

# Stage 2: Runtime with ffmpeg
FROM public.ecr.aws/lambda/provided:al2023
COPY --from=mwader/static-ffmpeg:7.1 /ffmpeg /usr/local/bin/
COPY --from=mwader/static-ffmpeg:7.1 /ffprobe /usr/local/bin/
COPY --from=builder /handler /var/runtime/bootstrap
ENTRYPOINT ["/var/runtime/bootstrap"]
```

### 3. FFmpeg for sips Replacement

Replace the macOS-only `sips` command in `generateThumbnailSips()` with ffmpeg for HEIC-to-JPEG thumbnail conversion. Since ffmpeg is already bundled in the container image, no additional dependencies are needed. The function uses `ffmpeg -i input.heic -vf "scale='min(1024,iw)':-2" -frames:v 1 output.jpg`.

Pure Go HEIC libraries (e.g. `github.com/jdeng/goheif`, `github.com/adrium/goheif`) were considered but rejected because they wrap the C-based `libde265` decoder via CGO, requiring `CGO_ENABLED=1` — which contradicts the static binary build strategy (`CGO_ENABLED=0`). Using ffmpeg keeps the build clean and avoids adding a CGO dependency.

### 4. GPG Already Replaced (DDR-025)

The `gpg` dependency for API key decryption is already replaced by AWS SSM Parameter Store in the Lambda binary (`cmd/media-lambda/main.go`), as decided in DDR-025. No additional changes needed.

### 5. Cold Start Minimization

Container image cold starts are mitigated through six strategies applied by default:

1. **AWS base image** (`provided:al2023`) — proactively cached on Lambda workers. Despite being larger than Alpine, it cold-starts faster because chunks are already on the worker.
2. **Static Go binary with stripped symbols** — `CGO_ENABLED=0` + `-ldflags="-s -w"` produces a ~15-20MB binary with no libc dependency.
3. **Multi-stage build** — only the Go binary and static ffmpeg binaries end up in the final image. No build tools, source code, or Go SDK.
4. **Stable layers first, code last** — ffmpeg binaries (rarely change) are copied before the Go binary (changes every deploy). Lambda's chunk-level caching keeps ffmpeg cached across deployments.
5. **Eager init** — AWS SDK clients (`s3.Client`, `ssm.Client`) created in `init()` so they're ready before the first request.
6. **Adequate memory** — 2048-3008MB provides sufficient CPU (Lambda allocates CPU proportional to memory) for both fast init and fast ffmpeg execution.

AWS Lambda implements block-level demand loading with convergent encryption and a multi-tier cache (on-worker, AZ-level, S3). According to AWS research, 67% of chunks are served from on-worker caches, and 80% of new container images contain zero unique bytes compared to what Lambda has already seen. With these strategies, real-world cold starts are expected to be ~1-2 seconds.

**SnapStart** is not available for Go (supports Java, Python, .NET only) and there are no announced plans to add Go support.

**Provisioned Concurrency** (~$11/month per instance at 1GB) eliminates cold starts entirely but is unnecessary for a personal-use tool. Can be added later if latency becomes a concern.

## Rationale

### Why Container Image over Lambda Layers?

Lambda Layers can bundle static binaries (limited to 250MB total across all layers), but:

- Pre-built ffmpeg Lambda Layer binaries typically lack `libsvtav1` (AV1 support). Custom compilation would be needed regardless.
- Container images provide **full control over the Go compiler version** — use `golang:1.24` in the build stage without depending on AWS-managed runtimes.
- A single container image bundles everything (Go binary + ffmpeg + ffprobe) with a reproducible Dockerfile. No separate layer management.

### Why Container Image over Pure Go Libraries (Zip)?

| Criteria | Container Image | Zip (Pure Go) |
|----------|:-:|:-:|
| Cold start | ~1-2s | ~300-500ms |
| Code change for ffmpeg | None | Impossible without CGO or dropping compression |
| Code change for ffprobe | None | Major rewrite (vendor-specific GPS parsing) |
| AV1+Opus compression | Preserved exactly (DDR-018) | Not achievable in pure Go |
| Build simplicity | Medium (Dockerfile) | Simple if pure Go, complex if CGO |
| Deploy artifact size | ~200-400MB | ~15-30MB (pure Go), ~80-100MB (with CGO .so) |
| Maintainability | High (ffmpeg updates via Dockerfile) | Low (own all parsing/encoding logic) |
| Infrastructure | ECR repository + container-aware CDK | Simpler CDK, no ECR |

The container image approach preserves the proven AV1+Opus compression pipeline (DDR-018) and the battle-tested ffprobe metadata extraction (DDR-011) with zero code changes to the existing Go application logic.

### Why ffmpeg for HEIC instead of a Pure Go library?

Pure Go HEIC libraries (`goheif`, `adrium/goheif`) were initially considered for sips replacement. However, they all wrap the C-based `libde265` decoder via CGO, requiring `CGO_ENABLED=1`. This contradicts the static binary build strategy (`CGO_ENABLED=0 -ldflags="-s -w"`) and would introduce a C cross-compilation dependency. Since ffmpeg is already bundled in the container for video processing, using it for HEIC conversion adds zero new dependencies and keeps the Go build clean.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Zip + pure Go libraries | No pure Go AV1 encoder exists; ffprobe vendor-specific GPS parsing would require major rewrite of `video.go` (DDR-011 rationale) |
| Zip + CGO bindings (`go-astiav`) | Requires `CGO_ENABLED=1`, C cross-compilation toolchain, ~50-80MB of `.so` files in zip; fragile build pipeline; partially defeats "pure Go" purpose |
| Lambda Layer with static ffmpeg | Pre-built layers lack `libsvtav1`; still need custom compilation; less control over Go version than container image |
| Drop video compression (upload raw) | Contradicts DDR-018; 300x increase in Gemini token costs; uploads take minutes instead of seconds |
| AWS MediaConvert for compression | Asynchronous processing (submit job, poll/callback); adds AWS service dependency and async flow complexity; ~$0.024/min pricing |
| ECS/Fargate for ffmpeg processing | Additional infrastructure to manage; higher latency (container spin-up); more complex architecture for single-file workflows |
| Client-side compression (ffmpeg.wasm) | Browser AV1 support limited (Chrome only, not Safari); ffmpeg.wasm is ~25MB download; poor UX for large files; relies on user's device CPU |
| Pure Go HEIC library (`goheif`) | Requires CGO (`libde265` C decoder); contradicts `CGO_ENABLED=0` static binary strategy; adds C cross-compilation complexity |
| Alpine/Debian base image | Not proactively cached by Lambda; slower cold starts despite smaller image size |
| Provisioned Concurrency for cold starts | ~$11/month per instance; overkill for personal-use tool; can be added later if needed |

## Consequences

**Positive:**

- Video triage enabled in Lambda — removes the "images only" limitation from DDR-026
- **Zero code changes** to `video_compress.go` and `video.go` — existing `exec.Command("ffmpeg", ...)` and `exec.Command("ffprobe", ...)` calls work unchanged
- Full AV1+Opus compression pipeline preserved (DDR-018) — consistent token costs and upload performance
- Full ffprobe metadata extraction preserved (DDR-011) — vendor-specific GPS, timestamps, device info for Apple, Samsung, DJI, GoPro
- Full control over Go compiler version — not constrained by AWS-managed runtimes
- Reproducible builds — Dockerfile pins exact versions of Go, ffmpeg, and codec libraries
- HEIC thumbnail generation becomes cross-platform — uses ffmpeg instead of macOS-only `sips`, works on macOS, Linux, and Lambda
- Dockerfile-based ffmpeg updates — change one version number instead of recompiling Go code

**Trade-offs:**

- Container image cold start ~1-2s vs ~300-500ms for zip (mitigated by strategies above; acceptable for personal-use tool)
- Larger deployment artifact (~200-400MB vs ~15-30MB zip)
- Requires ECR repository for container image storage (minor infrastructure addition)
- CDK/SAM configuration must use container image deployment (`Code.fromEcrImage` or `DockerImageCode.fromImageAsset`) instead of `Code.fromAsset` with zip
- Docker required in CI/CD pipeline for building the container image
- `mwader/static-ffmpeg` is a third-party dependency — must verify it includes `libsvtav1` and `libopus`, and monitor for updates

## Implementation

### Changes to Existing Files

| File | Changes |
|------|---------|
| `internal/filehandler/directory.go` | Replace `generateThumbnailSips()` with `generateThumbnailHEIC()` using ffmpeg for cross-platform HEIC-to-JPEG conversion |

### New Files

| File | Purpose |
|------|---------|
| `cmd/media-lambda/Dockerfile` | Multi-stage build: Go compilation + ffmpeg bundling into `provided:al2023` base |

### CDK Changes (Deploy Repo: `ai-social-media-helper-deploy`)

The deploy repo currently uses zip-based Lambda deployment. Switching to container images requires changes to the backend stack, the pipeline stack, and IAM permissions.

#### `cdk/lib/backend-stack.ts`

| Change | Detail |
|--------|--------|
| Lambda construct | `lambda.Function` -> `lambda.DockerImageFunction` |
| Code source | `lambda.Code.fromAsset('../.build/lambda')` (zip) -> `lambda.DockerImageCode.fromImageAsset()` (container image) |
| Remove `runtime` property | No longer needed — the container's `ENTRYPOINT` replaces `PROVIDED_AL2023` + `handler: 'bootstrap'` |
| Remove `handler` property | Same reason — handled by `ENTRYPOINT ["/var/runtime/bootstrap"]` in Dockerfile |
| Memory | `1024` -> `2048` or `3008` MB (ffmpeg needs CPU, and Lambda allocates CPU proportional to memory) |
| Ephemeral storage | May need increase from `1024` MiB if processing large videos in `/tmp` |
| ECR repository | Add an `ecr.Repository` resource for storing the container image, with a lifecycle rule to keep only the last 5 images |

#### `cdk/lib/pipeline-stack.ts`

| Change | Detail |
|--------|--------|
| **Backend build step** | Replace Go compile + zip with Docker build + ECR push. Commands change from `go build && zip` to `docker build -t <ecr-uri>:latest . && docker push <ecr-uri>:latest` |
| **CodeBuild environment** | Add `privileged: true` to enable Docker-in-Docker for building container images |
| **CodeBuild ECR login** | Add `aws ecr get-login-password \| docker login --username AWS --password-stdin <account>.dkr.ecr.<region>.amazonaws.com` before `docker push` |
| **Deploy step** | Replace `aws lambda update-function-code --zip-file fileb://function.zip` with `aws lambda update-function-code --image-uri <ecr-uri>:latest` |
| **PipelineStackProps** | Pass ECR repository URI to the pipeline so the build and deploy steps can reference it |
| **IAM grants** | Grant CodeBuild project's role ECR push permissions (`ecr:BatchCheckLayerAvailability`, `ecr:PutImage`, `ecr:InitiateLayerUpload`, `ecr:UploadLayerPart`, `ecr:CompleteLayerUpload`, `ecr:GetAuthorizationToken`) — CDK can handle this automatically via `ecrRepo.grantPullPush(backendBuild.role)` |

#### IAM Policies (DDR-023)

The existing `AiSocialMedia-Infra-Core` policy already includes ECR bootstrap permissions (`ecr:CreateRepository`, `ecr:DescribeRepositories`, `ecr:SetRepositoryPolicy`, `ecr:PutLifecyclePolicy`, `ecr:GetAuthorizationToken`) with `Resource: "*"`. These are sufficient for CDK to create the ECR repository during `cdk deploy`.

The CodeBuild role's ECR push permissions are granted within the CDK stack (not via the IAM user policies), so DDR-023's policies do not need updating.

#### Files Unchanged in Deploy Repo

| File | Why Unchanged |
|------|---------------|
| `cdk/lib/storage-stack.ts` | S3 media bucket is independent of Lambda packaging format |
| `cdk/lib/frontend-stack.ts` | SPA hosting (S3 + CloudFront) is independent of Lambda packaging format |
| `cdk/bin/cdk.ts` | Stack dependency order (Storage -> Backend -> Frontend -> Pipeline) stays the same |
| `cdk/package.json` | `DockerImageFunction` and `ecr.Repository` are in `aws-cdk-lib` — no new dependencies |

### Unchanged Files (Application Repo)

| File | Why Unchanged |
|------|---------------|
| `internal/filehandler/video_compress.go` | `exec.Command("ffmpeg", ...)` works unchanged — ffmpeg is on PATH inside container |
| `internal/filehandler/video.go` | `exec.Command("ffprobe", ...)` works unchanged — ffprobe is on PATH inside container |
| `internal/auth/auth.go` | GPG already replaced by SSM in Lambda binary (DDR-025) |
| `internal/chat/*.go` | Triage and selection logic unchanged |

## Related Decisions

- [DDR-011](./DDR-011-video-metadata-and-upload.md): Video Metadata Extraction — ffprobe chosen over pure Go for vendor-specific GPS parsing
- [DDR-018](./DDR-018-video-compression-gemini3.md): Video Compression — AV1+Opus pipeline via ffmpeg with libsvtav1
- [DDR-023](./DDR-023-aws-iam-deployment-user.md): AWS IAM User and Scoped Policies — existing ECR bootstrap permissions sufficient for CDK
- [DDR-025](./DDR-025-ssm-parameter-store-secrets.md): SSM Parameter Store — replaces GPG for API key in Lambda
- [DDR-026](./DDR-026-phase2-lambda-s3-deployment.md): Phase 2 Lambda + S3 — initial zip-based deployment (images only)
