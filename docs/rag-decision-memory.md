# RAG Decision Memory

Feedback-driven personalization so triage, selection, and captions improve with use. **Design decisions:** [DDR-066](./design-decisions/DDR-066-rag-decision-memory.md), [DDR-068](./design-decisions/DDR-068-rag-weekly-batch-staging.md).

## Overview

Session data (triage verdicts, selection decisions, user overrides, captions) previously lived only in DynamoDB with a 24h TTL. The RAG Decision Memory system:

1. **Stages** user decisions in a DynamoDB staging table via an EventBridge + SQS pipeline (raw JSON, no real-time embedding)
2. **Embeds** decisions in a daily batch with Bedrock Titan (1024 dimensions) and upserts to Aurora PostgreSQL (pgvector)
3. **Retrieves** a pre-computed **preference profile** (triage/selection) or **caption style examples** from DynamoDB and injects them into AI prompts
4. **Pre-computes** the preference profile daily (rule-based stats + Gemini narrative) with an early-exit check — Aurora only wakes when staging has new items

All RAG behavior is **best-effort**: if the RAG Query Lambda is unavailable, triage/selection/description run without personalization context and do not fail. Aurora never runs during user sessions.

## Components

| Component | Purpose |
|-----------|---------|
| **EventBridge + SQS** | Existing Lambdas emit `ContentFeedback` events to the default bus; rule routes to SQS ingest queue |
| **RAG Ingest Lambda** | Consumes SQS, writes raw feedback JSON to the `rag-ingest-staging` DynamoDB table (no embedding at ingest time) |
| **RAG Query Lambda** | Invoked by Triage/Selection/Description Lambdas; returns pre-computed profile from DynamoDB; does not contact Aurora |
| **Profile Builder Lambda** | Runs daily via EventBridge; checks staging table — if empty, exits immediately; otherwise wakes Aurora, generates Bedrock Titan embeddings, upserts to Aurora, queries all tables, computes stats, calls Gemini for narrative, writes profile + caption examples to DynamoDB, stops Aurora |
| **DynamoDB** | `rag-preference-profiles` table (profile text, caption examples); `rag-ingest-staging` table (raw feedback JSON with 14-day TTL) |

## Data flow

- **Write path:** Triage/Selection/Description/Publish/API Lambdas → `PutEvents` (ContentFeedback) → EventBridge → SQS → RAG Ingest Lambda → DynamoDB staging table (raw JSON).
- **Batch path (daily):** EventBridge Scheduler → Profile Builder Lambda → read staging items → if empty, exit; else wake Aurora → Bedrock Titan embed → Aurora upsert → query all tables → compute stats → Gemini narrative → write profile to DynamoDB → delete staging items → stop Aurora.
- **Read path (triage/selection):** Triage or Selection Lambda invokes RAG Query Lambda with `queryType` → Lambda reads profile from DynamoDB → returns text → caller injects into prompt.
- **Read path (caption):** Description Lambda invokes RAG Query Lambda with `queryType: caption` → Lambda returns cached caption examples from DynamoDB → injected into `BuildDescriptionPrompt`.

## Override capture

- **Real-time:** Each add/remove in the selection review UI POSTs to `/api/overrides/{sessionId}`; API Lambda emits `selection.override.action`.
- **Final:** When the user clicks Continue to enhancement, frontend POSTs to `/api/overrides/{sessionId}/finalize` with the net delta; API Lambda emits `selection.overrides.finalized`.

## Aurora lifecycle

Aurora only runs during the daily batch (~40–60 min/week on active days). The Profile Builder Lambda manages the full lifecycle within a single invocation:

1. Read all staging items from DynamoDB
2. If empty, exit immediately — no Aurora wake-up, Lambda completes in <100ms
3. Start Aurora if stopped; wait for available
4. Process staging items (embed + upsert), build profile, write to DynamoDB
5. Stop Aurora

There is no auto-stop/start machinery, no frontend health-check endpoint, and no Aurora interaction during user sessions.

## Operations

- **CDK stack:** `RagStack` in `ai-social-media-helper-deploy/cdk`. Deploy manually when introducing or changing RAG (see [Deployment Strategy](./DEPLOYMENT_STRATEGY.md)).
- **Aurora schema:** Run the SQL in `internal/rag/schema.sql` once per cluster (e.g. via Data API or a one-off migration).
- **Cost:** Aurora runs ~40–60 min/week (0.5 ACU, ~$0.06–0.10/mo); DynamoDB staging + profiles under $0.50/mo; Bedrock Titan embeddings under $1/mo. Total well under $5/mo.

## References

- [DDR-066: RAG Decision Memory](./design-decisions/DDR-066-rag-decision-memory.md) — original decision record
- [DDR-068: RAG Daily Batch Architecture](./design-decisions/DDR-068-rag-weekly-batch-staging.md) — supersedes DDR-066 ingest path, Aurora lifecycle, and Lambda topology
- [RAG_PLANNING.md](../../RAG_PLANNING.md) — original implementation plan (partially superseded by DDR-068)
