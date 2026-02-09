# Architecture Overview

## System Overview

The Gemini Media CLI is a collection of Go tools for analyzing, selecting, and enhancing photos and videos using Google's Gemini API. It runs in two modes: **local** (CLI + embedded web server) and **cloud** (AWS Lambda + S3 + CloudFront).

```mermaid
graph TD
    subgraph binaries [Binaries]
        MediaSelect["media-select\n(CLI)"]
        MediaTriage["media-triage\n(CLI)"]
        MediaWeb["media-web\n(local web server)"]
        MediaLambda["media-lambda\n(AWS Lambda)"]
    end

    subgraph internal [Shared Packages - internal/]
        Auth["auth\n(API key, Cognito)"]
        Chat["chat\n(Gemini API: selection,\ntriage, enhancement)"]
        FileHandler["filehandler\n(EXIF, thumbnails,\ncompression)"]
        Logging["logging\n(zerolog)"]
        Assets["assets\n(prompts, reference photos)"]
        Store["store\n(DynamoDB sessions)"]
        Jobs["jobs\n(job routing)"]
        Instagram["instagram\n(publishing client)"]
    end

    subgraph frontend [Frontend]
        PreactSPA["Preact SPA\n(TypeScript + Vite)"]
    end

    MediaSelect --> Chat
    MediaSelect --> FileHandler
    MediaTriage --> Chat
    MediaTriage --> FileHandler
    MediaWeb --> Chat
    MediaWeb --> FileHandler
    MediaWeb --> PreactSPA
    MediaLambda --> Chat
    MediaLambda --> FileHandler
    MediaLambda --> Store
    MediaLambda --> Instagram
    PreactSPA --> MediaWeb
    PreactSPA --> MediaLambda
```

## Tech Stack

| Component | Technology |
|-----------|-----------|
| Language | Go 1.24 |
| AI Model | Gemini 3 (Flash for triage, Pro for selection/enhancement) |
| SDK | `google.golang.org/genai` |
| CLI Framework | `github.com/spf13/cobra` |
| Logging | `github.com/rs/zerolog` |
| Web Frontend | Preact 10 + Vite 6 + TypeScript |
| AWS SDK | `aws-sdk-go-v2` (S3, SSM, DynamoDB) |
| Lambda Adapter | `aws-lambda-go-api-proxy` (HTTP API v2 to `http.ServeMux`) |

## Local Architecture

In local mode, `media-web` serves the Preact SPA via `go:embed` and exposes a JSON REST API on `localhost:8080`. Media files are read from the local filesystem.

```mermaid
graph LR
    Browser["Browser"]
    GoServer["Go HTTP Server\n(media-web)"]
    EmbedFS["Embedded SPA\n(embed.FS)"]
    JSONAPI["JSON REST API\n(/api/browse, /api/triage/*)"]
    LocalFS["Local Filesystem"]
    GeminiAPI["Gemini API"]

    Browser --> GoServer
    GoServer --> EmbedFS
    GoServer --> JSONAPI
    JSONAPI --> LocalFS
    JSONAPI --> GeminiAPI
```

The JSON-only API design enabled the Phase 2 migration to Lambda without changing the frontend. See [DDR-022](./design-decisions/DDR-022-web-ui-preact-spa.md).

## Cloud Architecture

In cloud mode, the Preact SPA is hosted on CloudFront (S3 origin), the Go backend runs as Lambda functions behind API Gateway, and media files are stored in S3 with presigned URL uploads.

```mermaid
graph TD
    Browser["Browser\n(Preact SPA)"]

    subgraph cloudfront [CloudFront]
        DefaultBehavior["/* -> S3 origin\n(SPA static assets, OAC)"]
        APIBehavior["/api/* -> API Gateway\n(same-origin proxy)"]
    end

    subgraph aws [AWS Backend]
        APIGW["API Gateway HTTP API\n(JWT authorizer via Cognito)"]
        APILambda["API Lambda\n(256MB, 30s)"]
        ThumbLambda["Thumbnail Lambda\n(512MB, 2min)"]
        SelectionLambda["Selection Lambda\n(4GB, 15min)"]
        EnhancementLambda["Enhancement Lambda\n(2GB, 5min)"]
        VideoLambda["Video Lambda\n(4GB, 15min)"]
        StepFn["Step Functions\n(SelectionPipeline,\nEnhancementPipeline)"]
        DynamoDB["DynamoDB\n(session state, TTL 24h)"]
    end

    S3Media["S3 Media Bucket\n(24h auto-expiration)"]
    S3Frontend["S3 Frontend Bucket"]
    GeminiAPI["Gemini API"]
    SSM["SSM Parameter Store\n(API keys, credentials)"]
    Cognito["Cognito User Pool"]

    Browser --> cloudfront
    DefaultBehavior --> S3Frontend
    APIBehavior --> APIGW
    APIGW --> Cognito
    APIGW --> APILambda
    APILambda --> StepFn
    APILambda --> DynamoDB
    APILambda --> S3Media
    StepFn --> ThumbLambda
    StepFn --> SelectionLambda
    StepFn --> EnhancementLambda
    StepFn --> VideoLambda
    ThumbLambda --> S3Media
    SelectionLambda --> GeminiAPI
    EnhancementLambda --> GeminiAPI
    VideoLambda --> S3Media
    APILambda --> SSM
    Browser -->|"presigned PUT"| S3Media
```

