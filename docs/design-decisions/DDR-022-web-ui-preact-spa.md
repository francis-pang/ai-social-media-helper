# DDR-022: Web UI with Preact SPA and Go JSON API

**Date**: 2026-02-06  
**Status**: Accepted  
**Iteration**: 13

## Context

The existing CLI tools (`media-triage`, `media-select`) perform destructive or curatorial actions on media files. Currently, the only confirmation mechanism is a text-based "Delete N file(s)? (y/N)" prompt — the user never sees thumbnails of what will be deleted or selected before confirming.

Users need a visual confirmation step: view thumbnails of media flagged for deletion (or selected for a carousel), multi-select which items to act on, and confirm. A terminal cannot render images and videos, so a graphical interface is required.

Additionally, the long-term goal is to evolve the Go backend into an AWS Lambda function behind API Gateway, with the frontend hosted remotely on a static hosting platform (see `docs/PHASE2-REMOTE-HOSTING.md`). This means the frontend and backend must be cleanly separated from day one — the frontend consumes a JSON REST API, and the backend never renders HTML.

### Constraints

1. The frontend must be a standalone SPA that consumes JSON — no server-side HTML rendering (eliminates Go templates, HTMX)
2. The SPA must be embeddable in the Go binary for Phase 1 (local `localhost` use)
3. The same SPA must be deployable to a static hosting platform for Phase 2 (remote use)
4. Security is a priority: strict CSP, minimal supply chain surface, XSS prevention
5. The scope is modest: file browser, thumbnail grid, multi-select confirmation — not a complex application

## Decision

### 1. Preact SPA for the Frontend

Use **Preact** (with Preact Signals for state management) as the frontend framework, built with Vite.

Preact was chosen over six evaluated alternatives:

| Framework | Bundle Size (gzip) | Runtime Deps | CSP Compatibility | Rejected Because |
|-----------|--------------------|--------------|-------------------|------------------|
| Vanilla JS | 0 KB | 0 | Trivial | No component model, no TypeScript, manual DOM manipulation becomes unwieldy |
| **Preact** | **~4 KB** | **0** | **Excellent** | **(Selected)** |
| Svelte 5 | ~2-3 KB | 0 (inlined) | Excellent | Svelte-specific syntax is less transferable; smaller ecosystem |
| Solid.js | ~7 KB | 0 | Excellent | Smallest community, youngest ecosystem, risky long-term bet |
| Vue 3 | ~33 KB | Moderate | Moderate (inline styles from libraries) | Larger bundle, Vue-specific syntax, painful v2->v3 history |
| React 19 | ~42 KB | 200-800+ transitive | Hardest (many libs need `unsafe-inline`) | Massive supply chain surface, overkill for this scope |

**Why Preact specifically:**

- **Zero runtime dependencies** — the `preact` npm package has 0 transitive deps. Only your code and Preact ship to the browser.
- **React-compatible API** — same JSX, same hooks, same component model. Transferable knowledge. Can use React tutorials and AI assistance.
- **3 KB gzipped** — 1/14th of React's bundle size.
- **Strict CSP works out of the box** — no inline style injection, no `unsafe-eval` needed.
- **Migration path to React** — if the UI outgrows Preact, switching to React is mechanical (same JSX, same hooks, change the import).
- **Preact Signals** — fine-grained reactivity for state management without Redux/Zustand overhead.

### 2. Go JSON REST API Backend

Add a new binary `cmd/media-web/main.go` that starts a local HTTP server using Go's `net/http`. This server:

- Serves the Preact SPA (static files embedded via `embed.FS`)
- Exposes JSON REST endpoints for the frontend to consume
- Reuses existing `internal/` packages (`filehandler`, `chat`, `auth`, `assets`)

The Go server **never renders HTML**. All responses are JSON. The frontend handles all rendering.

### 3. Directory Structure

```
ai-social-media-helper/
├── cmd/
│   ├── media-select/          # Existing CLI
│   ├── media-triage/          # Existing CLI
│   └── media-web/             # NEW: Web server binary
│       └── main.go
├── internal/                  # Shared Go packages (unchanged)
├── web/                       # NEW: Frontend code
│   └── frontend/              # Preact SPA
│       ├── package.json
│       ├── vite.config.ts
│       ├── tsconfig.json
│       ├── index.html
│       ├── src/
│       │   ├── main.tsx       # Entry point
│       │   ├── app.tsx        # Root component
│       │   ├── api/           # API client (fetch wrappers)
│       │   ├── components/    # UI components
│       │   ├── pages/         # Page-level components
│       │   └── types/         # TypeScript type definitions
│       └── dist/              # Build output (gitignored, embedded by Go)
├── docs/
└── ...
```

The `web/frontend/` directory is a self-contained Preact project. The `dist/` output is embedded into the Go binary at build time. In Phase 2, the same `dist/` is deployed to a static host.

### 4. Embed Strategy for Phase 1

The Go web server embeds the frontend build output:

```go
//go:embed web/frontend/dist/*
var frontendFS embed.FS

func main() {
    // Serve SPA static files
    http.Handle("/", http.FileServer(http.FS(frontendFS)))
    // Serve JSON API
    http.Handle("/api/", apiRouter())
    http.ListenAndServe(":8080", nil)
}
```

