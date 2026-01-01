# Gemini Media Analysis CLI

A command-line tool for uploading images and videos to Google's Gemini API and conducting in-depth analysis through stateful conversation sessions.

## Features

- 📤 **Direct File Upload**: Upload images and videos directly to Gemini API, bypassing typical UI file size limits
- 💬 **Stateful Conversations**: Maintain context across multiple questions about uploaded media
- 🎯 **In-Depth Analysis**: Ask detailed questions about visual content using Gemini's multimodal capabilities
- 💾 **Session Management**: Create, switch, and manage multiple analysis sessions
- 🚀 **Fast & Efficient**: Built with Go for fast startup and efficient file handling
- 📦 **Single Binary**: Easy deployment with a single executable file

## Quick Start

### Prerequisites

- Go 1.21 or later
- Gemini API key ([Get one here](https://makersuite.google.com/app/apikey))
- **FFmpeg** (required for video compression)
  - macOS: `brew install ffmpeg`
  - Linux: `apt install ffmpeg`
  - Ensure FFmpeg includes `libsvtav1` (AV1 encoder) and `libopus` (Opus audio)

### Installation

```bash
# Clone the repository
git clone <repository-url>
cd gemini-media-social-network

# Install dependencies
go mod download

# Build
go build -o gemini-cli ./cmd/gemini-cli

# Set your API key
export GEMINI_API_KEY="your-api-key-here"
```

### Usage

```bash
# Analyze photos in a directory (with context for better selection)
./gemini-cli --directory /path/to/photos --context "Weekend trip to Kyoto"
./gemini-cli -d ./vacation-photos -c "Birthday party at restaurant then karaoke"

# Interactive mode - prompts for directory and context
./gemini-cli

# With options
./gemini-cli -d ./photos --max-depth 2 --limit 50

# Show help
./gemini-cli --help
```

### Photo Selection

The CLI uses **quality-agnostic photo selection** - photo quality is NOT a selection criterion since you can enhance photos with Google's tools (Magic Editor, Unblur, Portrait Light, etc.).

Instead, selection prioritizes:
1. **Subject diversity**: food, architecture, landscape, people, activities
2. **Scene representation**: ensure each sub-event/location is covered
3. **Enhancement potential**: for duplicates, pick easiest to enhance

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
gemini-media-social-network/
├── cmd/gemini-cli/          # CLI entry point
│   └── main.go
├── internal/                # Internal packages
│   ├── auth/               # API key retrieval & validation
│   ├── chat/               # Text & image question/answer
│   ├── filehandler/        # Media file loading & EXIF extraction
│   └── logging/            # Structured logging
├── scripts/                 # Setup scripts
│   └── setup-gpg-credentials.sh
├── docs/                    # Design documentation
│   ├── index.md            # Documentation index
│   ├── architecture.md     # System architecture (current state)
│   ├── media_analysis.md   # Media analysis design
│   ├── design-decisions/   # Historical decision records (DDRs)
│   ├── authentication.md   # Auth design
│   └── ...                 # See docs/index.md
├── go.mod                   # Go module definition
├── README.md                # This file
└── plan.md                 # Implementation roadmap
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
go build -o gemini-cli ./cmd/gemini-cli
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
- [ ] Mixed media directories (images + videos)
- [ ] Session management
- [ ] Cloud storage integration (S3, Google Drive)

## License

[To be determined]

## Contributing

[To be added]

