# DDR-051: Comprehensive Logging Overhaul for Production Troubleshooting

**Date**: 2026-02-09  
**Status**: Accepted  
**Iteration**: Phase 2 Cloud Deployment

## Context

The system operates without a staging or developmental environment — production is the only environment. With 8 Lambda functions, Step Functions orchestration, DynamoDB state management, and external API integrations (Gemini, Instagram), troubleshooting failures requires comprehensive log coverage to reconstruct what happened without attaching a debugger.

Prior state:

- ~200 log statements across ~45 Go files, heavily skewed to info/error
- Zero trace-level usage; debug-level was sparse
- No cold start detection or Lambda request ID correlation
- No timing for external service calls (Gemini, Instagram, DynamoDB, S3)
- Many silent `continue` and `return nil` paths with no log output
- `ConsoleWriter` output format not ideal for CloudWatch JSON ingestion
- Default log level was `info`, hiding diagnostic context
- The `handleEnhancementFeedback` worker had 7 silent `return nil` paths that swallowed errors

The aggressive CloudWatch log retention lifecycle policy already in place makes over-logging an acceptable trade-off for observability.

## Decision

Overhaul the logging infrastructure and add comprehensive structured logging across all Lambda functions and internal packages, following a strict log level policy. The changes are:

1. **Default log level changed from `info` to `debug`** — since production is the only environment, this provides full diagnostic visibility by default without the firehose of trace
2. **JSON output in Lambda** — detect `AWS_LAMBDA_FUNCTION_NAME` env var; use JSON for CloudWatch, ConsoleWriter for local CLI
3. **Trace level added** — available via `GEMINI_LOG_LEVEL=trace` but not enabled by default
4. **Logger helpers added** — `WithLambdaContext(ctx)` for request ID correlation, `WithJob(sessionId, jobId)` for job correlation
5. **Cold start detection** — every Lambda logs its first invocation with function name and Go version
6. **Init summaries** — every Lambda logs a config summary at init with enabled features and SSM load timings
7. **~550+ new log statements** — covering every handler, job lifecycle, external API call, DynamoDB operation, S3 operation, file processing step, and error path

## Rationale

| Factor | Decision |
|--------|----------|
| No staging environment | Default to `debug` — treat prod as dev |
| CloudWatch ingestion | JSON output for Lambda environments |
| Aggressive log retention | Over-logging is acceptable; err on the side of more data |
| First-24-hours troubleshooting | Every request/job should be reconstructable from logs alone |
| Silent error paths | Previously-silent `return nil` paths now log errors |
| External service calls | Every Gemini, Instagram, DynamoDB, and S3 call logged with timing |

### Log level policy

| Level | Question it answers | Applied to |
|-------|-------------------|------------|
| **Trace** | "What exactly is happening in this code path?" | DynamoDB PK/SK ops, marshal details, per-frame histograms, prompt building, form parameter names |
| **Debug** | "Why did this request/operation behave this way?" | API calls with timing, S3 ops, config values, handler entry, decision points, intermediate states |
| **Info** | "What is the system doing, at a business level?" | Cold starts, init summaries, job lifecycle, phase transitions, business events |
| **Warn** | "Something is off; it might become a problem." | Skipped files, fallback behavior, missing optional config, transient errors |
| **Error** | "A real failure occurred; behavior was affected." | Job failures, API failures, previously-silent error paths |
| **Fatal** | "The process cannot safely continue." | Required config missing at init (unchanged) |

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Keep default at `info`, enable `debug` per-Lambda via env var | Requires remembering to set env var before the issue occurs; production is the only environment so debug should always be on |
| Use AWS X-Ray tracing instead of logging | X-Ray provides latency tracing but not the decision-point narrative needed for troubleshooting complex AI pipelines and multi-step job processing |
| Add logging incrementally as issues arise | Reactive approach means first occurrence of an issue provides no diagnostic data; the 24-hour post-deploy window is the highest-risk period |
| Structured logging with `slog` (stdlib) | zerolog already established in codebase (DDR-002), zero-allocation performance, fluent API — no benefit to switching |

