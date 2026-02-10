# DDR-052: Step Functions Polling for Long-Running Operations

**Date**: 2026-02-10  
**Status**: Accepted  
**Iteration**: Phase 2 Cloud Deployment

## Context

DDR-050 moved all long-running work off the API Lambda into either Step Functions (selection, enhancement) or an async Worker Lambda (triage, description, download, publish). This eliminated the goroutine-freeze problem, but introduced a new inefficiency: the Worker Lambda **idles while polling external services**.

Two operations are particularly wasteful:

1. **Publish** (1–10 min): After creating Instagram media containers, the Worker Lambda calls `WaitForContainer()` which polls Instagram's status API every 5–30 seconds, up to 5 minutes per video. During this time the Lambda is billed at 2 GB but doing nothing.

2. **Triage with videos** (30s–3 min): After compressing videos with FFmpeg and uploading them to the Gemini Files API, the Worker Lambda polls `Files.Get()` every 5 seconds until processing completes (~20–30 seconds per video). Again, billed 2 GB while idle.

### Cost analysis (22 real sessions, 2,043 photos + 377 videos)

| Operation | Total Lambda time | Idle polling time | Idle % |
|-----------|-------------------|-------------------|--------|
| Triage | 3.5 hours | 2.1 hours | 59% |
| Publish | 1.2 hours | 0.9 hours | 82% |
| **Total** | **4.7 hours** | **3.0 hours** | **65%** |

Moving to Step Functions eliminates the idle Lambda cost because **Wait states are free** — Step Functions charges per state transition ($0.000025), not per second of duration.

| | Current (Worker Lambda) | Proposed (Step Functions) | Savings |
|---|---|---|---|
| Triage + Publish | $0.564 | $0.222 | $0.342 (61%) |

The remaining Worker Lambda operations — description (5–30s), description-feedback (5–30s), enhancement-feedback (10–60s), and download (30s–5 min, CPU-bound) — are either short-lived or actively doing work the entire time, so they stay on the Worker Lambda.

## Decision

Move **Triage** and **Publish** from the async Worker Lambda to dedicated Step Functions state machines with polling loops that use Wait states instead of Lambda idle time.

### Triage Pipeline

```
PrepareMedia (Lambda) → [has videos?]
  → Yes → CheckGeminiStatus (Lambda) → [all active?]
    → Yes → RunTriage (Lambda) → End
    → No → Wait 5s → CheckGeminiStatus
  → No → RunTriage (Lambda) → End
```

- **PrepareMedia**: Downloads files from S3, loads metadata, compresses videos with FFmpeg, uploads to Gemini Files API. Returns file URIs and video file names.
- **CheckGeminiStatus**: Calls `client.Files.Get()` for each video URI. Returns whether all files are active.
- **RunTriage**: Calls `AskMediaTriage` with all prepared media. Writes results to DynamoDB.

### Publish Pipeline

```
CreateContainers (Lambda) → [has videos?]
  → Yes → CheckVideoStatus (Lambda) → [all finished?]
    → Yes → PublishPost (Lambda) → End
    → No → Wait 10s → CheckVideoStatus
  → No → PublishPost (Lambda) → End
```

- **CreateContainers**: Creates Instagram media containers for each item. Writes container IDs to DynamoDB.
- **CheckVideoStatus**: Calls `igClient.ContainerStatus()` for each video container. Returns whether all are finished.
- **PublishPost**: Creates carousel container (if multi-item), calls `igClient.Publish()`. Writes result to DynamoDB.

### Worker Lambda Scope (reduced)

After this change, the Worker Lambda handles only:
- `description` (5–30s)
- `description-feedback` (5–30s)
- `enhancement-feedback` (10–60s)
- `download` (30s–5 min, CPU-bound)

### Upload (no change)

The presigned URL approach for media upload is already optimal. The API Lambda call to generate a presigned URL costs $0.0000004 per file. API Gateway's 10 MB payload limit makes a direct S3 integration impractical for media files.

## Rationale

- **Step Functions Wait states are free**: The core insight. Lambda charges per GB-second of compute; Step Functions charges per state transition. A 60-second Wait state costs $0.000000 vs $0.002000 for 60 seconds of a 2 GB Lambda.
- **Consistent with existing patterns**: Selection and Enhancement already use Step Functions. This change brings Triage and Publish in line with the same architecture.
- **Timeout resilience**: The Worker Lambda has a hard 10-minute timeout. Large triage sessions (500+ files with many videos) can exceed this. Step Functions has a 30-minute timeout and can be extended.
- **Observable polling**: Step Functions provides a visual execution graph showing each poll iteration, making it easy to debug stuck video processing or Gemini API delays.
- **Reuses existing Lambdas**: The triage and publish logic stays in the Worker Lambda binary — the handlers are just split into Step Function-compatible entry points.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Keep Worker Lambda as-is | 65% idle compute cost; 10-minute timeout risk for large sessions |
| Lambda Durable Functions | New AWS feature (2026); $8/M durable operations + data charges are expensive at scale; adds lock-in to a new primitive |
| EventBridge Scheduler for delayed polling | More complex than Step Functions Wait; no built-in loop/retry semantics |
| SNS/SQS callback from Instagram/Gemini | Neither service supports webhooks for processing completion; polling is required |
| Express Workflows instead of Standard | Express charges per GB-second of duration (including waits), defeating the purpose; Standard Wait states are free |

## Consequences

**Positive:**

- 61% cost reduction for triage + publish operations
- No 10-minute timeout risk — Step Functions supports 30-minute (or longer) workflows
- Visual execution graphs for debugging long-running operations
- Consistent dispatch pattern: all long-running work now uses Step Functions
- Worker Lambda is lighter (fewer responsibilities, faster cold start)

**Trade-offs:**

- Two additional Step Functions state machines to maintain
- Triage and publish logic is split across multiple handler entry points (vs. monolithic functions)
- State transitions have a small per-transition cost ($0.000025 each), but polling loops are infrequent (6–12 transitions per execution)
- Cold start for poll-check Lambdas adds ~1s per poll iteration (mitigated by Step Functions retry)

## Implementation

| Component | Change |
|-----------|--------|
| `cmd/worker-lambda/main.go` | Split `handleTriage` into `handleTriagePrepare`, `handleTriageCheckGemini`, `handleTriageRun`; split `handlePublish` into `handlePublishCreateContainers`, `handlePublishCheckVideoStatus`, `handlePublishFinalize` |
| `cmd/media-lambda/triage.go` | Change dispatch from `invokeWorkerAsync` to `sfnClient.StartExecution` for triage pipeline |
| `cmd/media-lambda/publish.go` | Change dispatch from `invokeWorkerAsync` to `sfnClient.StartExecution` for publish pipeline |
| `cmd/media-lambda/globals.go` | Add `triageSfnArn` and `publishSfnArn` variables |
| `cmd/media-lambda/main.go` | Initialize new SFN ARN env vars in `init()` |
| `cdk/lib/backend-stack.ts` | Add `TriagePipeline` and `PublishPipeline` Step Functions state machines |
| `docs/ARCHITECTURE.md` | Update async dispatch table and cloud architecture diagram |

## Related Decisions

- [DDR-035](./DDR-035-multi-lambda-deployment.md): Multi-Lambda Deployment — defines the Step Functions architecture
- [DDR-050](./DDR-050-replace-goroutines-with-async-dispatch.md): Replace Goroutines with Async Dispatch — the pattern being refined by this DDR
