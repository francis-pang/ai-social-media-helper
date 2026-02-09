# Gemini Media Analysis CLI - Implementation Plan

## Overview

This document outlines the implementation roadmap for building a **Go-based CLI application** that enables users to upload images and videos directly to Google's Gemini API and maintain stateful conversation sessions for in-depth media analysis.

**Project Name**: Gemini Media Analysis CLI  
**Language**: Go 1.23+  
**Repository**: `/Users/fpang/code/miniature-disco/gemini-media-social-network`

For detailed documentation, see the [docs/](./docs/) folder.

---

## Project Structure

```
ai-social-media-helper/
├── cmd/
│   ├── media-select/
│   │   └── main.go                    # Media selection CLI entry point
│   └── media-triage/
│       └── main.go                    # Media triage CLI entry point
│
├── internal/                          # Shared private packages
│   ├── auth/
│   │   ├── auth.go                   # API key retrieval (env + GPG + passphrase file)
│   │   ├── auth_test.go              # Auth tests
│   │   └── validate.go               # API key validation with error types
│   │
│   ├── chat/
│   │   ├── chat.go                   # Text Q&A with date-embedded prompts
│   │   ├── model.go                  # Model configuration
│   │   ├── selection.go              # Multi-image photo/media selection
│   │   └── triage.go                 # Batch media triage evaluation
│   │
│   ├── logging/
│   │   └── logger.go                 # Structured logging with zerolog
│   │
│   ├── filehandler/
│   │   ├── media.go                  # MediaFile struct and loading
│   │   ├── image.go                  # Image metadata extraction (EXIF)
│   │   ├── video.go                  # Video metadata extraction (ffprobe)
│   │   ├── video_compress.go         # AV1+Opus video compression
│   │   └── directory.go              # Directory scanning and thumbnails
│   │
│   ├── assets/
│   │   ├── assets.go                 # Asset embedding (reference photos)
│   │   ├── prompts.go                # Prompt template embedding and rendering
│   │   ├── prompts/                  # Prompt text files
│   │   │   ├── system-instruction.txt
│   │   │   ├── selection-system.txt
│   │   │   ├── triage-system.txt
│   │   │   ├── social-media-image.txt
│   │   │   ├── social-media-video.txt
│   │   │   └── social-media-generic.txt
│   │   └── reference-photos/
│   │       └── francis-reference.jpg
│   │
│   ├── session/                      # (Future)
│   └── storage/                      # (Future)
│
├── scripts/
│   └── setup-gpg-credentials.sh      # GPG credential setup helper
│
├── docs/                              # Documentation
│   ├── index.md                      # Documentation index
│   ├── ARCHITECTURE.md               # System architecture (current state)
│   ├── implementation.md             # Implementation details (current state)
│   ├── media_analysis.md             # Media analysis design
│   ├── design-decisions/             # Historical decision records
│   │   ├── index.md                  # Decision index
│   │   ├── design_template.md        # ADR template
│   │   └── DDR-*.md                  # Individual decisions (DDR-001 through DDR-042)
│   ├── authentication.md             # Auth design
│   ├── configuration.md              # Config options
│   ├── operations.md                 # Logging/observability
│   ├── CLI_UX.md                     # CLI UX design
│   ├── testing.md                    # Testing strategy
│   └── language_comparison.md        # Go vs alternatives
│
├── .gpg-passphrase                    # GPG passphrase file (gitignored)
├── go.mod                             # Go module definition
├── go.sum                             # Dependency checksums
├── .gitignore                         # Git ignore rules
├── README.md                          # User documentation
└── PLAN.md                            # This file
```

---

## Development Roadmap

### Phase 1: Foundation (Iterations 1-6) ✅

- [x] **Iteration 1**: Basic connection validation - go.mod and minimal main.go
- [x] **Iteration 2**: Logging infrastructure with zerolog
- [x] **Iteration 3**: GPG encryption setup script
- [x] **Iteration 4**: GPG integration in Go (internal/auth package)
- [x] **Iteration 5**: API key validation with typed error handling
- [x] **Iteration 6**: Hardcoded text question/answer with date-embedded prompts

### Phase 2: Media Uploads (Iterations 7-11)

- [x] **Iteration 7**: Single image upload with EXIF extraction and social media generation
- [x] **Iteration 8**: Image directory upload with thumbnail-based photo selection
  - Scans directory for images, generates thumbnails (1024px max)
  - Sends thumbnails + metadata to Gemini for selection
  - Returns ranked list of up to 20 representative photos with justification
  - See [DDR-014](./docs/design-decisions/DDR-014-thumbnail-selection-strategy.md)
