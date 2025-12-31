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
- `GEMINI_MODEL` (optional): Model to use (default: `gemini-2.0-flash-exp`)
- `GEMINI_SESSION_DIR` (optional): Directory for session storage (default: `~/.gemini-media-cli/sessions`)

## Project Structure

```
gemini-media-social-network/
├── cmd/gemini-cli/          # CLI entry point
│   └── main.go
├── internal/                # Internal packages
│   ├── auth/               # API key retrieval & validation
│   │   ├── auth.go
│   │   ├── auth_test.go
│   │   └── validate.go
│   └── logging/            # Structured logging
│       └── logger.go
├── scripts/                 # Setup scripts
│   └── setup-gpg-credentials.sh
├── go.mod                   # Go module definition
├── go.sum                   # Dependency checksums
├── README.md                # This file
├── AUTHENTICATION.md        # Authentication design doc
├── CLI_UX.md               # CLI UX design doc
├── CONFIGURATION.md        # Configuration design doc
├── OPERATIONS.md           # Logging/observability design doc
└── PLAN.md                 # Implementation plan
```

## Development

See [PLAN.md](./PLAN.md) for detailed implementation plan and architecture.

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
- [ ] Core implementation
- [ ] CLI interface
- [ ] Session management
- [ ] Cloud storage integration (S3, Google Drive)

## License

[To be determined]

## Contributing

[To be added]

