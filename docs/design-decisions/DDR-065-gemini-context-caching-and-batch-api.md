# DDR-065: Gemini Context Caching and Batch API Integration

**Date**: 2026-02-19  
**Status**: Accepted  
**Iteration**: n/a

## Context

The application makes multiple Gemini API calls per user session, often reusing the same system prompt + media context across steps (e.g., Triage → Selection → Description). Each call re-sends the full prompt and media, incurring redundant input token costs. For high-volume triage and auto-description workloads where real-time latency is not required, the Gemini Batch API offers a 50% cost reduction.

Two optimization opportunities:

1. **Context Caching** — When the same system prompt and media files are sent in multiple consecutive Gemini calls within a session (e.g., selection followed by description), the shared context can be cached server-side by Gemini. Subsequent calls reference the cache instead of re-transmitting the full context, reducing latency and cost.

2. **Batch API** — For non-interactive bulk workloads (triage of 100+ files, auto-description of all post groups), requests can be accumulated into a JSONL file, submitted as a batch job, and processed asynchronously at 50% cost.

## Decision

### Phase 1: Context Caching (this implementation)

Introduce a `CacheManager` in `internal/chat/cache.go` that wraps Gemini's `Caches.Create` API. The cache stores the system instruction and media parts for reuse across multiple `GenerateContent` calls within the same session.

**Cache lifecycle:**

- Created on-demand when a media-heavy operation (selection, description) starts.
- Keyed by `{sessionID}:{operation}` for uniqueness.
- TTL of 1 hour (default), sufficient for a user session to complete all steps.
- Automatically deleted when the session ends or cache expires.
- Falls back to inline context if cache creation fails (minimum 4096 tokens required).

**Integration points:**

- `AskMediaSelectionJSON` — Cache system prompt + media parts; reuse for follow-up description calls.
- `GenerateDescription` — Reuse selection cache if available; create own cache for multi-turn feedback.
- `AskMediaTriage` — Cache per-batch system prompt + media for batched triage.

### Phase 2: Batch API (future)

Batch API integration is deferred to a future DDR. It requires GCS bucket provisioning and a polling mechanism (Step Functions or scheduled Lambda) to check batch job completion. The architecture change is significant enough to warrant its own design decision once the caching layer proves value.

## Rationale

1. **Cost reduction** — Media uploads (videos, high-res images) dominate input tokens. Caching avoids re-sending the same media bytes across selection → description flows.
2. **Latency improvement** — Cached context is pre-processed server-side; subsequent calls skip tokenization of cached content.
3. **Graceful fallback** — If caching fails (token count below minimum, API error), the existing inline flow works unchanged. No user-visible impact.
4. **Minimal code change** — The `CacheManager` is a thin wrapper; callers opt-in by passing a `CacheConfig` to their existing functions.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Client-side prompt deduplication | Would only reduce network transfer, not API token costs. Gemini still tokenizes the full prompt on each call. |
| Batch API first | Requires GCS infrastructure and a new polling architecture. Caching is simpler and addresses the most common case (interactive sessions). |
| Long-lived cache (24h TTL) | S3 media objects expire in 24h but session data is typically consumed within 1 hour. Longer TTL wastes cache storage cost. |
| Cache per-file instead of per-session | Would fragment the cache and require complex invalidation when files are added/removed mid-session. |

## Consequences

**Positive:**

- Reduced input token costs for multi-step workflows (selection → description).
- Lower latency on subsequent Gemini calls that reuse cached context.
- No architectural changes required — existing `GenerateContent` calls are augmented, not replaced.
- Observable via new `GeminiCacheHit` / `GeminiCacheMiss` metrics.

**Trade-offs:**

- Minimum 4096 tokens required for caching — very short prompts without media will not benefit.
- Cache entries consume Gemini storage (billed per GB-hour), though the 1h TTL limits exposure.
- Batch API (50% cost reduction for bulk workloads) is deferred to a future iteration.

## Related Documents

- [DDR-019](./DDR-019-externalized-prompt-templates.md) — Externalized Prompt Templates
- [DDR-020](./DDR-020-mixed-media-selection.md) — Mixed Media Selection Strategy
- [DDR-030](./DDR-030-cloud-selection-backend.md) — Cloud Selection Backend Architecture
- [DDR-036](./DDR-036-ai-post-description.md) — AI Post Description Generation
- [DDR-060](./DDR-060-s3-presigned-urls-for-gemini.md) — S3 Presigned URLs for Gemini
- [DDR-064](./DDR-064-gemini-3.1-pro-model-upgrade.md) — Model Upgrade to gemini-3.1-pro-preview
