# Architecture Overview

## Purpose

The Gemini Media Analysis CLI enables users to:
- Upload media files (images/videos) directly to Gemini API, bypassing typical UI file size limits
- Maintain stateful conversation sessions for asking multiple questions about uploaded media
- Perform in-depth analysis of visual content using Gemini's multimodal capabilities

## Language Choice: Go

**Selected for:**
- ⚡ **Fast startup times** - Native binary, no JVM overhead
- 📦 **Single binary deployment** - Easy distribution, no dependencies
- 🚀 **Efficient file handling** - Excellent streaming support for large files
- 🛠️ **CLI-first design** - Strong ecosystem for command-line tools
- 🔒 **Type safety** - Compile-time error checking
- 🧵 **Concurrency** - Built-in goroutines for efficient concurrent operations

See [language_comparison.md](./language_comparison.md) for detailed comparison with alternatives.

---

## Core Components

1. **CLI Interface** - Command parsing and user interaction
2. **File Handler** - File validation, reading, and preparation
3. **Gemini Client** - API communication and file uploads
4. **Session Manager** - Stateful conversation management
5. **Configuration** - API key and settings management

---

## Technical Stack

| Component | Technology | Purpose |
|-----------|-----------|---------|
| **Language** | Go 1.23+ | Core language |
| **Gemini Model** | `gemini-3-flash-preview` | AI model (free tier compatible) |
| **SDK** | `github.com/google/generative-ai-go/genai` | Official Gemini API SDK |
| **Logging** | `github.com/rs/zerolog` | Structured logging |
| **CLI Framework** | `github.com/spf13/cobra` (planned) | Command-line interface |
| **Configuration** | Environment variables + GPG | Config and secret management |
| **JSON** | `encoding/json` | Session persistence |
| **File I/O** | `os`, `io`, `mime` | File handling |
| **UUID** | `github.com/google/uuid` | Session IDs |
| **Testing** | `testing` package | Unit tests |
| **Build** | `go build` | Single binary output |
| **Dependencies** | `go.mod` + `go.sum` | Dependency management |

**Pricing Reference**: [Gemini API Pricing](https://ai.google.dev/gemini-api/docs/pricing)

---

## Data Flow

```
┌─────────────────┐
│  User Input     │  (CLI commands)
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ Cobra Parser    │  (cmd/gemini-cli/main.go)
│ - Parse args    │
│ - Route commands│
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ File Handler    │  (internal/filehandler/handler.go)
│ - Validate      │
│ - Read file     │
│ - Stream I/O    │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ Gemini Client   │  (internal/gemini/client.go)
│ - Upload file   │
│ - Generate      │
│ - Context-aware │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ Session Manager │  (internal/session/manager.go)
│ - Store refs    │
│ - Save history  │
│ - Persist JSON  │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ Response Output │  (fmt.Printf)
└─────────────────┘
```

### Go-Specific Characteristics

- ✅ Context propagation for cancellation/timeouts
- ✅ Error handling via `(result, error)` tuples
- ✅ Thread-safe operations with mutexes
- ✅ Streaming file I/O for large files
- ✅ No shared mutable state

---

## Future Extensibility

### Storage Provider Interface

```go
type StorageProvider interface {
    Upload(ctx context.Context, file io.Reader, filename string) (string, error)
    GetURL(ctx context.Context, fileID string) (string, error)
    Delete(ctx context.Context, fileID string) error
}
```

### Planned Implementations

1. **DirectUploadProvider** (Current):
   - Uploads directly to Gemini API
   - No intermediate storage

2. **S3StorageProvider** (Future):
   - Upload to AWS S3 bucket
   - Generate pre-signed URLs
   - Use `github.com/aws/aws-sdk-go-v2`

3. **GoogleDriveStorageProvider** (Future):
   - Upload to Google Drive
   - Share files with Gemini API
   - Use `cloud.google.com/go/storage`

### Benefits of Cloud Storage

- **Bandwidth Savings**: Upload once, reference many times
- **Persistence**: Files remain accessible across sessions
- **Scalability**: Handle larger files efficiently
- **Cost Optimization**: Reduce API transfer costs

---

**Last Updated**: 2025-12-31

