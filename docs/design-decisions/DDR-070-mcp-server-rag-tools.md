# DDR-070: MCP Server for RAG Tools and Model-Driven Media Fetch

**Date**: 2026-02-28
**Status**: Accepted
**Iteration**: Cloud — architecture evolution

## Context

The RAG system (DDR-066, DDR-068) preloads the full preference profile and injects it into every Gemini prompt, regardless of whether the model needs it. This wastes input tokens on obvious triage decisions. RAG tool definitions are scattered — each Lambda knows how to invoke the RAG Query Lambda, but there is no shared tool contract. The RAG Query Lambda speaks a bespoke JSON protocol, not reusable outside the Lambda ecosystem. There is no way to use RAG tools from an IDE (Cursor, Claude Code) for development and debugging.

The current single-pass triage sends ALL media content (thumbnails + compressed videos) to Gemini in one batch, even for items that are obviously unsaveable from metadata alone (e.g., a 0.3s accidental video).

## Decision

Build an MCP (Model Context Protocol) server using the official Go SDK (`github.com/modelcontextprotocol/go-sdk`) that exposes RAG data and media fetching as tools. Deploy as both an embedded in-process server (for Lambda) and a standalone binary (for IDE development).

| Layer | Component | Purpose |
|-------|-----------|---------|
| **1. MCP Package** | `internal/mcp/` | Server with 4 tools: `get_preference_profile`, `get_caption_examples`, `get_curation_stats`, `fetch_media` |
| **2. Gemini Bridge** | `internal/chat/mcpbridge.go` | Auto-converts MCP tool definitions to Gemini `FunctionDeclaration`s; routes `FunctionCall` to MCP; handles multimodal results (images → InlineData, videos → FileData) |
| **3. Triage Flow** | `AskMediaTriageMCP` in `triage.go` | Two-pass triage: metadata-only first pass + model-driven `fetch_media` for items needing visual inspection |
| **4. Standalone Binary** | `cmd/mcp-server/main.go` | Same MCP server over stdio transport for Cursor/Claude Code IDE integration |
| **5. Lambda Integration** | `RAG_MODE=mcp` env toggle | Opt-in switch from preload mode to MCP mode; Lambdas read DynamoDB directly |

Key design choices:
- **In-process, not network**: MCP server runs in the same process as the Lambda — zero network overhead, just function calls via the bridge.
- **`fetch_media` smart routing**: Videos below 20 MiB use S3 presigned URLs (Gemini fetches directly); larger videos upload via the Gemini Files API to avoid Google-side fetch timeouts.
- **Opt-in toggle**: `RAG_MODE` env var (`preload` default vs `mcp`) allows safe rollout with instant rollback.
- **Multimodal bridge**: MCP `ImageContent` → Gemini `InlineData`; video `_media_ref` JSON → Gemini `FileData`. All media parts bundled with `FunctionResponse` in a single Content turn.

## Rationale

- **Token savings**: Model pulls RAG context only when needed; skips obvious metadata-only triage items.
- **Video token savings**: Sub-0.5s accidental videos triaged from metadata alone — skips the most expensive tokens (120 tokens/frame at 1 fps).
- **IDE development**: Same MCP server runs standalone for prompt debugging in Cursor/Claude Code.
- **Unified tool contract**: MCP tools are the single source of truth; Gemini FunctionDeclarations auto-generated from MCP definitions.
- **Eliminates Lambda-to-Lambda hop**: In MCP mode, triage/selection Lambdas read DynamoDB directly; RAG Query Lambda is bypassed.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Keep preload-only RAG | Wastes tokens on every call; no IDE reuse; bespoke JSON protocol |
| HTTP-based MCP transport | Adds network latency and deployment complexity for Lambda use case |
| Separate MCP server Lambda | Additional cold start, network hop; in-process is zero-overhead |
| Custom Gemini tool framework | Vendor lock-in; MCP is an open standard with growing ecosystem |
| Always-fetch (no metadata-only triage) | Misses the easy wins for obviously unsaveable items |

## Consequences

**Positive:**

- RAG context is on-demand instead of always-preloaded, saving input tokens.
- Model can skip media fetch for obvious metadata-only decisions (short videos, known patterns).
- Same tools usable from IDE for development, debugging, and prompt iteration.
- MCP is an open standard — tools are reusable across any MCP-compatible client.
- Eliminates RAG Query Lambda invoke hop when in MCP mode.
- Presigned URL size threshold raised from 10 MiB to 20 MiB (Google expanded signed-URL support in Jan 2026).

**Trade-offs:**

- Extra 1-3s latency per tool round trip (2-3 Gemini API calls instead of 1).
- MCP mode is an additional code path to maintain alongside preload mode.
- `RAG_MODE=mcp` requires DynamoDB read grants on triage/selection Lambdas (IAM change in CDK).
- Fallback: if model never calls `fetch_media`, `AskMediaTriageMCP` falls back to single-pass.

## Related Documents

- DDR-066 (RAG Decision Memory) — original RAG design
- DDR-068 (RAG Daily Batch Architecture) — batch processing flow
- DDR-069 (BatchExecuteStatement) — ingest throughput optimization
