# RAG Decision Memory

Feedback-driven personalization so triage, selection, and captions improve with use. **Design decision:** [DDR-066](./design-decisions/DDR-066-rag-decision-memory.md).

## Overview

Session data (triage verdicts, selection decisions, user overrides, captions) previously lived only in DynamoDB with a 24h TTL. The RAG Decision Memory system:

1. **Persists** user decisions to Aurora PostgreSQL (pgvector) via an EventBridge + SQS pipeline
2. **Embeds** each decision with Bedrock Titan (1024 dimensions) for semantic search
3. **Retrieves** similar past decisions and injects a **preference profile** (triage/selection) or **caption style examples** into AI prompts
4. **Pre-computes** the preference profile weekly (rule-based stats + Gemini narrative) and stores it in DynamoDB for fast reads and cold-start fallback

All RAG behavior is **best-effort**: if the RAG Query Lambda or Aurora is unavailable, triage/selection/description run without personalization context and do not fail.

## Components

| Component | Purpose |
|-----------|---------|
| **EventBridge + SQS** | Existing Lambdas emit `ContentFeedback` events to the default bus; rule routes to SQS ingest queue |
| **RAG Ingest Lambda** | Consumes SQS, calls Bedrock Titan for embeddings, upserts to Aurora (five tables by event type) |
| **RAG Query Lambda** | Invoked by Triage/Selection/Description Lambdas; returns pre-computed profile (DynamoDB) or live vector results; updates last-activity for auto-stop |
| **RAG Status Lambda** | `GET /api/rag/status` — checks Aurora cluster state, starts cluster if stopped |
| **Auto-Stop Lambda** | Runs every 15 min; stops Aurora if last activity &gt; 2 hours |
| **Profile Builder Lambda** | Runs weekly; queries Aurora, computes stats, calls Gemini for narrative, writes profile + caption examples to DynamoDB |
| **DynamoDB** | `rag-preference-profiles` table (profile text, caption examples); last-activity item for auto-stop |

## Data flow

- **Write path:** Triage/Selection/Description/Publish/API Lambdas → `PutEvents` (ContentFeedback) → EventBridge → SQS → RAG Ingest Lambda → Bedrock Titan → Aurora (pgvector).
- **Read path (triage/selection):** Triage or Selection Lambda invokes RAG Query Lambda with `queryType` → Lambda reads profile from DynamoDB (and optionally vector search) → returns text → caller injects into prompt.
- **Read path (caption):** Description Lambda invokes RAG Query Lambda with `queryType: caption` → Lambda returns caption style examples from DynamoDB + top-10 similar captions from Aurora → injected into `BuildDescriptionPrompt`.

## Override capture

- **Real-time:** Each add/remove in the selection review UI POSTs to `/api/overrides/{sessionId}`; API Lambda emits `selection.override.action`.
- **Final:** When the user clicks Continue to enhancement, frontend POSTs to `/api/overrides/{sessionId}/finalize` with the net delta; API Lambda emits `selection.overrides.finalized`.

## Aurora auto-stop / start

- **Stop:** Auto-Stop Lambda runs every 15 min, reads last-activity from DynamoDB; if older than 2 hours, calls `rds:StopDBCluster`.
- **Start:** Frontend calls `GET /api/rag/status` on load; RAG Status Lambda checks cluster state and calls `rds:StartDBCluster` if stopped. Frontend can show “Loading your preferences…” and poll until `available`.
- **Cold start:** While Aurora is starting (~30s), RAG Query Lambda serves the last pre-computed profile from DynamoDB (stale cache fallback).

## Operations

- **New CDK stack:** `RagStack` in `ai-social-media-helper-deploy/cdk`. Deploy manually when introducing or changing RAG (see [Deployment Strategy](./DEPLOYMENT_STRATEGY.md)).
- **Aurora schema:** Run the SQL in `internal/rag/schema.sql` once per cluster (e.g. via Data API or a one-off migration).
- **Cost:** Aurora 0.5–2 ACU when running; Bedrock Titan embeddings on the order of a few dollars per month at low volume. Auto-stop keeps Aurora off when idle.

## References

- [DDR-066: RAG Decision Memory](./design-decisions/DDR-066-rag-decision-memory.md) — decision record
- [RAG_PLANNING.md](../../RAG_PLANNING.md) — full implementation plan, schema, and cost notes (workspace root)
