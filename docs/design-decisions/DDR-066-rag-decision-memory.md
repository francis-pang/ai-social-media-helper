# DDR-066: RAG Decision Memory — Feedback-Driven Personalization

**Date**: 2026-02-20  
**Status**: Implemented  
**Iteration**: Cloud — AI personalization

## Context

Session data (triage verdicts, selection decisions, user overrides, captions) lives in DynamoDB with a 24h TTL and is deleted. The AI has no memory of user preferences across sessions: it cannot learn that the user tends to keep blurry action shots, overrides AI exclusions for near-duplicates, or prefers a certain caption style. Triage, selection, and caption quality do not improve with use.

We needed a system that (1) persists user decisions beyond the session, (2) makes them queryable for relevance to new media, and (3) injects summarized context into AI prompts so future recommendations align better with observed behavior. The system is single-user and low-volume.

## Decision

Introduce a **RAG (Retrieval-Augmented Generation) Decision Memory** system with the following choices:

| Area | Decision |
|------|----------|
| **Retrieval** | Vector-search RAG — embed decisions with Bedrock Titan (1024d), store in Aurora PostgreSQL with pgvector, retrieve top-K similar past decisions |
| **Vector store** | Aurora Serverless v2 (0.5–2 ACU), Data API only (no VPC for Lambdas), HNSW index, auto-stop after 2h idle, frontend health check to start on app load |
| **Feedback pipeline** | EventBridge (default bus) + SQS — Lambdas emit `ContentFeedback` events; ingest Lambda consumes queue, embeds, upserts to Aurora |
| **Scope** | Full pipeline: triage, selection, overrides, captions, publish — each stage emits events |
| **Prompt integration** | Pre-computed preference profile (triage/selection) and caption style examples, stored in DynamoDB; weekly batch (rule-based stats + Gemini narrative) |
| **Override capture** | Both: real-time POST on each add/remove in selection review, and final delta on proceed to enhancement |
| **Cold start** | Stale cache fallback — when Aurora is stopped, serve last profile from DynamoDB |
| **Infrastructure** | New dedicated RAG CDK stack; rollout is big-bang (destroy and redeploy) |
| **Data retention** | Permanent (no TTL, RETAIN) |

New components: RAG stack (Aurora Serverless v2, EventBridge rule, SQS + DLQ, DynamoDB table `rag-preference-profiles`), five new Lambdas (ingest, query, status, auto-stop, profile builder), and instrumentation in existing Lambdas to emit events and call the RAG Query Lambda before building prompts.

## Rationale

- **Vector-search over full-context:** Data volume is small today but vector retrieval scales and gives semantic similarity (e.g. “blurry action shot” matches past similar decisions).
- **Aurora Serverless v2 + Data API:** Avoids VPC/NAT for Lambdas, keeps cost bounded (0.5–2 ACU); auto-stop and frontend-triggered start limit cost when idle.
- **EventBridge + SQS:** Decouples emission from storage; DLQ and idempotency protect against loss and duplicates.
- **Pre-computed profile in DynamoDB:** Fast reads, always available, and doubles as cold-start fallback when Aurora is starting.
- **Hybrid profile (rules + LLM):** Stats are deterministic; Gemini only turns them into natural-language narrative, reducing hallucination.

## Alternatives Considered

| Approach | Rejected Because |
|----------|-------------------|
| Full-context RAG (load all history into prompt) | No semantic ranking; prompt size and cost grow with history |
| OpenSearch Serverless | Minimum cost ~$700/month; overkill for single-user |
| Direct Lambda persistence (no EventBridge) | Tighter coupling; harder to add consumers and replay |
| Triage-only scope | Misses selection overrides and caption style, which carry strong signal |
| Feature-flag rollout | Chosen strategy is big-bang; RAG is best-effort so main flow never blocks |

## Consequences

**Positive:**

- Triage, selection, and captions can improve over time using observed keep/discard and override patterns.
- Single RAG stack can be deployed or torn down independently.
- Best-effort design: RAG and EventBridge failures do not break triage, selection, or publish.
- Cost remains low (Aurora off when idle; Bedrock Titan embeddings on the order of dollars per month).

**Trade-offs:**

- Aurora cold start (~30s) is hidden by serving cached profile from DynamoDB.
- Weekly profile batch means preference text can be up to a week stale until the next run.
- New AWS surface: Aurora, EventBridge, Bedrock, five new Lambdas, and new DynamoDB table.

## Related Documents

- [RAG Decision Memory](../rag-decision-memory.md) — feature overview and operations
- [RAG_PLANNING.md](../../../RAG_PLANNING.md) — full implementation plan and schema (workspace root)
- DDR-050 (async dispatch), DDR-053 (granular Lambda split) — patterns used by existing Lambdas now emitting events
