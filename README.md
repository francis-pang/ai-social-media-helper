# AI Social Media Helper

A collection of Go tools for analyzing, selecting, enhancing, and captioning photos and videos using Google's Gemini API and Vertex AI.

## Workflows

| Workflow | Description |
|----------|-------------|
| **Media Triage** | AI-powered triage to identify and delete unsaveable files |
| **Media Selection + Instagram** | AI selection, enhancement, grouping, captions, and Instagram publish |
| **Facebook Prep** | AI-generated captions, location tags, and date/time stamps for Facebook uploads |

## Lambdas

| Command | Description |
|---------|-------------|
| `media-select` | AI-powered media selection for Instagram carousels (CLI) |
| `media-triage` | AI-powered media triage to identify and delete unsaveable files (CLI) |
| `media-web` | Web UI for visual triage and selection (local web server) |
| `media-lambda` | Cloud-hosted API service via AWS Lambda + S3 + CloudFront |
| `triage-lambda` | Triage pipeline processing (DDR-053) |
| `description-lambda` | AI caption generation + feedback (DDR-053) |
| `download-lambda` | ZIP bundle creation (DDR-053) |
| `publish-lambda` | Instagram publish pipeline (DDR-053) |
| `thumbnail-lambda` | Per-file thumbnail generation (Step Functions worker) |
| `selection-lambda` | AI media selection via Gemini (Step Functions worker) |
| `enhance-lambda` | Per-photo AI enhancement + feedback via Gemini (Step Functions worker) |
| `video-lambda` | Per-video enhancement via ffmpeg (Step Functions worker) |
| `fb-prep-lambda` | Facebook caption + location + timestamp generation with Google Maps grounding |
| `gemini-batch-poll` | Lightweight Vertex AI / Gemini Batch API status poller (Step Functions worker) |

## Quick Start

### Prerequisites

- Go 1.24 or later
- FFmpeg with `libsvtav1` and `libopus` (required for video compression)
- Node.js 18+ (required only for building the web UI)
- **GCP service account** with Vertex AI permissions (cloud mode) — or standalone Gemini API key (fallback)

### Build and Run

```bash
git clone <repository-url>
cd ai-social-media-helper

go mod download

# Build all tools
make all

# Or build individually
go build -o media-select ./cmd/media-select
go build -o media-triage ./cmd/media-triage
make build-web

# Set your API key
export GEMINI_API_KEY="your-api-key-here"
```

### Usage

```bash
# Select best media for an Instagram carousel
./media-select -d /path/to/photos -c "Weekend trip to Kyoto"

# Triage media — identify and delete unsaveable files
./media-triage -d /path/to/photos
./media-triage -d ./photos --dry-run    # preview without deleting

# Start the local web UI
./media-web                              # opens http://localhost:8080

# Show help
./media-select --help
./media-triage --help
```

## CLI Options

### media-select

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--directory` | `-d` | (prompt) | Directory containing media to analyze |
| `--context` | `-c` | (prompt) | Trip/event description for better selection |
| `--model` | `-m` | `gemini-3-flash-preview` | Gemini model to use |
| `--max-depth` | | 0 (unlimited) | Maximum recursion depth |
| `--limit` | | 0 (unlimited) | Maximum media items to process |

### media-triage

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--directory` | `-d` | (prompt) | Directory containing media to triage |
| `--model` | `-m` | `gemini-3-flash-preview` | Gemini model to use |
| `--max-depth` | | 0 (unlimited) | Maximum recursion depth |
| `--limit` | | 0 (unlimited) | Maximum media items to process |
| `--dry-run` | | false | Show report without prompting for deletion |

## Configuration

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `VERTEX_AI_PROJECT` | Cloud (primary) | — | GCP project ID (`gen-lang-client-0436578028`) |
| `VERTEX_AI_REGION` | Cloud (primary) | — | GCP region (`us-east4`) |
| `GCP_SERVICE_ACCOUNT_JSON` | Cloud (primary) | — | GCP service account JSON (sourced from SSM) |
| `GEMINI_API_KEY` | Fallback | — | Standalone Gemini API key (free-tier fallback) |
| `GEMINI_MODEL` | No | `gemini-3-flash` | Model to use |
| `GEMINI_LOG_LEVEL` | No | `info` | Log level: debug, info, warn, error |

The AI client automatically selects the backend: **Vertex AI** is used when `VERTEX_AI_PROJECT` is set; the standalone Gemini API is the fallback when only `GEMINI_API_KEY` is present. See [DDR-077](./docs/design-decisions/DDR-077-cost-aware-vertex-ai-migration.md) for the full dual-backend strategy.

See [docs/authentication.md](./docs/authentication.md) for GPG-encrypted credential storage and cloud authentication (Cognito).

## Documentation

All design documentation lives in [docs/](./docs/index.md):

- **Architecture** — [architecture.md](./docs/architecture.md) — system components, local + cloud deployment
- **Workflows** — [media-triage.md](./docs/media-triage.md), [media-selection.md](./docs/media-selection.md)
- **Media processing** — [image-processing.md](./docs/image-processing.md), [video-processing.md](./docs/video-processing.md)
- **Design decisions** — [77 DDRs](./docs/design-decisions/) documenting every architectural choice

## Roadmap

- [x] Foundation (logging, auth, validation, CLI with Cobra)
- [x] Media uploads with EXIF extraction and quality-agnostic selection
- [x] Video compression (AV1+Opus), mixed media selection
- [x] Media triage with batch AI evaluation
- [x] Web UI (Preact SPA + Go JSON API)
- [x] Cloud deployment (Lambda + S3 + CloudFront + DynamoDB)
- [x] Multi-Lambda architecture with Step Functions
- [x] Landing page workflow switcher, AI post descriptions
- [x] Container registry strategy (ECR Private + ECR Public)
- [x] Step Functions Lambda entrypoints (Thumbnail, Selection, Enhancement, Video)
- [x] Step Functions polling for triage + publish (DDR-052: eliminates idle Lambda compute)
- [x] Granular Lambda split: Worker → 4 domain-specific Lambdas + shared bootstrap (DDR-053)
- [x] Gemini context caching for selection, triage, and description (DDR-065)
- [x] RAG decision memory: feedback-driven personalization (DDR-066)
- [x] RAG daily batch architecture: DynamoDB staging, daily profile rebuild, 3 Lambdas (DDR-068)
- [x] Facebook Prep workflow: session-aware captions, Google Maps grounding, date/time stamps (DDR-077)
- [x] Vertex AI migration: dual-backend (Vertex AI primary + Gemini API fallback), GCP service account (DDR-077)
- [x] Economy Mode: Gemini Batch API (50% cost savings) for non-interactive workflows (DDR-077)
- [x] Gemini Batch Poll Step Function + lightweight Lambda for server-side batch polling (DDR-077)
- [ ] SOLID/DRY refactoring: shared handler helpers, store generics, chat utilities, interface segregation
- [ ] Custom domain with ACM certificate

## Testing

```bash
go test ./...
go test -cover ./...
```

## License

MIT
