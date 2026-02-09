# DDR-050: Replace Background Goroutines with DynamoDB + Step Functions / Async Lambda

**Date**: 2026-02-09  
**Status**: Accepted  
**Iteration**: Phase 2 Cloud Deployment

## Context

The API Lambda (`cmd/media-lambda/`) runs all long-running operations as background goroutines:

```go
go runTriageJob(job, model)       // triage.go
go runSelectionJob(...)            // selection.go
go runEnhancementJob(...)          // enhancement.go
go runDescriptionJob(...)          // description.go
go runDownloadJob(...)             // download.go
go runPublishJob(...)              // publish.go
```

In AWS Lambda, the execution environment **freezes** between invocations. When a goroutine makes a network call (e.g. Gemini API, S3 upload) and the Lambda finishes responding to the HTTP request, AWS freezes the process. The goroutine's TCP connections stall, Gemini requests time out, and jobs get permanently stuck at "processing".

Meanwhile, the proper infrastructure for async processing **already exists but is unused**:

- **DynamoDB store** (`internal/store/`): Full CRUD for selection, enhancement, download, description, and publish jobs — but the API Lambda only uses in-memory maps
- **Step Functions**: `SelectionPipeline` and `EnhancementPipeline` deployed in CDK with dedicated high-resource Lambdas — but never started by the API Lambda
- **Dedicated Lambdas**: selection-lambda (4GB/15min), enhance-lambda (2GB/5min), thumbnail-lambda (512MB/2min), video-lambda (4GB/15min) — all deployed but only invokable via Step Functions
- **API Lambda permissions**: Already has `StartExecution` on both state machines, DynamoDB read/write, and env vars for ARNs

The API Lambda has **zero** DynamoDB or Step Functions integration — it only uses S3 and in-memory maps.

## Decision

Replace all six background goroutines with two dispatch patterns:

### Pattern 1: Step Functions (Selection, Enhancement)

Selection and Enhancement already have dedicated Step Functions state machines with specialized Lambdas. The API Lambda now:

1. Writes a pending job record to DynamoDB
2. Calls `sfn:StartExecution` with job parameters
3. Returns the job ID to the frontend immediately

The Step Functions pipeline invokes Thumbnail → Selection or Enhancement → Video Lambdas, which write results to DynamoDB. The API Lambda polls DynamoDB for status.

### Pattern 2: Async Worker Lambda (Triage, Description, Download, Publish)

A new Worker Lambda handles the four remaining job types that don't need Step Functions orchestration. The API Lambda:

1. Writes a pending job record to DynamoDB
2. Calls `lambda:Invoke` with `InvocationType=Event` (fire-and-forget)
3. Returns the job ID to the frontend immediately

The Worker Lambda processes the job and writes results to DynamoDB. The API Lambda polls DynamoDB for status.

### Worker Lambda Specifications

| Property | Value |
|----------|-------|
| Container | Light (Dockerfile.light, same as API Lambda) |
| CMD_TARGET | `worker-lambda` |
| Memory | 2048 MB |
| Timeout | 10 minutes |
| Ephemeral Storage | 2048 MB |
| Event Types | triage, description, description-feedback, download, publish, enhancement-feedback |

### Job State Flow

All jobs follow the same lifecycle, stored in DynamoDB:

```
pending → processing → complete
                    ↘ error
```

The API Lambda writes `pending`, the processing Lambda/Step Function updates to `processing` and then `complete` or `error`.

### New Store Type

`TriageJob` added to `internal/store/` following the existing single-table pattern (SK = `TRIAGE#{jobId}`).

## Rationale

- **Reliability**: DynamoDB survives Lambda container recycling, concurrent invocations, and deployments. In-memory maps do not.
- **Lambda freeze safety**: No background goroutines means no TCP connections to stall when Lambda freezes.
- **Existing infrastructure**: Step Functions and DynamoDB store are already deployed and tested — this change wires them together.
- **Minimal new infrastructure**: Only one new Lambda function (Worker) for four job types. Selection and Enhancement reuse existing Step Functions.
- **Consistent polling pattern**: All six job types use the same DynamoDB polling pattern in the API Lambda.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| SQS queue + polling worker | Adds SQS infrastructure; async Lambda invoke is simpler for fire-and-forget jobs |
| EventBridge for job dispatch | Over-engineered for single-consumer async invocation |
| Lambda extensions for background work | Experimental, limited to 2 seconds after response, not suitable for multi-minute processing |
| Keep goroutines + increase Lambda timeout | Doesn't fix the freeze problem; Lambda still freezes between invocations regardless of timeout |
| Step Functions for all 6 job types | Over-engineered for triage/description/download/publish which are single-step operations |

## Consequences

**Positive:**

- Jobs survive Lambda cold starts, container recycling, and concurrent invocations
- Step Functions provide built-in retry, timeout, and error handling for selection/enhancement
- DynamoDB provides a single source of truth for all job state
- Worker Lambda can be independently scaled, monitored, and debugged
- API Lambda stays fast (256 MB, 30s) — never blocks on processing

**Trade-offs:**

- One additional Lambda function to deploy and monitor
- ~100ms latency overhead for DynamoDB writes + async Lambda invoke (vs. ~0ms for goroutine launch)
- Worker Lambda cold starts add ~1-3 seconds before processing begins
- DynamoDB costs for job records (minimal — TTL deletes after 24 hours)

## Implementation

| Component | Change |
|-----------|--------|
| `internal/store/store.go` | Add `TriageJob` struct and interface methods |
| `internal/store/dynamo.go` | Add `skTriage` constant and step mapping |
| `internal/store/dynamo_jobs.go` | Add `PutTriageJob()`, `GetTriageJob()` |
| `cmd/media-lambda/globals.go` | Add DynamoDB, Lambda, SFN clients |
| `cmd/media-lambda/main.go` | Initialize new clients in `init()` |
| `cmd/media-lambda/triage.go` | DynamoDB + async Worker Lambda invoke |
| `cmd/media-lambda/selection.go` | DynamoDB + Step Functions `StartExecution` |
| `cmd/media-lambda/enhancement.go` | DynamoDB + Step Functions `StartExecution` |
| `cmd/media-lambda/description.go` | DynamoDB + async Worker Lambda invoke |
| `cmd/media-lambda/download.go` | DynamoDB + async Worker Lambda invoke |
| `cmd/media-lambda/publish.go` | DynamoDB + async Worker Lambda invoke |
| `cmd/media-lambda/session.go` | DynamoDB `InvalidateDownstream` |
| `cmd/worker-lambda/main.go` | New Lambda: triage, description, download, publish processing |
| `cdk/lib/backend-stack.ts` | Add Worker Lambda + permissions |
| `cdk/lib/backend-pipeline-stack.ts` | Add Worker Lambda build + deploy |

### Files Deleted

- `cmd/media-lambda/selection_run.go` — logic exists in `cmd/selection-lambda/`
- `cmd/media-lambda/enhancement_run.go` — logic exists in `cmd/enhance-lambda/`
- `cmd/media-lambda/triage_run.go` — moved to Worker Lambda
- `cmd/media-lambda/description_run.go` — moved to Worker Lambda

## Related Decisions

- [DDR-035](./DDR-035-multi-lambda-deployment.md): Multi-Lambda Deployment Architecture — defines Step Functions and dedicated Lambdas
- [DDR-039](./DDR-039-dynamodb-session-store.md): DynamoDB SessionStore — the store being wired into the API Lambda
- [DDR-043](./DDR-043-step-functions-lambda-entrypoints.md): Step Functions Lambda Entrypoints — the processing Lambdas that Step Functions invokes
