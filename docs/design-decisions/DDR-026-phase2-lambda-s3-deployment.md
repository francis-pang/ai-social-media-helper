# DDR-026: Phase 2 Lambda + S3 Cloud Deployment

**Date**: 2026-02-07  
**Status**: Accepted  
**Iteration**: 15

## Context

Phase 1 of the web UI ([DDR-022](./DDR-022-web-ui-preact-spa.md)) runs as a local Go binary with an embedded Preact SPA. The user opens `http://localhost:8080`, uses a native OS file picker to select media, and the Go server reads files directly from the local filesystem to send to Gemini for triage.

This local-only model has limitations:

1. **No mobile access** — the tool only runs on the machine with the media files
2. **No remote triage** — cannot triage files that have already been uploaded to cloud storage
3. **Tight coupling to desktop** — depends on native OS file picker (`zenity`), local filesystem paths, and a desktop browser

The [Phase 2 Remote Hosting plan](../PHASE2-REMOTE-HOSTING.md) identified AWS Lambda + S3 + CloudFront as the target architecture. This DDR documents the concrete implementation decisions made during the migration.

### Constraints

1. The existing `internal/` packages (`chat`, `filehandler`, `auth`) must be reused — the triage logic is already correct and tested
2. The Preact SPA must work in both local mode (Phase 1) and cloud mode (Phase 2) from the same codebase
3. Lambda has a 6MB response payload limit — media files cannot be returned inline
4. Lambda has limited `/tmp` storage (configurable up to 10GB) — files must be downloaded from S3 before processing
5. The Gemini API key must not appear in plaintext in CloudFormation or the Lambda console (see [DDR-025](./DDR-025-ssm-parameter-store-secrets.md))
6. Video processing (`ffmpeg`/`ffprobe`) is not available in the Lambda runtime — video triage is deferred

## Decision

### 1. New Lambda Entry Point: `cmd/media-lambda/main.go`

A separate binary from `cmd/media-web/`, purpose-built for Lambda execution. It uses the `aws-lambda-go-api-proxy` library (`httpadapter.NewV2`) to wrap a standard Go `http.ServeMux` for API Gateway HTTP API v2 payload format.

This approach was chosen over writing raw Lambda handler functions because:
- The `http.ServeMux` pattern is identical to the local server, making code review straightforward
- The proxy library handles event-to-request translation transparently
- Standard Go HTTP middleware (logging, CORS) works unchanged

### 2. S3-Based Storage with Presigned URLs

Media files are uploaded directly from the browser to S3 using presigned PUT URLs, bypassing Lambda's 6MB payload limit entirely.

**Upload flow:**

```
Browser                    Lambda                       S3
  |                          |                           |
  |-- GET /api/upload-url -->|                           |
  |<-- presigned PUT URL ----|                           |
  |                          |                           |
  |---------- PUT file (presigned) --------------------->|
  |<--------- 200 OK -----------------------------------|
  |                          |                           |
  |-- POST /api/triage/start (sessionId) -->|            |
  |                          |-- ListObjects ----------->|
  |                          |<-- object list -----------|
  |                          |-- GetObject (each) ------>|
  |                          |   (download to /tmp)      |
  |                          |-- AskMediaTriage -------->| (Gemini)
```

**Session-based grouping:** Each upload session gets a UUID. Files are stored at `s3://{bucket}/{sessionId}/{filename}`. The triage start request passes only the `sessionId`; the Lambda lists objects with that prefix.

**Why presigned URLs over multipart upload through Lambda:**
- No 6MB payload limit constraint
- No Lambda compute time spent on file transfer
- Browser can show upload progress via XHR `onprogress`
- S3 handles concurrent uploads efficiently

### 3. Download-to-Tmp Processing Strategy

The Lambda downloads S3 objects to `/tmp/{sessionId}/` before processing. This allows the existing `filehandler.LoadMediaFile()` and `chat.AskMediaTriage()` functions to work unchanged — they expect local filesystem paths.

The Lambda is configured with 1GB ephemeral storage and 1GB memory to handle image processing (thumbnail generation, EXIF extraction).

After triage completes, the `/tmp/{sessionId}/` directory is cleaned up immediately.

### 4. CloudFront API Proxy (`/api/*` Behavior)

Rather than having the frontend call API Gateway directly (which requires CORS, a separate domain, and CSP `connect-src` allowlisting), we added a CloudFront additional behavior:

```
/api/*  -->  API Gateway HTTP API origin (https-only, no caching)
/*      -->  S3 frontend origin (OAC, cached)
/assets/* ->  S3 frontend origin (OAC, 1-year cache)
```

**Benefits:**
- **Same-origin requests** — no CORS headers needed between frontend and API
- **Tighter CSP** — `connect-src 'self'` instead of allowing `*.execute-api.*.amazonaws.com`
- **Simpler frontend** — API base URL is `""` (same domain) in both local and cloud modes
- **Single domain** — users interact with `d10rlnv7vz8qt7.cloudfront.net` for everything

