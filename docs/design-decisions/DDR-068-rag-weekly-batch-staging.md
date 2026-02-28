# DDR-068: RAG Weekly Batch Architecture — DynamoDB Staging

**Date**: 2026-02-28
**Status**: Implemented
**Iteration**: Cloud — cost and complexity reduction
**Supersedes**: Parts of DDR-066 (real-time Aurora ingest, auto-stop/status Lambdas)

## Context

DDR-066 introduced a RAG Decision Memory system with real-time Aurora Serverless v2 ingest: every `ContentFeedback` event triggers Bedrock Titan embedding generation and an Aurora upsert. Aurora must be running during user sessions for both ingest writes and (fallback) vector search. An auto-stop Lambda runs every 15 minutes (672 invocations/week) to stop Aurora after 2 hours of idle, and a status Lambda on the frontend health-check path starts Aurora when the app loads.

Analysis of the query path reveals that **triage and selection queries never touch Aurora when a pre-computed profile exists** — they return immediately from DynamoDB. Aurora vector search is only a fallback for first-time users with no profile. Caption queries merge DynamoDB cached examples with live Aurora results, but the cached examples cover the majority of the value.

This means Aurora runs 4–10+ hours per week primarily to accept real-time ingest writes that are only consumed once per week during the profile build. The auto-stop/start machinery (2 Lambdas, EventBridge schedule, frontend polling) adds operational complexity for minimal benefit.

## Decision

Switch from real-time Aurora ingest to **weekly batch processing with DynamoDB staging** (Option A1 from the architecture analysis):


| Area              | Before (DDR-066)                                            | After (DDR-068)                                                                                                            |
| ----------------- | ----------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------- |
| **Ingest path**   | SQS → Bedrock embed → Aurora upsert (real-time)             | SQS → DynamoDB staging table write (raw JSON)                                                                              |
| **Aurora uptime** | 4–10+ hours/week (session duration + 2h idle)               | ~15–30 min/week (batch processing only)                                                                                    |
| **Query path**    | DynamoDB profile + Aurora fallback                          | DynamoDB profile only (no Aurora during sessions)                                                                          |
| **Profile build** | Weekly EventBridge → profile Lambda → Aurora query → Gemini | Weekly EventBridge → profile Lambda → wake Aurora → process staging → embed + insert → Aurora query → Gemini → stop Aurora |
| **Lambda count**  | 5 (ingest, query, status, autostop, profile)                | 3 (ingest, query, profile)                                                                                                 |
| **Frontend**      | Polls `/api/rag/status` to warm Aurora                      | No RAG status endpoint; app works without Aurora                                                                           |


New DynamoDB staging table `rag-ingest-staging`:

- PK: `STAGING` (constant — single partition, fine for low volume)
- SK: `{timestamp}#{messageId}` (unique per event)
- `feedbackJSON`: full `ContentFeedback` JSON
- TTL: 14-day `expiresAt` as safety net

Weekly profile Lambda now handles the full batch lifecycle:

1. Read all staging items from DynamoDB
2. If empty, skip (profile is already current)
3. Start Aurora if stopped; wait for available
4. For each staging item: parse feedback, generate Bedrock Titan embedding, upsert to Aurora
5. Query Aurora for all decisions; compute stats; generate preference profile via Gemini
6. Write profile + caption examples to DynamoDB profiles table
7. Delete processed staging items
8. Stop Aurora

## Rationale

- **Cost**: Aurora runs ~~30 min/week instead of 4–10+ hours, saving 50–80% (~~$1–2/mo vs $3–10/mo).
- **Complexity**: Eliminates 2 Lambdas (auto-stop, status), the 15-minute EventBridge schedule, and the frontend Aurora polling/banner.
- **Session latency**: Improved — no Aurora cold-start during sessions, no Bedrock embedding on each ingest event.
- **No functional regression for triage/selection**: The primary query path already uses DynamoDB profiles, not Aurora.
- **Caption freshness trade-off is acceptable**: Cached caption examples (updated weekly) cover the majority of caption style value. Live vector search added marginal benefit.

## Alternatives Considered


| Approach                               | Rejected Because                                                                                                                 |
| -------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------- |
| Option A2: S3 staging                  | Higher write latency (~50ms vs ~5ms for DDB), no TTL, S3 eventual consistency edge case, marginal cost difference at this volume |
| Option B: Keep real-time Aurora        | Most expensive, Aurora uptime largely wasted (queries use DynamoDB profile), auto-stop/start machinery adds ops burden           |
| Step Functions for batch orchestration | Adds CDK complexity; Lambda with 10-min timeout handles the batch lifecycle within a single invocation at this volume            |


## Consequences

**Positive:**

- Aurora cost drops from $1–2.50/mo to ~$0.03/mo (30 min/week at 0.5 ACU).
- Lambda count reduced from 5 to 3; EventBridge schedules reduced from 2 to 1.
- No Aurora cold-start latency during user sessions.
- Frontend simplified (no status polling or warming banner).
- Staging table has 14-day TTL safety net; no data loss even if batch fails.

**Trade-offs:**

- Caption vector search is no longer real-time; cached examples are up to 1 week stale.
- First-time user bootstrapping runs without personalization until the first weekly batch (same as when Aurora was stopped in the old design).
- Profile Lambda is more complex (handles Aurora lifecycle + batch embed + profile build).
- Profile Lambda timeout increased to 10 minutes to accommodate Aurora wake time + batch processing.

## Related Documents

- DDR-066 (RAG Decision Memory) — original design, partially superseded
- [RAG Weekly Batch Architecture analysis](../../../.cursor/plans/rag_weekly_batch_architecture_2b345fdd.plan.md) — options comparison

