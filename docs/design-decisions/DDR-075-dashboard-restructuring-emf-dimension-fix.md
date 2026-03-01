# DDR-075: Dashboard Restructuring and EMF Dimension Fix

**Date**: 2026-03-01  
**Status**: Accepted  
**Iteration**: Cloud — observability and operations

## Context

The CloudWatch dashboard introduced in DDR-062 grew to a single `AiSocialMediaDashboard` with ~45 widgets. Investigation revealed that multiple widgets showed "no data" despite confirmed Lambda traffic. Diagnosis via `aws cloudwatch list-metrics` and `get-metric-statistics` identified three root causes:

### Bug 1: EMF Library Emits FunctionName in Every DimensionSet

`internal/metrics/emf.go` automatically adds `FunctionName` as a dimension on every Lambda emission. CloudWatch metric queries require an **exact dimension match** — querying `{Operation: "triage"}` returns no data if the stored metric was recorded under `{FunctionName: "my-fn", Operation: "triage"}`. Dashboard helpers (e.g., `emfMetric()`) correctly omit `FunctionName`, but the stored metrics include it, so all 18+ custom EMF metrics returned empty results in the dashboard.

### Bug 2: DynamoDB SuccessfulRequestLatency Requires Operation Dimension

`AWS/DynamoDB SuccessfulRequestLatency` is only published with `{TableName, Operation}` dimensions (e.g., `Operation: "GetItem"`). Dashboard queries using only `{TableName}` never match — CloudWatch returns no data. All DynamoDB latency widgets were consistently empty.

### Bug 3: Auth Validation Metrics Are CLI-Only

`ApiKeyValidationMs` and `ApiKeyValidationResult` are emitted by the CLI's `validateKey()` path only. Lambda requests are validated via the API Gateway JWT authorizer, which never calls this code path. These metrics will always show "no data" in CloudWatch and add visual noise.

## Decision

Three changes:

### 1. Fix EMF Library — Dual DimensionSets

When `FunctionName` is present in the recorder's dimensions, `Flush()` emits **two DimensionSets** in the EMF `_aws.CloudWatchMetrics[0].Dimensions` array:

1. **Custom dimensions only** (without `FunctionName`) — for dashboard queries
2. **All dimensions including `FunctionName`** — for per-Lambda debugging via CloudWatch Metrics

`FunctionName` remains as a top-level field in the EMF document so it is present in both dimension sets as a metric value. When `FunctionName` is absent (CLI runs), a single DimensionSet is emitted as before.

### 2. Split Single Dashboard Into Three Purpose-Built Dashboards

| Dashboard | Name | Purpose |
|-----------|------|---------|
| Triage | `AiSocialMedia-Triage` | Active triage workflow — all metrics expected to have data |
| Selection | `AiSocialMedia-Selection` | Selection/enhancement/publish — empty until those workflows run |
| Infrastructure | `AiSocialMedia-Infrastructure` | Common infra — API GW, CloudFront, Lambda, DynamoDB sessions, S3, logs |

**Rationale for split**: A single dashboard mixes active metrics (triage runs frequently) with inactive ones (selection is rarely used). Operators cannot distinguish "no traffic" from "broken metric" at a glance. Three dashboards make the health of each workflow self-evident.

### 3. Fix DynamoDB Latency Queries — Include Operation Dimension

All `SuccessfulRequestLatency` widgets now use `{TableName, Operation}` dimensions (e.g., `Operation: "GetItem"`, `Operation: "PutItem"`) to match how CloudWatch actually stores this metric.

## Additional Improvements (Initial Deployment)

- **ms-to-seconds conversion**: Multi-second metrics (SFN execution time, job durations) use `MathExpression` with `m / 1000` and `leftYAxis: { label: 's' }`. Sub-second metrics retain milliseconds with `leftYAxis: { label: 'ms' }`.
- **CRCU/CWCU same axis**: ConsumedReadCapacityUnits and ConsumedWriteCapacityUnits both on the `left` axis (previously CWCU was on `right`, making the scale misleading).
- **DynamoDB idle periods**: `FILL(m, 0)` via `MathExpression` for capacity metrics — shows zero instead of "no data" gaps during low-traffic windows.
- **Remove Auth row**: The Auth & Validation row (`ApiKeyValidationMs`, `ApiKeyValidationResult`) is removed from all dashboards — these are CLI-only metrics.
- **Publish Pipeline**: Added to the Selection dashboard.

