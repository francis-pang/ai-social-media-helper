# DDR-068: RAG Daily Batch Architecture — DynamoDB Staging

**Date**: 2026-02-28
**Status**: Implemented
**Iteration**: Cloud — cost and complexity reduction
**Supersedes**: Parts of DDR-066 (real-time Aurora ingest, auto-stop/status Lambdas)

## Context

DDR-066 introduced a RAG Decision Memory system with real-time Aurora Serverless v2 ingest: every `ContentFeedback` event triggers Bedrock Titan embedding generation and an Aurora upsert. Aurora must be running during user sessions for both ingest writes and (fallback) vector search. An auto-stop Lambda runs every 15 minutes (672 invocations/week) to stop Aurora after 2 hours of idle, and a status Lambda on the frontend health-check path starts Aurora when the app loads.

Analysis of the query path reveals that **triage and selection queries never touch Aurora when a pre-computed profile exists** — they return immediately from DynamoDB. Aurora vector search is only a fallback for first-time users with no profile. Caption queries merge DynamoDB cached examples with live Aurora results, but the cached examples cover the majority of the value.

This means Aurora runs 4–10+ hours per week primarily to accept real-time ingest writes that are only consumed during the profile build. The auto-stop/start machinery (2 Lambdas, EventBridge schedule, frontend polling) adds operational complexity for minimal benefit.

## Decision

Switch from real-time Aurora ingest to **daily batch processing with DynamoDB staging and early exit** (evolved from Option A1 in the architecture analysis):


| Area              | Before (DDR-066)                                            | After (DDR-068)                                                                                                            |
| ----------------- | ----------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------- |
| **Ingest path**   | SQS → Bedrock embed → Aurora upsert (real-time)             | SQS → DynamoDB staging table write (raw JSON)                                                                              |
| **Aurora uptime** | 4–10+ hours/week (session duration + 2h idle)               | ~40–60 min/week (daily batch, Aurora only wakes when staging has items)                                                    |
| **Query path**    | DynamoDB profile + Aurora fallback                          | DynamoDB profile only (no Aurora during sessions)                                                                          |
| **Profile build** | Weekly EventBridge → profile Lambda → Aurora query → Gemini | Daily EventBridge → profile Lambda → check staging → if empty, exit; else wake Aurora → process → embed + insert → Aurora query → Gemini → stop Aurora |
| **Lambda count**  | 5 (ingest, query, status, autostop, profile)                | 3 (ingest, query, profile)                                                                                                 |
| **Frontend**      | Polls `/api/rag/status` to warm Aurora                      | No RAG status endpoint; app works without Aurora                                                                           |


New DynamoDB staging table `rag-ingest-staging`:

- PK: `STAGING` (constant — single partition, fine for low volume)
- SK: `{timestamp}#{messageId}` (unique per event)
- `feedbackJSON`: full `ContentFeedback` JSON
- TTL: 14-day `expiresAt` as safety net

Daily profile Lambda runs with an early-exit check to avoid unnecessary Aurora wake-ups:

1. Read all staging items from DynamoDB
2. **If empty, exit immediately** — no Aurora wake-up, no Gemini call, Lambda completes in <100ms
3. Start Aurora if stopped; wait for available
4. For each staging item: parse feedback, generate Bedrock Titan embedding, upsert to Aurora
5. Query Aurora for all decisions; compute stats; generate preference profile via Gemini
6. Write profile + caption examples to DynamoDB profiles table
7. Delete processed staging items
8. Stop Aurora

## Rationale

- **Cost**: Aurora runs ~40–60 min/week instead of 4–10+ hours, saving 40–70% (~$0.06–0.10/mo vs $3–10/mo). Daily cadence costs ~2–3x more than weekly batch (~$0.18–0.34/mo total vs ~$0.07–0.11/mo) but remains well under $1/mo. The early-exit check ensures Aurora only wakes on days with new feedback (typically 3–4 days/week).
- **Complexity**: Eliminates 2 Lambdas (auto-stop, status), the 15-minute EventBridge schedule, and the frontend Aurora polling/banner.
- **Session latency**: Improved — no Aurora cold-start during sessions, no Bedrock embedding on each ingest event.
- **No functional regression for triage/selection**: The primary query path already uses DynamoDB profiles, not Aurora.
- **Caption freshness improved over weekly**: Cached caption examples are at most 1 day stale instead of 1 week, capturing recent style shifts faster.

## Alternatives Considered


| Approach                               | Rejected Because                                                                                                                 |
| -------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------- |
| Option A2: S3 staging                  | Higher write latency (~50ms vs ~5ms for DDB), no TTL, S3 eventual consistency edge case, marginal cost difference at this volume |
| Option B: Keep real-time Aurora        | Most expensive, Aurora uptime largely wasted (queries use DynamoDB profile), auto-stop/start machinery adds ops burden           |
| Option C: Weekly batch (instead of daily) | ~2–3x cheaper (~$0.07–0.11/mo), but profiles can be up to 1 week stale; daily cadence is worth the marginal cost for fresher personalization |
| Step Functions for batch orchestration | Adds CDK complexity; Lambda with 10-min timeout handles the batch lifecycle within a single invocation at this volume            |


## Consequences

**Positive:**

- Aurora cost drops from $1–2.50/mo to ~$0.06–0.10/mo (40–60 min/week at 0.5 ACU, only on days with new feedback).
- Lambda count reduced from 5 to 3; EventBridge schedules reduced from 2 to 1 (daily).
- No Aurora cold-start latency during user sessions.
- Frontend simplified (no status polling or warming banner).
- Staging table has 14-day TTL safety net; no data loss even if batch fails.
- Profiles are at most 1 day stale instead of 1 week, capturing recent style shifts faster.
- Early-exit check keeps cost near-zero on idle days (~$0.00 per empty-scan invocation).

**Trade-offs:**

- Caption vector search is no longer real-time; cached examples are up to 1 day stale.
- First-time user bootstrapping runs without personalization until the first daily batch (at most ~24 hours).
- Profile Lambda is more complex (handles Aurora lifecycle + batch embed + profile build).
- Profile Lambda timeout increased to 10 minutes to accommodate Aurora wake time + batch processing.
- Gemini profile-build calls increase from 1/week to 3–4/week (on active days), roughly 3–4x the token cost (~$0.12–0.24/mo vs ~$0.04–0.08/mo).

## Related Documents

- DDR-066 (RAG Decision Memory) — original design, partially superseded
- [RAG Weekly Batch Architecture analysis](../../../.cursor/plans/rag_weekly_batch_architecture_2b345fdd.plan.md) — options comparison

