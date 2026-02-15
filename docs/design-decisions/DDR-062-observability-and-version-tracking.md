# DDR-062: Observability Gaps and Version Tracking

**Date**: 2026-02-15  
**Status**: Accepted  
**Iteration**: N/A

## Context

Debugging `POST /api/triage/init` returning 404 required 30+ minutes of log diving because:

1. **No version identity anywhere** -- we couldn't determine if the deployed Lambda binary matched the repo code
2. **CloudFront masks API errors** -- 403/404 from API Gateway become `200 + index.html`, hiding the real error
3. **No API Gateway access logs** -- requests rejected by the JWT authorizer leave zero trace
4. **No route registration logging** -- cold start logs list state machines and buckets but not registered HTTP routes
5. **Middleware logs lack context** -- no request ID, no response content-type, no way to distinguish "mux 404" from "handler 404"
6. **Frontend errors lack context** -- no backend version, no request correlation ID

## Decision

Implement six observability improvements across backend, frontend, and infrastructure:

### 1. Version/Commit Identity via `-ldflags -X` (Option A)

Inject `commitHash` and `buildTime` into the Go binary via `-ldflags` during Docker build. Inject `VITE_COMMIT_HASH` during frontend build.

- **Backend**: `version.go` declares `commitHash` and `buildTime` vars, overridden by Dockerfile `ARG COMMIT_HASH` and `-ldflags`
- **Dockerfiles**: Both `Dockerfile.light` and `Dockerfile.heavy` accept `COMMIT_HASH` build arg
- **CodeBuild**: `build_image()` passes `--build-arg COMMIT_HASH=$COMMIT` to all images
- **Frontend**: `VITE_COMMIT_HASH` injected by CodeBuild from `CODEBUILD_RESOLVED_SOURCE_VERSION`

### 2. Version Exposure Points

- **Health endpoint** (`GET /api/health`): returns `commitHash` and `buildTime` in JSON
- **Cold-start log**: `StartupLogger` includes `commitHash` and `buildTime` in lambda identity dict
- **`X-App-Version` response header**: set on every API response via middleware
- **`X-Client-Version` request header**: sent by frontend on every API request
- **Frontend error messages**: include both backend and client version for skew detection

### 3. Logging Improvements

- **Route registration logging**: log all registered HTTP routes at cold start (INFO level)
- **Enhanced request middleware**: log `requestId`, `contentType`, `clientVersion` on every request
- **Mux-404 catch-all**: explicit fallback handler at `/` logs unmatched routes at WARN level
- **API Gateway access logging**: structured JSON access logs capturing auth errors, throttling, routing failures

### 4. CloudFront Error Response Fix

Replace distribution-level `errorResponses` (which masked API 403/404 as `200 + index.html`) with a **CloudFront Function** on the default behavior (S3 origin) only. The function rewrites non-file SPA routes to `/index.html` at viewer-request time. The `/api/*` behavior is completely unaffected.

## Rationale

- **Option A (`-ldflags`)** chosen over `runtime/debug.ReadBuildInfo()` (requires `.git` in Docker context), SSM Parameter (cold-start latency + race condition), and Lambda env var (triggers cold start on update)
- **CloudFront Function** chosen over Lambda@Edge (adds latency and cost) and custom error pages (can't conditionally route)
- **No new log levels needed** -- the issue was missing log points, not verbosity. All improvements use existing INFO/DEBUG/WARN levels.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| `runtime/debug.ReadBuildInfo()` | Requires `COPY .git` in Dockerfile (bloats image), `go build -trimpath` may strip VCS info, doesn't help frontend |
| SSM Parameter for commit hash | Adds ~50ms cold-start latency, race condition where SSM updates before image is live |
| Lambda env var for commit hash | Env var change triggers Lambda cold start (disruptive), doesn't help frontend |
| Lambda@Edge for SPA routing | Adds latency and cost, more complex than CloudFront Functions |
| Distribution-level errorResponses | Masks API errors from all origins including `/api/*` (the root cause of the debugging issue) |

## Consequences

**Positive:**
- Every cold start log is self-identifying: `{"lambda": {"commitHash": "2c8207f", "buildTime": "20260215T...", ...}}`
- `curl /api/health` shows exact commit — quick verification from anywhere
- Browser DevTools shows `X-App-Version` on every response
- API errors from `/api/*` are no longer masked by CloudFront
- API Gateway access logs catch auth rejections before they reach Lambda
- Unmatched routes are logged explicitly at WARN level
- Frontend error messages include both versions for instant skew detection

**Trade-offs:**
- Dockerfile and pipeline changes required (one-time)
- Slightly larger response headers (`X-App-Version`)
- Additional CloudWatch log group for API Gateway access logs (cost)
- CloudFront Function has limited runtime (1ms compute, 10KB code) — but SPA routing logic is trivial

## What Each Improvement Catches

| Improvement | What it catches |
|-------------|-----------------|
| Route registration log | "Route /api/triage/init is not in the registered routes list" — instant root cause |
| Commit hash in cold start | "Lambda running commit abc1234, but repo HEAD is 2c8207f" — version mismatch |
| Health endpoint version | `curl /api/health` shows commit — quick check from anywhere |
| X-App-Version header | Browser DevTools shows backend version on every request |
| X-Client-Version header | Backend logs detect frontend/backend version skew |
| API Gateway access logs | Catches JWT rejections, throttling, routing errors before Lambda |
| Mux-404 catch-all | Distinguishes "no route matched" from "handler returned 404" |
| CloudFront Function | API errors are passed through correctly instead of masked as 200+HTML |
| Frontend error enrichment | Error messages include both frontend and backend version |

## Related Documents

- [DDR-028](./DDR-028-security-hardening.md) — Security hardening (origin-verify, JWT authorizer)
- [DDR-035](./DDR-035-multi-lambda-deployment.md) — Multi-Lambda deployment (Docker builds)
- [DDR-051](./DDR-051-comprehensive-logging-overhaul.md) — Comprehensive logging overhaul