**Implementation:** The frontend stack takes the API Gateway endpoint as a prop and extracts the domain using `Fn::Split`. The CloudFront behavior uses `CACHING_DISABLED` policy and `ALL_VIEWER_EXCEPT_HOST_HEADER` origin request policy to pass all request data through.

### 5. Dual-Mode Frontend with Build-Time Detection

The Preact SPA supports both local and cloud modes from the same codebase:

- **Local mode** (`VITE_CLOUD_MODE` not set): Shows `FileBrowser` component with native OS file picker via `/api/pick`
- **Cloud mode** (`VITE_CLOUD_MODE=1`): Shows `FileUploader` component with drag-and-drop and presigned URL upload

Detection is via `import.meta.env.VITE_CLOUD_MODE` — a Vite build-time constant that tree-shakes unused code paths.

### 6. Lambda API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/health` | Health check |
| `GET` | `/api/upload-url?sessionId=...&filename=...&contentType=...` | Presigned S3 PUT URL (15min expiry) |
| `POST` | `/api/triage/start` | Start triage from S3 session (`{sessionId, model?}`) |
| `GET` | `/api/triage/{id}/results` | Poll triage results |
| `POST` | `/api/triage/{id}/confirm` | Delete confirmed S3 objects (`{deleteKeys}`) |
| `GET` | `/api/media/thumbnail?key=...` | Download from S3, generate thumbnail, return bytes |
| `GET` | `/api/media/full?key=...` | Return presigned GET URL for full-resolution image |

### 7. CDK Infrastructure (Deploy Repo)

The infrastructure is defined in a separate repository (`ai-social-media-helper-deploy`) with four CDK stacks:

| Stack | Resources | Dependencies |
|-------|-----------|--------------|
| `AiSocialMediaStorage` | S3 media bucket (24h lifecycle, CORS for PUT) | None |
| `AiSocialMediaBackend` | Lambda (`provided.al2023`, 1GB, 5min timeout), API Gateway HTTP API | Storage |
| `AiSocialMediaFrontend` | S3 frontend bucket, CloudFront (OAC, security headers, `/api/*` proxy) | Backend |
| `AiSocialMediaPipeline` | CodePipeline (GitHub source, Go + Node builds, S3 + Lambda deploy) | Frontend, Backend |

**Stack dependency change from Phase 1:** The frontend stack now depends on the backend stack (to get the API Gateway endpoint for the CloudFront proxy). Previously it was independent.

## Rationale

### Why a separate binary instead of a StorageProvider interface?

The original plan called for a `StorageProvider` interface to abstract local vs. S3 storage so the same handlers could serve both. In practice:

- The local handlers use OS-specific APIs (`zenity` file picker, `os.ReadDir`, `filepath.Walk`) that have no S3 equivalent
- The S3 handlers have unique concerns (presigned URLs, session-based object listing, `/tmp` cleanup) that don't map to a clean interface
- A shared interface would require either leaky abstractions or a lowest-common-denominator API that serves neither mode well
- Two separate, focused binaries are easier to understand, test, and maintain than one binary with polymorphic storage

The `internal/` packages (`chat.AskMediaTriage`, `filehandler.LoadMediaFile`, `filehandler.GenerateThumbnail`) are reused directly — the abstraction exists at the package level, not the handler level.

### Why `httpadapter.NewV2` instead of raw Lambda handlers?

The `aws-lambda-go-api-proxy` library translates API Gateway HTTP API v2 events into standard `http.Request` objects. This means:

- Handlers are testable with `httptest` (standard Go HTTP testing)
- The same `http.ServeMux` routing patterns work in both Lambda and local development
- Middleware (logging, CORS) is reusable
- No Lambda-specific event parsing in handler code

### Why CloudFront proxies API instead of direct API Gateway calls?

Cross-origin API calls require CORS headers, a wider CSP `connect-src` directive, and the frontend needs to know the API Gateway URL at build time. By proxying `/api/*` through CloudFront:

- The frontend makes same-origin requests (no CORS)
- CSP is tighter (`connect-src 'self'` plus S3 for presigned uploads)
- The API base URL is `""` in both local and cloud modes
- One domain for everything (simpler mental model)

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| StorageProvider interface | Leaky abstraction; local and cloud handlers have fundamentally different concerns (see Rationale) |
| Raw Lambda handler functions | Lose standard `http.Request` testing, middleware, and routing; handlers become tightly coupled to AWS event format |
| API Gateway Lambda proxy (REST API v1) | HTTP API v2 is newer, cheaper ($1.00/million vs $3.50/million), lower latency, and supports payload format 2.0 natively |
| Frontend calls API Gateway directly | Requires CORS, wider CSP, `VITE_API_BASE_URL` build variable, cross-origin cookies complexity |
| Upload through Lambda (multipart) | 6MB payload limit, Lambda compute time wasted on file transfer, no upload progress |
| EFS mount for Lambda storage | Adds VPC requirement, NAT Gateway cost, slower cold starts; `/tmp` is sufficient for session-scoped processing |