## Post-Deployment Findings (2026-03-01)

After the initial deployment, CloudWatch confirmed traffic was flowing through the updated Lambdas, but multiple widgets still showed "no data." Investigation (`aws cloudwatch list-metrics`, source code audit) identified the following additional bugs:

### Bug 4: File Processing Metrics Stored With Two Dimensions, Queried With One

`cmd/media-process-lambda/processor.go` emits `FilesProcessed`, `FileProcessingMs`, and `FileSize` with **both** `Operation` and `FileType` dimensions together. After the dual DimensionSet fix, stored sets are `{Operation, FileType}` and `{FunctionName, Operation, FileType}`. The initial dashboard widgets queried `{Operation}` alone or `{FileType}` alone — neither matches.

**Fix**: All three metric widgets in Triage Row 3 now query with `{FileType: 'image'|'video', Operation: 'mediaProcess'}`. The "By File Type" widget (which used `{FileType}` only) is merged into a stacked "Files Processed" widget, eliminating the redundancy.

### Bug 5: TriageJobFiles Is the Wrong Metric Name

Triage Row 8 queried `TriageJobFiles`, but `cmd/triage-lambda/handler.go` emits `JobFilesProcessed` with `{JobType: 'triage'}`.

**Fix**: Widget updated to query `JobFilesProcessed` with `{JobType: 'triage'}`.

### Bug 6: GeminiApiErrors Queried With No Dimensions, Emitted With {Operation}

`GeminiApiErrors` is emitted with `{Operation: 'triage'}`. The dashboard queried it with no dimensions, which never matches after the DimensionSet fix.

**Fix**: Dimension `{Operation: 'triage'}` added to the Gemini Errors widget.

### Bug 7: ImageResizeMs / ImageSizeBytes / ImageCompressionRatio Never Emitted

`internal/filehandler/image_resize.go` performs the resize but has no EMF instrumentation. Triage Row 4 widgets for these metrics never populate.

**Fix**: Three metrics emitted in `cmd/media-process-lambda/processor.go` immediately after `ResizeImageForGemini` returns:

- `ImageResizeMs` — wall-clock time for the resize call (milliseconds)
- `ImageSizeBytes` — byte length of the resized output
- `ImageCompressionRatio` — `originalFileSize / resizedDataLength` (dimensionless)

All three use dimensions `{Operation: 'mediaProcess', FileType: 'image'}` for consistency with the other per-file metrics.

### Bug 8: GeminiFilesApiUploadBytes Placed on Triage Dashboard

This metric is emitted exclusively in `internal/chat/selection_media.go` (selection workflow). Placing it on the Triage dashboard guaranteed "no data."

**Fix**: Removed from Triage Row 9; it remains on the Selection dashboard.

### Bug 9: Selection Dashboard Cache Metric Names Are Wrong

| Dashboard widget used    | Code actually emits  | Source                   |
|--------------------------|----------------------|--------------------------|
| `GeminiCacheHits`        | `GeminiCacheHit`     | `selection_media.go:224` |
| `GeminiCacheMisses`      | `GeminiCacheMiss`    | `selection_media.go:226` |
| `GeminiCacheTokensSaved` | `GeminiCachedTokens` | `selection_media.go:233` |
| `PublishAttempts`        | *(never emitted)*    | —                        |

**Fix**: All four corrected to actual emitted names. `PublishAttempts` widget removed.

### Bug 10: Multi-Second Durations Displayed in Raw Milliseconds

Several Lambda duration and latency metrics routinely exceed 1,000 ms, producing unreadable y-axis values (e.g., `445.4k` ms for a 445-second MediaProcess invocation). The `msToSeconds()` helper existed but was only applied to SFN pipeline times and `JobDurationMs`.

**Fix**: `msToSeconds()` applied to all metrics that exceed 1 second in practice:

