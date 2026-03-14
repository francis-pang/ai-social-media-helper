# DDR-088: Gemini Token Metrics Emission Gaps

**Date**: 2026-03-14  
**Status**: Accepted  
**Iteration**: Cloud — observability and cost analysis

## Context

CloudWatch metrics `GeminiInputTokens` and `GeminiOutputTokens` (namespace `AiSocialMedia`, dimension `Operation`) are used for cost analysis and model comparison (e.g. 3 Flash vs 3.1 Flash-Lite). AWS CLI queries showed only **triage** had token metrics for March 2026; selection, description, and FB Prep had none.

Operations are defined in `docs/operations.md` and the dashboard (`operations-dashboard-stack.ts`). The gap prevents accurate cost justification for model switches.

## Gap Analysis


| Operation                 | Code Path                                            | Emits Token Metrics? | Notes                                                                      |
| ------------------------- | ---------------------------------------------------- | -------------------- | -------------------------------------------------------------------------- |
| `triage`                  | `internal/ai/triage.go`                              | ✅ Yes                | Real-time path only                                                        |
| `mediaSelection`          | `internal/ai/selection_media.go`                     | ✅ Yes                | Real-time path only                                                        |
| `photoSelection`          | `internal/ai/selection_photo.go`                     | ✅ Yes                | Real-time path only                                                        |
| `jsonSelection`           | `internal/ai/selection_media.go`                     | ✅ Yes                | Real-time path only                                                        |
| `description`             | `internal/ai/description.go`                         | ❌ No                 | Has `resp.UsageMetadata` but never emits                                   |
| `fbPrepLocationPreEnrich` | `cmd/fb-prep-lambda/handler.go`                      | ❌ No                 | Real-time Maps call; has `resp.UsageMetadata`                              |
| `fbPrepBatch`             | `cmd/fb-prep-lambda/handler.go` (handleCollectBatch) | ❌ No                 | Aggregates tokens from batch results; stores in DynamoDB but does not emit |
| `filesApiUpload`          | `internal/ai/selection.go`                           | N/A                  | Gemini Files API upload; no token usage per upload                         |


**Batch paths** (economy mode): Triage, Selection, and Description submit to Gemini Batch API and return `batch_job_id`. Only FB Prep has a collect step (`handleCollectBatch`) that reads completed results and has access to token counts. Triage/Selection/Description batch flows do not have collect Lambdas in the current SFN design — token metrics for those batch paths are out of scope for this DDR.

## Decision

### 1. Add token metrics to Description (real-time)

In `internal/ai/description.go`:

- **GenerateDescription**: After successful response (both cache and streaming paths), emit `GeminiInputTokens` and `GeminiOutputTokens` with `Operation=description` when `resp.UsageMetadata != nil`.
- **RegenerateDescription**: Same after successful `GenerateContent` response.

### 2. Add token metrics to FB Prep

In `cmd/fb-prep-lambda/handler.go`:

- **fbPrepLocationPreEnrich**: Add `GeminiInputTokens` and `GeminiOutputTokens` to the existing metrics block when `resp.UsageMetadata != nil`.
- **handleCollectBatch**: After aggregating `inputTokens` and `outputTokens` from batch results, emit with `Operation=fbPrepBatch`.

### 3. Update operations documentation

Add `description`, `fbPrepLocationPreEnrich`, and `fbPrepBatch` to the Operation values list in `docs/operations.md`.

## Scope of Change


| File                            | Change                                                               |
| ------------------------------- | -------------------------------------------------------------------- |
| `internal/ai/description.go`    | Emit token metrics in GenerateDescription and RegenerateDescription  |
| `cmd/fb-prep-lambda/handler.go` | Emit token metrics in fbPrepLocationPreEnrich and handleCollectBatch |
| `docs/operations.md`            | Add new Operation values                                             |


## Consequences

- **Positive**: Full token visibility for Description and FB Prep (real-time + batch) enables accurate cost analysis.
- **Positive**: AWS CLI `aws cloudwatch get-metric-statistics` can query all Gemini-consuming operations.
- **Neutral**: Triage/Selection/Description batch paths remain without metrics until collect steps are added to those SFNs (future work).

## Related Documents

- DDR-062 (Observability and Version Tracking — EMF introduction)
- DDR-075 (Dashboard Restructuring — EMF dimension fix)
- DDR-077 (Cost-Aware Vertex AI Migration — economy mode)
- DDR-082 (FB Prep Economy Mode SFN — batch flow)