## Consequences

**Positive:**

- Full triage workflow works remotely — upload from any browser, AI evaluates, confirm deletions
- Same Gemini triage logic (unchanged `chat.AskMediaTriage`) ensures parity with local results
- Presigned URL upload handles files of any size without Lambda payload limits
- CloudFront proxy eliminates CORS complexity and tightens CSP
- Local Phase 1 mode continues working unchanged (same codebase, different build flag)
- Lambda cold start is fast (~290ms including SSM parameter fetch) due to Go's compiled binary
- S3 media bucket has 24h lifecycle — uploaded files auto-delete, no manual cleanup needed
- CodePipeline automates future deployments from `main` branch pushes

**Trade-offs:**

- Video triage is not supported in Lambda (no `ffmpeg`/`ffprobe`) — images only for now
- Lambda concurrency: triage jobs run in-memory with goroutines; each Lambda invocation handles one session (no cross-invocation state sharing)
- `/tmp` storage is ephemeral — if Lambda is recycled mid-triage, the job fails (mitigated by 5-minute timeout)
- Two separate binaries (`media-web` and `media-lambda`) to maintain, though they share `internal/` packages
- Frontend has two UI paths (FileBrowser vs FileUploader) — slightly more code to maintain

## Implementation

### New Files

| File | Purpose |
|------|---------|
| `cmd/media-lambda/main.go` | Lambda entry point: S3-based handlers, presigned URLs, SSM API key loading |
| `web/frontend/src/components/FileUploader.tsx` | Drag-and-drop file upload with presigned URL upload to S3 |
| `web/frontend/src/env.d.ts` | Vite environment variable type declarations |

### Modified Files

| File | Changes |
|------|---------|
| `go.mod` / `go.sum` | Added `aws-lambda-go`, `aws-lambda-go-api-proxy`, `aws-sdk-go-v2` (S3, SSM, config) |
| `web/frontend/src/app.tsx` | Added `uploadSessionId` signal; conditional rendering based on `isCloudMode` |
| `web/frontend/src/api/client.ts` | Added `getUploadUrl()`, `uploadToS3()`, `openFullImage()`, `isCloudMode` detection |
| `web/frontend/src/types/api.ts` | Added `UploadUrlResponse`, `FullImageResponse`, `key`, `sessionId`, `deleteKeys` fields |
| `web/frontend/src/components/SelectedFiles.tsx` | Handles both local paths and S3 session IDs |
| `web/frontend/src/components/TriageView.tsx` | Uses `itemId()` helper for cloud/local; presigned URL for full image in cloud mode |

### CDK Changes (Deploy Repo)

| File | Changes |
|------|---------|
| `cdk/lib/backend-stack.ts` | `provided.al2023` runtime, `Code.fromAsset`, 1GB memory, 5min timeout, SSM IAM policy |
| `cdk/lib/frontend-stack.ts` | `FrontendStackProps.apiEndpoint`; `/api/*` CloudFront behavior; tightened CSP |
| `cdk/bin/cdk.ts` | Reordered: Storage -> Backend -> Frontend -> Pipeline; pass `apiEndpoint` to frontend |

### Shared Code Reused (Unchanged)

| Package | Functions Used by Lambda |
|---------|--------------------------|
| `internal/chat` | `AskMediaTriage()`, `DefaultModelName` |
| `internal/filehandler` | `LoadMediaFile()`, `GenerateThumbnail()`, `SupportedImageExtensions`, `SupportedVideoExtensions`, `IsSupported()` |
| `internal/logging` | `Init()` |

## Related Decisions

- [DDR-022](./DDR-022-web-ui-preact-spa.md): Web UI with Preact SPA and Go JSON API (Phase 1 foundation)
- [DDR-023](./DDR-023-aws-iam-deployment-user.md): AWS IAM User and Scoped Policies for CDK Deployment
- [DDR-025](./DDR-025-ssm-parameter-store-secrets.md): SSM Parameter Store for Runtime Secrets

## Deployed Resources

| Resource | Identifier |
|----------|-----------|
| CloudFront distribution | `d10rlnv7vz8qt7.cloudfront.net` |
| API Gateway HTTP API | `obopiy55xg.execute-api.us-east-1.amazonaws.com` |
| Lambda function | `AiSocialMediaApiHandler` |
| Frontend S3 bucket | `ai-social-media-frontend-123456789012` |
| Media S3 bucket | `ai-social-media-uploads-123456789012` |
| Artifacts S3 bucket | `ai-social-media-artifacts-123456789012` |
| SSM parameter | `/ai-social-media/prod/gemini-api-key` |
| CodePipeline | `AiSocialMediaPipeline` |