| Metric | Observed range |
|--------|---------------|
| MediaProcess `metricDuration` | avg ~68s, max ~445s |
| TriageProcessor `metricDuration` | avg ~17s, max ~414s |
| `VideoCompressionMs` | 10s–450s |
| `FileProcessingMs` | 1s–450s |
| `GeminiApiLatencyMs` (triage + selection) | ~5s–60s |
| Selection/Enhancement/Publish/Thumbnail `metricDuration` | 5s–minutes |
| All-Lambda cross-comparison `metricDuration p99` | mixed, dominated by long runners |

Metrics confirmed sub-second (kept in ms): `apiHandlerFn.metricDuration` (avg 136–655ms), API GW Latency, CloudFront OriginLatency, `RequestLatencyMs`, `ImageResizeMs` (~300ms), DynamoDB latencies.

### New Feature: Compression Ratio Metrics

`VideoCompressionRatio` was already emitted by `internal/filehandler/video_compress.go` but had no dashboard widget. `ImageCompressionRatio` did not exist at all.

**Added**:
- Triage Row 4 "Video Compression: Latency + Ratio" — dual-axis widget: left=`VideoCompressionMs` in seconds, right=`VideoCompressionRatio`
- Triage Row 4 "Image Resize: Latency + Ratio" — dual-axis widget: left=`ImageResizeMs` in ms, right=`ImageCompressionRatio`
- Triage Row 4 "Compression: Output Size" — `ImageSizeBytes` (image) + `MediaFileSizeBytes` (video)

## Risks

**Dual DimensionSets double the CloudWatch custom metric count per emission.** Each EMF flush that previously emitted 1 metric series now emits 2 (one with `FunctionName`, one without). At single-user traffic levels this is negligible — estimated additional cost < $1/month. If traffic scales significantly, consider removing `FunctionName` as a dimension entirely (keep it as a property for Logs Insights only), which would revert to single-set emission.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Remove FunctionName dimension entirely | Loses per-Lambda metric filtering for multi-Lambda namespaces (useful when debugging a specific function) |
| Add FunctionName to dashboard queries | Requires hardcoding function names in CDK; breaks if functions are renamed; still one DimensionSet |
| Keep single dashboard, fix metric queries only | 45 widgets is too dense for operational use; mixes workflows with very different traffic patterns |
| DynamoDB: use metric math to aggregate across operations | Loses per-operation latency breakdown; GetItem and PutItem have very different latency profiles |

## Consequences

**Positive:**
- All triage-related widgets in `AiSocialMedia-Triage` show data immediately after deploying the EMF fix
- DynamoDB latency widgets now return actual data
- Cleaner operational view — each dashboard tells a coherent story about one workflow
- 8 previously-invisible EMF metrics are now surfaced

**Trade-offs:**
- Three dashboards to bookmark instead of one
- Dual DimensionSets increase CloudWatch custom metric count (~2x for Lambda-emitted metrics)
- DynamoDB latency now shows per-operation widgets rather than a single average

## Implementation

### Files Modified

| File | Change |
|------|--------|
| `internal/metrics/emf.go` | `Flush()` emits dual DimensionSets when `FunctionName` present |
| `internal/metrics/emf_test.go` | Tests verify single-set (CLI) and dual-set (Lambda) behaviour |
| `cmd/media-process-lambda/processor.go` | Emit `ImageResizeMs`, `ImageSizeBytes`, `ImageCompressionRatio` after `ResizeImageForGemini` |
| `ai-social-media-helper-deploy/cdk/lib/operations-dashboard-stack.ts` | Replace single `AiSocialMediaDashboard` with three dashboards; fix dimension queries, metric names, ms→s conversions, and add ratio widgets |

## Related Documents

- DDR-062 (Observability Gaps and Version Tracking — original dashboard and EMF introduction)
- DDR-051 (Comprehensive Logging Overhaul — log metric filters)
- DDR-061 (S3 Event-Driven Per-File Processing — MediaProcess Lambda and file processing table)
- DDR-054 (S3 Multipart Upload Acceleration — OperationsDashboardStack split rationale)