- [x] **Iteration 9**: Single video upload
- [x] **Iteration 10**: Quality-agnostic metadata-driven photo selection
  - Quality is NOT a criterion (user has Google enhancement tools)
  - Subject diversity as highest priority (food, architecture, landscape, people, activities)
  - Hybrid scene detection (visual + time 2hr+ + GPS 1km+)
  - User trip context for informed selection
  - Three-part output: ranked list, scene grouping, exclusion report
  - See [DDR-016](./docs/design-decisions/DDR-016-quality-agnostic-photo-selection.md)
- [x] **Iteration 11**: Mixed media directory (images + videos)
- [x] **Iteration 12**: Multi-binary layout + media triage command (DDR-021)

### Phase 3: Web UI (Iterations 13-15) 🔨

- [x] **Iteration 13**: Web UI architecture decision (DDR-022: Preact SPA + Go JSON API)
- [ ] **Iteration 14**: Go web server with JSON API (browse, thumbnail, triage endpoints)
- [ ] **Iteration 15**: Preact frontend (file browser, thumbnail grid, multi-select confirm)

### Phase 4: Session Management (Iterations 16-18)

- [ ] **Iteration 16**: Multi-question single session with REPL
- [ ] **Iteration 17**: Session persistence to disk (JSON)
- [ ] **Iteration 18**: Session management commands

### Phase 5: CLI Polish (Iterations 19-21)

- [ ] **Iteration 19**: Dynamic CLI arguments with Cobra
- [ ] **Iteration 20**: Interactive mode (REPL for multi-turn conversations)
- [ ] **Iteration 21**: Progress indicators and UX polish

### Phase 6: Advanced Features (Iterations 22-24)

- [ ] **Iteration 22**: Configuration file support with Viper
- [ ] **Iteration 23**: Batch operations with concurrency
- [ ] **Iteration 24**: Cloud storage integration (S3/GDrive)

### Phase 7: Remote Deployment (Future)

- [ ] AWS Lambda migration (Go backend)
- [ ] S3 + CloudFront hosting (Preact frontend)
- [ ] API Gateway + Cognito authentication
- See [docs/PHASE2-REMOTE-HOSTING.md](./docs/PHASE2-REMOTE-HOSTING.md) for options

---

## Getting Started

### Prerequisites

- Go 1.23 or later
- Gemini API key from [Google AI Studio](https://aistudio.google.com/)
- GPG (for secure credential storage)
- ffprobe (for video metadata extraction)
- macOS `sips` (for HEIC thumbnail generation)

### Quick Setup

1. **Get API Key**: Visit [Google AI Studio](https://aistudio.google.com/app/apikey)

2. **Store credentials**:
   ```bash
   # Option A: Environment variable
   export GEMINI_API_KEY="your-api-key-here"
   
   # Option B: GPG encrypted file
   ./scripts/setup-gpg-credentials.sh
   ```

3. **Build and run**:
   ```bash
   # CLI tools
   go build -o media-select ./cmd/media-select
   go build -o media-triage ./cmd/media-triage
   ./media-select
   ./media-triage

   # Web UI (requires Node.js for frontend build)
   make build-web
   ./media-web
   ```

4. **Enable debug logging**:
   ```bash
   GEMINI_LOG_LEVEL=debug ./media-select
   GEMINI_LOG_LEVEL=debug ./media-triage
   ```

### Common Issues

| Issue | Solution |
|-------|----------|
| "API key not found" | Set `GEMINI_API_KEY` or run setup script |
| "GPG decryption failed" | Check GPG key with `gpg --list-keys` |
| "Invalid API key" | Regenerate at [Google AI Studio](https://aistudio.google.com/app/apikey) |
| "API quota exceeded" | Wait for reset or check [usage](https://ai.dev/usage) |

---

## Documentation

| Topic | Document |
|-------|----------|
| Architecture & Data Flow | [docs/architecture.md](./docs/architecture.md) |
| Design Decisions | [docs/design-decisions/](./docs/design-decisions/) |
| Implementation Patterns | [docs/implementation.md](./docs/implementation.md) |
| Media Analysis | [docs/media_analysis.md](./docs/media_analysis.md) |
| Authentication | [docs/authentication.md](./docs/authentication.md) |
| Configuration | [docs/configuration.md](./docs/configuration.md) |
| Logging & Operations | [docs/operations.md](./docs/operations.md) |
| CLI UX Design | [docs/CLI_UX.md](./docs/CLI_UX.md) |
| Testing Strategy | [docs/testing.md](./docs/testing.md) |

---

## External References

- [Gemini API Pricing](https://ai.google.dev/gemini-api/docs/pricing) - Model pricing and free tier limits
- [Gemini API Documentation](https://ai.google.dev/gemini-api/docs) - Official API docs
- [Google AI Studio](https://aistudio.google.com/) - API key management

---

**Last Updated**: 2026-02-06  
**Version**: 1.8.0  
**Status**: Implementation Phase (Iteration 13 — Web UI Architecture)
