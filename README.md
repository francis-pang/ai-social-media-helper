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
# Upload a media file
./gemini-cli upload image.jpg

# Ask a question about uploaded media
./gemini-cli ask "What objects are in this image?"

# Create a new session
./gemini-cli session new

# List all sessions
./gemini-cli session list

# Switch to a different session
./gemini-cli session switch <session-id>

# Enter interactive mode
./gemini-cli interactive
```

## Supported File Types

### Images
- JPEG (.jpg, .jpeg)
- PNG (.png)
- GIF (.gif)
- WebP (.webp)

### Videos
- MP4 (.mp4)
- QuickTime (.mov)
- AVI (.avi)

## Configuration

The application uses environment variables for configuration:

- `GEMINI_API_KEY` (required): Your Gemini API key
- `GEMINI_MODEL` (optional): Model to use (default: `gemini-3-flash-preview`). See [Gemini API Pricing](https://ai.google.dev/gemini-api/docs/pricing) for available models.
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
│   ├── chat/               # Text question/answer functionality
│   └── logging/            # Structured logging
├── scripts/                 # Setup scripts
│   └── setup-gpg-credentials.sh
├── docs/                    # Design documentation
│   ├── ARCHITECTURE.md     # System architecture
│   ├── DESIGN_DECISIONS.md # Key decisions
│   ├── AUTHENTICATION.md   # Auth design
│   └── ...                 # See docs/README.md
├── go.mod                   # Go module definition
├── README.md                # This file
└── PLAN.md                 # Implementation roadmap
```

## Documentation

| Topic | Document |
|-------|----------|
| Implementation Roadmap | [PLAN.md](./PLAN.md) |
| All Design Docs | [docs/README.md](./docs/README.md) |
| Architecture | [docs/ARCHITECTURE.md](./docs/ARCHITECTURE.md) |
| Design Decisions | [docs/DESIGN_DECISIONS.md](./docs/DESIGN_DECISIONS.md) |

## Development

See [PLAN.md](./PLAN.md) for implementation roadmap and [docs/](./docs/) for detailed design documentation.

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
- [ ] Media uploads (images, videos)
- [ ] CLI interface with Cobra
- [ ] Session management
- [ ] Cloud storage integration (S3, Google Drive)

## License

[To be determined]

## Contributing

[To be added]