### Key Design Decisions

1. **Presigned URL uploads** — browser uploads directly to S3, bypassing Lambda's 6MB payload limit
2. **CloudFront API proxy** — `/api/*` requests are proxied to API Gateway, making all requests same-origin (no CORS needed)
3. **Download-to-tmp** — Lambda downloads S3 objects to `/tmp` so existing `filehandler` and `chat` packages work unchanged
4. **Separate binary** — `media-lambda` is purpose-built for Lambda; different I/O patterns than `media-web`
5. **Build-time mode detection** — `VITE_CLOUD_MODE` flag switches between local file picker and S3 drag-and-drop uploader

See [DDR-026](./design-decisions/DDR-026-phase2-lambda-s3-deployment.md) for the full cloud migration decision.

## Multi-Lambda Architecture

Processing steps that exceed API Gateway's 30-second timeout use AWS Step Functions for parallel orchestration:

| Lambda | Purpose | Container | Memory | Timeout |
|--------|---------|-----------|--------|---------|
| API | HTTP API, DynamoDB, presigned URLs, start Step Functions | Light | 256 MB | 30s |
| Thumbnail | Per-file thumbnail generation | Heavy (ffmpeg) | 512 MB | 2 min |
| Selection | Gemini AI media selection | Heavy (ffmpeg) | 4 GB | 15 min |
| Enhancement | Per-photo Gemini image editing | Light | 2 GB | 5 min |
| Video | Per-video ffmpeg enhancement | Heavy (ffmpeg) | 4 GB | 15 min |

"Light" images (~55 MB) contain only the Go binary. "Heavy" images (~175 MB) include ffmpeg. Both share base Docker layers for efficient ECR storage. See [DDR-035](./design-decisions/DDR-035-multi-lambda-deployment.md) and [docker-images.md](./docker-images.md).

## Security Architecture

Defense-in-depth with multiple layers. See [DDR-028](./design-decisions/DDR-028-security-hardening.md).

```mermaid
flowchart LR
    Browser --> CloudFront
    CloudFront -->|"x-origin-verify header"| APIGW["API Gateway"]
    APIGW -->|"JWT Authorizer (Cognito)"| Lambda
    Lambda -->|"Origin verify middleware\nInput validation\nContent-type allowlist\nRandom job IDs"| Processing["Process Request"]
```

| Layer | Control |
|-------|---------|
| CloudFront | Origin-verify header, response security headers (CSP, HSTS) |
| API Gateway | JWT authorizer (Cognito), throttling (100 burst / 50 rps), CORS |
| Lambda | Origin-verify middleware, input validation, content-type allowlist, safe error messages |
| S3 | CORS locked to CloudFront domain, OAC (no public access) |

## Frontend Components

| Component | Mode | Purpose |
|-----------|------|---------|
| `LandingPage.tsx` | Cloud | Workflow chooser (triage vs selection) |
| `FileUploader.tsx` | Cloud (triage) | Drag-and-drop S3 upload |
| `MediaUploader.tsx` | Cloud (selection) | File System Access API pickers + trip context |
| `SelectionView.tsx` | Cloud (selection) | AI selection results + review with override |
| `EnhancementView.tsx` | Cloud (selection) | Photo enhancement with feedback loop |
| `PostGrouper.tsx` | Cloud (selection) | Drag-and-drop media grouping into posts |
| `DownloadView.tsx` | Cloud (selection) | ZIP bundle download |
| `DescriptionEditor.tsx` | Cloud (selection) | AI caption generation with feedback |
| `PublishView.tsx` | Cloud (selection) | Instagram publishing |
| `FileBrowser.tsx` | Local | Native OS file picker via Go backend |
| `TriageView.tsx` | Both | Triage results and deletion interface |
| `LoginForm.tsx` | Cloud | Cognito authentication UI |

## CI/CD

Two independent CodePipelines triggered by GitHub pushes to main:

| Pipeline | Flow |
|----------|------|
| Frontend | Preact build -> S3 sync -> CloudFront invalidation |
| Backend | 5 Docker builds (2 light + 3 heavy) -> 5 Lambda function updates |

## Related Documents

- [media-triage.md](./media-triage.md) — Triage workflow
- [media-selection.md](./media-selection.md) — Selection workflow
- [image-processing.md](./image-processing.md) — Image technical details
- [video-processing.md](./video-processing.md) — Video technical details
- [authentication.md](./authentication.md) — Credential management and Cognito auth
- [docker-images.md](./docker-images.md) — Docker image strategy and ECR layer sharing

---

**Last Updated**: 2026-02-09
