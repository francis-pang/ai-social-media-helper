# Gemini Media CLI Tools

A collection of command-line tools for analyzing photos and videos using Google's Gemini API.

## Tools

| Command | Description |
|---------|-------------|
| `media-select` | AI-powered media selection for Instagram carousels |
| `media-triage` | AI-powered media triage to identify and delete unsaveable files |
| `media-web` | Web UI for visual triage and selection (local web server, Phase 1) |
| `media-lambda` | Cloud-hosted triage via AWS Lambda + S3 (Phase 2) |

## Features

- 📤 **Direct File Upload**: Upload images and videos directly to Gemini API
- 🎯 **Media Selection**: AI selects the best photos/videos for social media posts
- 🗑️ **Media Triage**: AI identifies unsaveable media (blurry, dark, accidental) for cleanup
- 🎬 **Mixed Media**: Photos and videos are analyzed equally
- 🚀 **Fast & Efficient**: Built with Go for fast startup and efficient file handling
- 🌐 **Web UI**: Visual triage with thumbnail previews and multi-select confirmation
- 📦 **Multi-Binary**: Each tool is an independent executable

## Quick Start

### Prerequisites

- Go 1.24 or later
- Gemini API key ([Get one here](https://makersuite.google.com/app/apikey))
- **FFmpeg** (required for video compression)
  - macOS: `brew install ffmpeg`
  - Linux: `apt install ffmpeg`
  - Ensure FFmpeg includes `libsvtav1` (AV1 encoder) and `libopus` (Opus audio)
- **Node.js 18+** (required only for building the web UI)
  - macOS: `brew install node`

### Installation

```bash
# Clone the repository
git clone <repository-url>
cd ai-social-media-helper

# Install dependencies
go mod download

# Build CLI tools
go build -o media-select ./cmd/media-select
go build -o media-triage ./cmd/media-triage

# Build web UI (requires Node.js)
make build-web

# Set your API key
export GEMINI_API_KEY="your-api-key-here"
```

### Usage: media-select

Select the best media for an Instagram carousel post.

```bash
# Analyze photos and videos in a directory (with context for better selection)
./media-select --directory /path/to/photos --context "Weekend trip to Kyoto"
./media-select -d ./vacation-photos -c "Birthday party at restaurant then karaoke"

# Interactive mode - prompts for directory and context
./media-select

# With options
./media-select -d ./photos --max-depth 2 --limit 50

# Specify a different model
./media-select -d ./media --model gemini-3-pro-preview

# Show help
./media-select --help
```

### Usage: media-triage

Identify and delete unsaveable photos and videos from a directory.

```bash
# Triage media in a directory (interactive - prompts before deletion)
./media-triage --directory /path/to/photos
./media-triage -d ./vacation-photos

# Dry run - show report without prompting for deletion
./media-triage -d ./photos --dry-run

# With options
./media-triage -d ./photos --max-depth 2 --limit 100

# Specify a different model
./media-triage -d ./media --model gemini-3-pro-preview

# Show help
./media-triage --help
```

### Usage: media-web

Visual web UI for triaging media with thumbnail previews.

```bash
# Start the web server (opens browser to http://localhost:8080)
./media-web

# Use a different port
./media-web --port 9090

# Specify a different model
./media-web --model gemini-3-pro-preview
```

The web UI provides:
1. **File browser** — navigate directories and select media files
2. **Triage processing** — AI evaluates media and categorizes as keep/discard
3. **Visual confirmation** — view thumbnails of flagged media before deleting
4. **Full-image preview** — click any thumbnail to open the full-resolution file in a new tab
5. **Multi-select deletion** — choose exactly which files to remove

### Cloud Deployment (Phase 2)

The same triage workflow is also available as a cloud-hosted service:

- **Frontend**: Preact SPA on CloudFront (`https://d10rlnv7vz8qt7.cloudfront.net`)
- **Backend**: Go Lambda function behind API Gateway
- **Storage**: S3 with presigned URL uploads (files auto-expire after 24 hours)

The cloud version presents a **landing page** where users choose between two workflows (DDR-042):

- **Media Triage**: Drag-and-drop file upload → AI evaluation → delete bad files
- **Media Selection**: File/folder picker → AI-powered selection → enhance → group → caption → publish (DDR-029, DDR-030)

Files are uploaded directly to S3 via presigned PUT URLs. The selection workflow uses Chrome's File System Access API for native file/folder picking with recursive media filtering and client-side thumbnail generation. After upload, Gemini analyzes all media in a single call for comparative selection (scene detection, deduplication, content evaluation) and returns structured JSON results. The review UI displays selected/excluded items with justifications, scene groups, and user override capability.

```bash
# Build and deploy the Lambda binary
GOARCH=amd64 GOOS=linux CGO_ENABLED=0 go build -o bootstrap ./cmd/media-lambda
zip -j function.zip bootstrap
aws lambda update-function-code --function-name AiSocialMediaApiHandler --zip-file fileb://function.zip

# Build and deploy the frontend (cloud mode)
cd web/frontend
VITE_CLOUD_MODE=1 npm run build
aws s3 sync dist/ s3://ai-social-media-frontend-123456789012/ --delete
aws cloudfront create-invalidation --distribution-id EFVHUDLKPXL4H --paths "/*"
```

See [DDR-026](./docs/design-decisions/DDR-026-phase2-lambda-s3-deployment.md) for the full architecture decision record.

### Media Selection

The CLI uses **quality-agnostic media selection** - quality is NOT a selection criterion since you can enhance photos with Google's tools (Magic Editor, Unblur, Portrait Light, etc.).

**Mixed Media Support**: The tool scans for both images AND videos. Photos and videos compete equally in selection - a compelling 15-second video may be better than multiple similar photos. See [DDR-020](./docs/design-decisions/DDR-020-mixed-media-selection.md) for details.

Selection prioritizes:
1. **Subject diversity**: food, architecture, landscape, people, activities
2. **Scene representation**: ensure each sub-event/location is covered
3. **Media type synergy**: choose whether a moment is better as photo or video
4. **Audio content**: consider music, speech, ambient sounds in videos
5. **Enhancement potential**: for duplicates, pick easiest to enhance

Provide trip context with `--context` / `-c` to help Gemini understand your event.

## Supported File Types

### Images
- JPEG (.jpg, .jpeg)
- PNG (.png)
- GIF (.gif)
- WebP (.webp)
- HEIC (.heic) - iPhone photos
- HEIF (.heif) - High Efficiency Image Format

### Videos
- MP4 (.mp4)
- QuickTime (.mov)
- AVI (.avi)
- WebM (.webm)
- Matroska (.mkv)

**Note:** All videos are automatically compressed before upload using AV1+Opus codecs for optimal Gemini efficiency. A 1GB video typically compresses to ~2MB while preserving AI-analyzable quality. See [DDR-018](./docs/design-decisions/DDR-018-video-compression-gemini3.md) for details.

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

The application uses environment variables for configuration:

- `GEMINI_API_KEY` (required): Your Gemini API key
- `GEMINI_MODEL` (optional): Model to use (default: `gemini-3-flash`). See [Gemini API Pricing](https://ai.google.dev/gemini-api/docs/pricing) for available models.
  - `gemini-3-flash` - Fast, cost-effective (default)
  - `gemini-3-pro` - Higher quality, recommended for media analysis
- `GEMINI_LOG_LEVEL` (optional): Log level - debug, info, warn, error (default: `info`)
- `GEMINI_SESSION_DIR` (optional): Directory for session storage (default: `~/.gemini-media-cli/sessions`)

### GPG Passphrase File (Optional)

For non-interactive environments (CI/CD, automated testing), create a `.gpg-passphrase` file in the project root containing your GPG key passphrase. This file is gitignored and allows automated GPG decryption without user interaction.

## Project Structure

```
ai-social-media-helper/
├── cmd/
│   ├── media-select/        # Media selection CLI (Instagram carousel)
│   ├── media-triage/        # Media triage CLI (identify unsaveable files)
│   ├── media-web/           # Local web server (JSON API + embedded SPA, Phase 1)
│   └── media-lambda/        # AWS Lambda entry points + Dockerfiles (Phase 2)
│       ├── main.go          # API Lambda handler
│       ├── Dockerfile       # Original single-image build (DDR-027)
│       ├── Dockerfile.light # Parameterized: Go binary only (DDR-035)
│       └── Dockerfile.heavy # Parameterized: Go binary + ffmpeg (DDR-035)
├── web/
│   └── frontend/            # Preact SPA (TypeScript + Vite, dual-mode)
│       └── src/
│           ├── components/
│           │   ├── FileBrowser.tsx    # Native OS picker (Phase 1)
│           │   ├── FileUploader.tsx   # Drag-and-drop S3 upload (triage, Phase 2)
│           │   ├── LandingPage.tsx    # Workflow chooser (DDR-042)
│           │   ├── MediaUploader.tsx  # File System Access API upload (selection, DDR-029)
│           │   ├── SelectedFiles.tsx  # Confirm selection (both modes)
│           │   └── TriageView.tsx     # Results and deletion (both modes)
│           ├── api/client.ts          # API client with cloud/local mode
│           └── types/api.ts           # Shared TypeScript types
├── internal/                # Shared Go packages (used by all binaries)
│   ├── auth/               # API key retrieval & validation
│   ├── chat/               # Gemini API interaction (selection, triage, enhancement)
│   ├── filehandler/        # Media file loading, EXIF, thumbnails, compression
│   ├── logging/            # Structured logging
│   └── assets/             # Embedded prompts and reference photos
├── scripts/                 # Setup scripts
├── docs/                    # Design documentation
│   ├── design-decisions/   # Historical decision records (DDR-001 to DDR-042)
│   ├── DOCKER-IMAGES.md    # Docker image strategy, ECR layer sharing, and registry strategy (DDR-035, DDR-041)
│   └── ...                 # See docs/index.md
├── Makefile                 # Build orchestration
├── go.mod                   # Go module definition
├── README.md                # This file
└── PLAN.md                 # Implementation roadmap
```

## Documentation

| Topic | Document |
|-------|----------|
| Implementation Roadmap | [plan.md](./plan.md) |
| All Design Docs | [docs/index.md](./docs/index.md) |
| Architecture | [docs/architecture.md](./docs/architecture.md) |
| Docker Image Strategy | [docs/DOCKER-IMAGES.md](./docs/DOCKER-IMAGES.md) |
| Design Decisions | [docs/design-decisions/](./docs/design-decisions/) |
| Media Analysis | [docs/media_analysis.md](./docs/media_analysis.md) |

## Development

See [plan.md](./plan.md) for implementation roadmap and [docs/](./docs/) for detailed design documentation.

### Building

```bash
# CLI tools only
go build -o media-select ./cmd/media-select
go build -o media-triage ./cmd/media-triage

# Web UI (builds frontend then embeds into Go binary)
make build-web

# Everything
make all
```

### Testing

```bash
go test ./...
```

### Running Tests with Coverage

```bash
go test -cover ./...
```

## Roadmap

- [x] Project planning and architecture
- [x] Foundation (connection, logging, auth, validation)
- [x] Text question/answer with date-embedded prompts
- [x] Single image upload with EXIF metadata extraction
- [x] Social media content generation from images
- [x] Image directory batch processing with photo selection
- [x] CLI interface with Cobra (--directory, --max-depth, --limit flags)
- [x] Video uploads with Files API
- [x] Quality-agnostic photo selection with user context (--context flag)
- [x] Video compression for Gemini optimization (AV1+Opus, DDR-018)
- [x] Externalized prompt templates for faster iteration (DDR-019)
- [x] Mixed media directories - images + videos with unified selection (DDR-020)
- [x] Model selection flag (--model / -m)
- [x] Multi-binary CLI layout (media-select + media-triage)
- [x] Media triage - AI identifies unsaveable photos/videos for deletion (DDR-021)
- [x] Web UI - Preact SPA with Go JSON API (DDR-022)
- [x] Web UI - visual triage with thumbnails and multi-select (DDR-024)
- [x] AWS IAM deployment user with scoped policies (DDR-023)
- [x] SSM Parameter Store for runtime secrets (DDR-025)
- [x] Phase 2 cloud deployment - Lambda + S3 + CloudFront (DDR-026)
- [x] S3 presigned URL upload with drag-and-drop frontend
- [x] CloudFront API proxy for same-origin requests
- [x] CodePipeline CI/CD (GitHub source, Go + Node builds, S3 + Lambda deploy)
- [x] Media selection Step 1: File System Access API upload with thumbnails and trip context (DDR-029)
- [x] Media selection Step 2: AI-powered selection with structured JSON output and thumbnail pre-generation (DDR-030)
- [x] Media selection Step 3: Review selection with override, scene groups, and exclusion reasons (DDR-030)
- [x] Multi-Lambda deployment: 5 Lambdas, 2 Step Functions, DynamoDB, 2 ECR repos, split pipelines (DDR-035)
- [x] Container registry strategy: ECR Private for proprietary code, ECR Public for generic images (DDR-041)
- [x] Landing page workflow switcher: choose between triage and selection in cloud mode (DDR-042)
- [ ] Media selection Step 4-5: AI-powered media enhancement with feedback loops
- [ ] Media selection Step 6-7: Post grouping, publishing/download
- [x] Media selection Step 8: AI post description with full media context and iterative feedback (DDR-036)
- [ ] DynamoDB session state store (Go implementation: `internal/store/`)
- [ ] Video triage in Lambda (requires FFmpeg Lambda layer)
- [ ] Custom domain with ACM certificate
- [ ] Session management

## License

[To be determined]

## Contributing

[To be added]

