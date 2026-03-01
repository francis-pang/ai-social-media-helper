# DDR-079: Triage UI — Immediate Navigation, Retry, and Streaming Logs

**Status:** Accepted  
**Date:** 2026-03-01

## Context

When the Gemini triage-run step fails (e.g., 503 "model experiencing high demand"), the
FileUploader screen shows an error banner but the user is stuck with no recovery path — the
only option is "Cancel All Uploads" which discards all uploaded and processed files.

Root cause analysis of session `67a845a4` revealed three issues:

1. **FileUploader waits too long to navigate.** It stays on-screen until `triage-run`
   completes (status = "complete" or "error"), blocking the user from seeing the processing
   dashboard even though file processing finished minutes earlier.

2. **No retry mechanism.** The error state only offers "Start Over", forcing a full re-upload
   of multi-GB file sets. The processed files remain in S3/DDB and could be re-triaged.

3. **No visibility into backend progress.** The ProcessingIndicator shows synthetic phase
   labels but no batch-level progress or backend logs. Users can't tell what's happening
   during the 2–4 minute triage-run window.

## Decision

### A. Immediate navigation on file processing completion

`FileUploader.pollTriageResults` now navigates to `TriageView` as soon as
`allProcessed = true` (all files have been downsized and thumbnailed), without waiting for
the Gemini triage-run step. The `results.status !== "pending"` guard is removed.

On `results.status === "error"`, the FileUploader also navigates to TriageView instead of
showing a local error banner, so TriageView can display the error with recovery options.

### B. Retry triage on error

TriageView's error state now shows a "Retry Triage" button alongside "Start Over". Retry
calls `POST /api/triage/finalize` which re-starts the Step Function. The backend handles:

- **Unique SFN execution names:** Appends `-r<N>` suffix (e.g., `triage-abc123-r1`) when
  the job status is `"error"`, since Step Functions requires unique execution names.
- **Job status reset:** Resets status from `"error"` to `"processing"`, clears the error
  field, and increments `retryCount` in DDB.

### C. Batch progress tracking

`AskMediaTriage` now accepts a `BatchProgressFunc` callback, invoked after each batch
completes. The triage-run handler passes a callback that writes `triageBatch` and
`triageBatchTotal` to the DDB triage job. The results API returns these fields, enabling
the TriageView to show "Analyzing batch 2 of 4" in the processing indicator.

### D. CloudWatch streaming logs

New endpoint `GET /api/triage/{id}/logs?sessionId=...&since=...` queries CloudWatch Logs
for the triage Lambda, filtered by session ID. The ProcessingIndicator adds a
"Synthetic / Raw Logs" toggle — synthetic shows enriched phase/batch events, raw streams
actual CloudWatch log entries polled every 4 seconds.

## Changes

| File | Change |
|------|--------|
| `web/src/components/FileUploader.tsx` | Navigate on `allProcessed` or `error` instead of waiting for `complete` |
| `web/src/components/TriageView.tsx` | Add `retryTriage`, batch progress display, enriched log props |
| `web/src/components/ProcessingIndicator.tsx` | Raw/Synthetic logs toggle, CloudWatch log polling |
| `web/src/api/client.ts` | Add `getTriageLogs` API function |
| `web/src/types/api.ts` | Add `triageBatch`, `triageBatchTotal`, `TriageLogEntry`, `TriageLogsResponse` |
| `cmd/api/triage.go` | Re-finalize support, batch progress in results, `handleTriageLogs` endpoint |
| `cmd/api/globals.go` | Add `cwlClient` for CloudWatch Logs |
| `cmd/api/main.go` | Initialize CloudWatch Logs client |
| `cmd/triage-worker/handler.go` | Pass batch progress callback to `AskMediaTriage` |
| `internal/ai/triage.go` | Add `BatchProgressFunc` parameter to `AskMediaTriage` |
| `internal/store/store.go` | Add `RetryCount`, `TriageBatch`, `TriageBatchTotal` to `TriageJob` |

## Deployment

- **Environment variable:** Set `TRIAGE_LOG_GROUP_NAME` on the API Lambda to the triage
  processor's CloudWatch log group name for the raw logs endpoint to function.
- **IAM:** The API Lambda needs `logs:FilterLogEvents` permission on the triage log group.
- Backend deploy required (new API endpoint + handler changes).
- Frontend deploy required (navigation + UI changes).
