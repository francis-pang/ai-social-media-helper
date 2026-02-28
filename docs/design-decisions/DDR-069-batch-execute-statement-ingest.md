# DDR-069: BatchExecuteStatement for RAG Ingest Throughput

**Date**: 2026-02-28
**Status**: Accepted
**Iteration**: Cloud — performance optimization
**Extends**: DDR-068 (daily batch architecture)

## Context

DDR-068 introduced daily batch processing where the Profile Builder Lambda reads staging items, generates embeddings, and upserts to Aurora. Currently, `ingestStagingItems` processes items sequentially: each item flows through `embedAndUpsert` → `UpsertXxxDecision` → `exec()` → `ExecuteStatement`, issuing one Aurora Data API round trip per item.

A typical session with 60 triage + 20 selection + 5 caption items = 85 sequential `ExecuteStatement` calls. While embedding generation is the bottleneck (~100-200ms per item), the sequential DB writes add unnecessary latency and risk hitting Data API rate limits at scale.

The `BatchUpsertXxx` methods already exist in `internal/rag/dataapi.go` (added in the initial implementation) but `ingestStagingItems` in `cmd/rag-profile-lambda/main.go` still calls per-item `UpsertXxx` methods.

## Decision

Refactor `ingestStagingItems` to a two-phase approach:

| Phase | Before | After |
|-------|--------|-------|
| **Embed** | Per-item embed + upsert in one loop | Phase A: embed all items, collect into typed slices |
| **Upsert** | N sequential `ExecuteStatement` calls | Phase B: group by event type, one `BatchExecuteStatement` per type |
| **Chunking** | N/A (no batching) | `batchExec` chunks at 50 items per call (conservative for 4 MiB API limit) |
| **Error handling** | Per-item: skip on failure | Batch: on chunk failure, fall back to per-item for that chunk |

API limits:
- `BatchExecuteStatement` max request size: 4 MiB
- Each parameter set with a 1024-dim embedding vector is ~8 KB serialized, so ~400 items fit per batch
- Conservative chunk size of 50 leaves ample headroom

## Rationale

- **Throughput**: 85 items across 5 types becomes ~5 `BatchExecuteStatement` calls instead of 85 `ExecuteStatement` calls.
- **Latency**: Batch reduces total DB write time from ~85 × 50ms ≈ 4.3s to ~5 × 100ms ≈ 0.5s.
- **Rate limits**: Fewer API calls means less risk of Data API throttling.
- **Fallback safety**: Per-item fallback on batch failure preserves current behavior as a degraded path.
- **No new code for batch methods**: `BatchUpsertXxx` methods already exist; this change only refactors the caller.

## Alternatives Considered

| Approach | Rejected Because |
|----------|-----------------|
| Keep per-item upsert | Wastes latency; batch methods already exist unused |
| Transaction-based batch | Data API transactions have a 40 KB limit, too small for embedding vectors |
| Concurrent per-item goroutines | Adds complexity without reducing total API calls; risk of Data API throttling |

## Consequences

**Positive:**
- Profile Lambda batch phase drops from ~4.3s to ~0.5s for DB writes (85 items typical).
- Total Data API calls reduced from N to ceil(N/50) per event type.
- Existing `BatchUpsertXxx` methods are now actually used.
- Per-item `UpsertXxx` methods retained for fallback and other callers.

**Trade-offs:**
- Batch failure loses the entire chunk (mitigated by per-item fallback).
- Slightly more complex error handling (two-phase with fallback).

## Related Documents

- DDR-068 (RAG Daily Batch Architecture) — defines the batch processing flow
- DDR-066 (RAG Decision Memory) — original Aurora ingest design
