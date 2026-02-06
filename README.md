# Gemini Media CLI Tools

A collection of command-line tools for analyzing photos and videos using Google's Gemini API.

## Tools

| Command | Description |
|---------|-------------|
| `media-select` | AI-powered media selection for Instagram carousels |
| `media-triage` | AI-powered media triage to identify and delete unsaveable files |
| `media-web` | Web UI for visual triage and selection (local web server) |

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

- Go 1.21 or later
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
4. **Multi-select deletion** — choose exactly which files to remove

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
│   └── media-web/           # Web server (JSON API + embedded SPA)
├── web/
│   └── frontend/            # Preact SPA (TypeScript + Vite)
├── internal/                # Shared Go packages
│   ├── auth/               # API key retrieval & validation
│   ├── chat/               # Gemini API interaction (selection, triage)
│   ├── filehandler/        # Media file loading, EXIF, thumbnails, compression
│   ├── logging/            # Structured logging
│   └── assets/             # Embedded prompts and reference photos
├── scripts/                 # Setup scripts
├── docs/                    # Design documentation
│   ├── design-decisions/   # Historical decision records (DDR-001 to DDR-022)
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
- [x] Web UI architecture - Preact SPA with Go JSON API (DDR-022)
- [ ] Web UI implementation - visual triage with thumbnails and multi-select
- [ ] Session management
- [ ] Cloud storage integration (S3, Google Drive)
- [ ] AWS Lambda migration (see docs/PHASE2-REMOTE-HOSTING.md)

## License

[To be determined]

## Contributing

[To be added]

