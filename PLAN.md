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
gemini-media-social-network/
├── cmd/
│   └── gemini-cli/
│       └── main.go                    # Entry point with validation
│
├── internal/                          # Private packages
│   ├── auth/
│   │   ├── auth.go                   # API key retrieval (env + GPG + passphrase file)
│   │   ├── auth_test.go              # Auth tests
│   │   └── validate.go               # API key validation with error types
│   │
│   ├── chat/
│   │   └── chat.go                   # Text Q&A with date-embedded prompts
│   │
│   ├── logging/
│   │   └── logger.go                 # Structured logging with zerolog
│   │
│   ├── filehandler/                  # (Future)
│   ├── session/                      # (Future)
│   └── storage/                      # (Future)
│
├── scripts/
│   └── setup-gpg-credentials.sh      # GPG credential setup helper
│
├── docs/                              # Documentation
│   ├── index.md                      # Documentation index
│   ├── architecture.md               # System architecture (current state)
│   ├── implementation.md             # Implementation details (current state)
│   ├── media_analysis.md             # Media analysis design
│   ├── design-decisions/             # Historical decision records
│   │   ├── index.md                  # Decision index
│   │   ├── design_template.md        # ADR template
│   │   └── DDR-*.md                  # Individual decisions
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
└── plan.md                            # This file
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

### Phase 2: Media Uploads (Iterations 7-10)

- [x] **Iteration 7**: Single image upload with EXIF extraction and social media generation
- [ ] **Iteration 8**: Image directory upload
- [ ] **Iteration 9**: Single video upload
- [ ] **Iteration 10**: Mixed media directory (images + videos)

### Phase 3: Session Management (Iterations 11-13)

- [ ] **Iteration 11**: Multi-question single session with REPL
- [ ] **Iteration 12**: Session persistence to disk (JSON)
- [ ] **Iteration 13**: Session management commands

### Phase 4: CLI Polish (Iterations 14-16)

- [ ] **Iteration 14**: Dynamic CLI arguments
- [ ] **Iteration 15**: Interactive mode
- [ ] **Iteration 16**: Progress indicators and UX polish

### Phase 5: Advanced Features (Iterations 17-19)

- [ ] **Iteration 17**: Configuration file support with Viper
- [ ] **Iteration 18**: Batch operations with concurrency
- [ ] **Iteration 19**: Cloud storage integration (S3/GDrive)

---

## Getting Started

### Prerequisites

- Go 1.23 or later
- Gemini API key from [Google AI Studio](https://aistudio.google.com/)
- GPG (for secure credential storage)

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
   go build -o gemini-cli ./cmd/gemini-cli
   ./gemini-cli
   ```

4. **Enable debug logging**:
   ```bash
   GEMINI_LOG_LEVEL=debug ./gemini-cli
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

**Last Updated**: 2025-12-31  
**Version**: 1.3.0  
**Status**: Implementation Phase (Iteration 7 Complete)