## Consequences

**Positive:**

- Every Lambda invocation produces a complete narrative: cold start -> init -> handler entry -> external calls -> decisions -> result
- Silent error paths in `handleEnhancementFeedback` now properly log Error (7 paths fixed)
- External service call timing enables identifying slow dependencies (Gemini API, Instagram API, DynamoDB)
- JSON output in Lambda enables CloudWatch Logs Insights queries on structured fields
- Cold start detection enables measuring and alerting on cold start frequency
- SSM parameter load timing reveals if secrets loading is a cold start bottleneck
- `requestId` correlation enables tracing a single API Gateway request across all log lines

**Trade-offs:**

- Log volume increases significantly (~3-4x more log statements) — mitigated by aggressive retention policy
- Debug-level default means CloudWatch costs may increase — acceptable for a single-environment setup
- Trace level (when enabled) can produce very high volume in video processing loops — only enable for targeted debugging sessions

## Implementation

| Component | Change |
|-----------|--------|
| `internal/logging/logger.go` | Add trace level, default to debug, JSON output for Lambda, `WithLambdaContext()` and `WithJob()` helpers |
| `cmd/media-lambda/main.go` | Cold start detection, init summary with feature flags, SSM timing |
| `cmd/media-lambda/middleware.go` | Per-request logging (method, path, status, duration), cold start, origin-verify outcome |
| `cmd/media-lambda/httputil.go` | JSON encode error logging, trace-level response logging |
| `cmd/media-lambda/*.go` (12 files) | Handler entry/exit, validation outcomes, DynamoDB reads, dispatch events |
| `cmd/worker-lambda/main.go` | Job lifecycle for all 6 types: phase transitions, S3 ops, file processing, error paths |
| `cmd/selection-lambda/main.go` | Handler timing, media index validation, Gemini call timing |
| `cmd/enhance-lambda/main.go` | Image dimensions, MIME type, Imagen config, thumbnail failures |
| `cmd/video-lambda/main.go` | Metadata extraction, enhancement config, file sizes |
| `cmd/thumbnail-lambda/main.go` | Unsupported file warnings, file sizes, timing |
| `cmd/webhook-lambda/main.go` | Init timing, SSM load timing |
| `cmd/oauth-lambda/main.go` | Request entry, SSM timing, token expiry |
| `internal/store/dynamo.go` | Trace for PK/SK ops, Debug for timing on all DynamoDB calls |
| `internal/store/dynamo_jobs.go` | Debug for all Get/Put job methods with status |
| `internal/chat/*.go` (12 files) | Gemini/Imagen API call timing, prompt sizes, response sizes, pipeline phases |
| `internal/instagram/client.go` | HTTP request/response logging, container creation, polling, publish |
| `internal/instagram/oauth.go` | Token exchange HTTP timing |
| `internal/filehandler/*.go` (7 files) | File loading, metadata extraction, ffmpeg commands, video processing |
| `internal/webhook/handler.go` | Request entry, verification, signature validation |
| `internal/auth/validate.go` | Validation timing and result |

**Final counts:** 757 log statements (up from ~200), plus 51 Fatal (unchanged).

| Level | Count | Percentage |
|-------|-------|------------|
| Trace | 16 | 2% |
| Debug | 314 | 41% |
| Info | 135 | 18% |
| Warn | 165 | 22% |
| Error | 76 | 10% |
| Fatal | 51 | 7% |

## Related Decisions

- [DDR-002](./DDR-002-logging-before-features.md): Logging Infrastructure First — established zerolog as the logging library
- [DDR-035](./DDR-035-multi-lambda-deployment.md): Multi-Lambda Deployment Architecture — defines the Lambda topology being logged
- [DDR-050](./DDR-050-replace-goroutines-with-async-dispatch.md): Replace Goroutines with Async Dispatch — defines the Worker Lambda job types being logged