### 5. Phase 1 API Endpoints (Triage Flow)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/browse?path=...` | List directories and files at a path |
| `POST` | `/api/triage/start` | Start triage processing for selected files |
| `GET` | `/api/triage/{id}/status` | Poll triage job status |
| `GET` | `/api/triage/{id}/results` | Get triage results (keep/discard with reasons) |
| `GET` | `/api/media/thumbnail?path=...` | Get thumbnail for a media file |
| `GET` | `/api/media/full?path=...` | Serve full-resolution media file (DDR-024) |
| `POST` | `/api/triage/{id}/confirm` | Confirm deletion of selected files |

### 6. Build Workflow

```bash
# Development (two terminals)
cd web/frontend && npm run dev     # Vite dev server with HMR
cd . && go run ./cmd/media-web     # Go API server (proxies to Vite in dev)

# Production build
cd web/frontend && npm run build   # Produces dist/
go build -o media-web ./cmd/media-web  # Embeds dist/ into binary
```

## Rationale

### Why not server-side rendering (Go templates, HTMX)?

Server-rendered approaches couple HTML rendering to the Go server. When the Go backend migrates to AWS Lambda (Phase 2), Lambda should return JSON, not HTML. Using Go templates or HTMX would require a full frontend rewrite at that point. The SPA approach means the frontend is unchanged — only the API base URL changes.

### Why Preact over React?

React's ecosystem is larger, but its supply chain surface is 200-800x larger than Preact's (by transitive dependency count). For a personal tool with a modest UI (file browser, thumbnail grid, multi-select), React's ecosystem advantages don't justify the supply chain and bundle size overhead. Preact provides the same developer experience (JSX, hooks, component model) at 1/14th the size with zero runtime dependencies.

### Why a separate `web/` directory?

The `web/frontend/` directory keeps frontend code cleanly separated from Go code. This supports the long-term goal of splitting into separate repositories (frontend, backend server, library). The Go module root stays clean — `node_modules/` and frontend tooling don't pollute the Go project.

### Why embed in Go binary for Phase 1?

Embedding via `embed.FS` produces a single self-contained binary — the same deployment model as the existing CLI tools. Users run one command, the browser opens, and everything works. No separate frontend server to manage.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Go templates + HTMX | Tightly couples rendering to Go server; requires full rewrite for Lambda migration |
| Vanilla JS (no framework) | No component model, no TypeScript, code becomes unwieldy past ~1000 LOC |
| React 19 | 42KB bundle, 200-800+ transitive deps, hard to configure strict CSP, overkill for scope |
| Svelte 5 | Excellent technical profile but Svelte-specific syntax is less transferable than Preact's React-compatible API |
| Vue 3 | Larger bundle (33KB), Vue-specific syntax, some component libraries require `unsafe-inline` CSP |
| Solid.js | Youngest framework, smallest community — risky for long-term maintenance |
| Desktop app (Wails) | Adds CGo dependency, complex build pipeline, overkill for local tool |

## Consequences

**Positive:**

- Visual confirmation of media actions — users see thumbnails before deleting or selecting
- Clean frontend/backend separation from day one — trivial Lambda migration in Phase 2
- Zero runtime supply chain risk — only Preact (0 deps) ships to the browser
- Strict CSP works out of the box — `script-src 'self'`, `style-src 'self'`
- React-compatible knowledge — JSX, hooks, component patterns transfer to React if needed
- Single binary deployment for Phase 1 — same model as existing CLI tools
- `web/frontend/` is self-contained — can be extracted to its own repo when needed

**Trade-offs:**

- Adds Node.js + npm as a build-time dependency (not a runtime dependency)
- Two build steps (npm + go) instead of one — mitigated by a Makefile
- Preact's ecosystem is smaller than React's — fewer pre-built component libraries
- `preact/compat` layer for React library compatibility adds ~2KB and can have subtle differences

## Implementation

### New Files

| File | Purpose |
|------|---------|
| `cmd/media-web/main.go` | Web server binary: embed SPA, serve JSON API, start browser |
| `web/frontend/` | Preact SPA project (package.json, vite.config.ts, src/, etc.) |
| `docs/PHASE2-REMOTE-HOSTING.md` | Phase 2 options document (hosting platforms, Lambda migration) |
| `docs/design-decisions/DDR-022-web-ui-preact-spa.md` | This decision record |

### Modified Files

| File | Changes |
|------|---------|
| `.gitignore` | Add `web/frontend/node_modules/`, `web/frontend/dist/` |
| `docs/architecture.md` | Add web UI component, Phase 1/2 architecture diagram |
| `docs/index.md` | Add links to new documents |
| `docs/design-decisions/index.md` | Add DDR-022 entry |
| `PLAN.md` | Add web UI phases, update directory structure |
| `README.md` | Add `media-web` tool documentation |

### Shared Code Reused

| Package | Functions Used |
|---------|----------------|
| `internal/filehandler` | `ScanDirectoryMediaWithOptions()`, `GenerateThumbnail()` |
| `internal/chat` | `AskMediaTriage()`, `AskMediaSelection()` |
| `internal/auth` | `GetAPIKey()`, `ValidateAPIKey()` |
| `internal/logging` | `Init()` |

## Related Decisions

- DDR-014: Thumbnail-Based Multi-Image Selection Strategy
- DDR-019: Externalized Prompt Templates
- DDR-020: Mixed Media Selection Strategy
- DDR-021: Media Triage Command with Batch AI Evaluation

## Testing Approach

1. **API tests**: Go handler tests with `httptest` for each JSON endpoint
2. **Frontend tests**: Preact component tests with Vitest + Testing Library (optional, scope-dependent)
3. **Integration tests**: End-to-end flow from file browser to deletion confirmation
4. **Manual testing**: Real media directories with the web UI in a browser
