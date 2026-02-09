# Gemini Media CLI Tools

A collection of Go tools for analyzing, selecting, and enhancing photos and videos using Google's Gemini API.

## Tools

| Command | Description |
|---------|-------------|
| `media-select` | AI-powered media selection for Instagram carousels |
| `media-triage` | AI-powered media triage to identify and delete unsaveable files |
| `media-web` | Web UI for visual triage and selection (local web server) |
| `media-lambda` | Cloud-hosted API service via AWS Lambda + S3 + CloudFront |
| `thumbnail-lambda` | Per-file thumbnail generation (Step Functions worker) |
| `selection-lambda` | AI media selection via Gemini (Step Functions worker) |
| `enhance-lambda` | Per-photo AI enhancement via Gemini (Step Functions worker) |
| `video-lambda` | Per-video enhancement via ffmpeg (Step Functions worker) |

## Quick Start

### Prerequisites

- Go 1.24 or later
- Gemini API key ([Get one here](https://makersuite.google.com/app/apikey))
- FFmpeg with `libsvtav1` and `libopus` (required for video compression)
- Node.js 18+ (required only for building the web UI)

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
| `GEMINI_API_KEY` | Yes | — | Gemini API key |
| `GEMINI_MODEL` | No | `gemini-3-flash` | Model to use |
| `GEMINI_LOG_LEVEL` | No | `info` | Log level: debug, info, warn, error |

See [docs/authentication.md](./docs/authentication.md) for GPG-encrypted credential storage and cloud authentication (Cognito).

## Documentation

All design documentation lives in [docs/](./docs/index.md):

- **Architecture** — [architecture.md](./docs/architecture.md) — system components, local + cloud deployment
- **Workflows** — [media-triage.md](./docs/media-triage.md), [media-selection.md](./docs/media-selection.md)
- **Media processing** — [image-processing.md](./docs/image-processing.md), [video-processing.md](./docs/video-processing.md)
- **Design decisions** — [50 DDRs](./docs/design-decisions/) documenting every architectural choice

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
- [ ] Media enhancement (Steps 4-5): Gemini 3 Pro Image + Imagen 3
- [ ] Post grouping and publishing (Steps 6-7, 9)
- [ ] Video triage in Lambda (requires FFmpeg Lambda layer)
- [ ] DynamoDB session state store (handler migration)
- [ ] Custom domain with ACM certificate

## Testing

```bash
go test ./...
go test -cover ./...
```

## License

[To be determined]
