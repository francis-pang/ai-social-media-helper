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

## Additional Improvements

- **ms-to-seconds conversion**: Multi-second metrics (SFN execution time, job durations) use `MathExpression` with `m / 1000` and `leftYAxis: { label: 's' }`. Sub-second metrics retain milliseconds with `leftYAxis: { label: 'ms' }`.
- **CRCU/CWCU same axis**: ConsumedReadCapacityUnits and ConsumedWriteCapacityUnits both on the `left` axis (previously CWCU was on `right`, making the scale misleading).
- **DynamoDB idle periods**: `FILL(m, 0)` via `MathExpression` for capacity metrics — shows zero instead of "no data" gaps during low-traffic windows.
- **Remove Auth row**: The Auth & Validation row (`ApiKeyValidationMs`, `ApiKeyValidationResult`) is removed from all dashboards — these are CLI-only metrics.
- **Surface previously-unused EMF metrics**: 8 EMF metrics that existed in code but had no dashboard widgets are now surfaced: `ImageResizeMs`, `ImageSizeBytes`, `GeminiCacheHits`, `GeminiCacheMisses`, `GeminiCacheTokensSaved`, `GeminiFilesApiUploadBytes`, `TriageJobFiles`, `PublishAttempts`.
- **Publish Pipeline**: Added to the Selection dashboard.

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
| `ai-social-media-helper-deploy/cdk/lib/operations-dashboard-stack.ts` | Replace single `AiSocialMediaDashboard` with three dashboards; fix `dynamoMetric()` helper to accept optional `Operation` dimension |

## Related Documents

- DDR-062 (Observability Gaps and Version Tracking — original dashboard and EMF introduction)
- DDR-051 (Comprehensive Logging Overhaul — log metric filters)
- DDR-061 (S3 Event-Driven Per-File Processing — MediaProcess Lambda and file processing table)
- DDR-054 (S3 Multipart Upload Acceleration — OperationsDashboardStack split rationale)
