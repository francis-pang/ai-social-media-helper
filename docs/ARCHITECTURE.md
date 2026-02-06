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

1. **CLI Binaries** - Two independent commands under `cmd/`:
   - `media-select` - AI-powered media selection for Instagram carousels
   - `media-triage` - AI-powered media triage to identify and delete unsaveable files
2. **File Handler** - File validation, EXIF extraction, thumbnail generation
3. **Gemini Client** - API communication and file uploads
4. **Photo Selection** - Quality-agnostic selection with scene detection
5. **Media Triage** - Batch quality/meaning evaluation with interactive deletion
6. **Session Manager** - Stateful conversation management (future)
7. **Configuration** - API key and settings management

---

## Technical Stack

| Component | Technology | Purpose |
|-----------|-----------|---------|
| **Language** | Go 1.23+ | Core language |
| **Gemini Model** | `gemini-3-flash-preview` | AI model (free tier compatible) |
| **SDK** | `github.com/google/generative-ai-go/genai` | Official Gemini API SDK |
| **Logging** | `github.com/rs/zerolog` | Structured logging |
| **CLI Framework** | `github.com/spf13/cobra` | Command-line interface |
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
│ Cobra Parser    │  (cmd/media-select/ or cmd/media-triage/)
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

## Photo Selection Flow (Iteration 10)

```
┌─────────────────┐
│ Directory Scan  │  Recursive, sorted by path
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ EXIF Extraction │  GPS, Date, Camera info
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ Thumbnail Gen   │  1024px max, JPEG output
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ Gemini API      │  Thumbnails + Metadata + Context
│ Selection       │
└────────┬────────┘
         │
         ▼
┌─────────────────────────────────────────┐
│ Structured Output                        │
│ 1. Ranked list with justification       │
│ 2. Scene grouping (hybrid detection)    │
│ 3. Exclusion report (per-photo reasons) │
└─────────────────────────────────────────┘
```

### Quality-Agnostic Selection

**Key Principle**: Photo quality is NOT a selection criterion. User has Google enhancement tools (Magic Editor, Unblur, Portrait Light, etc.).

**Selection Priorities**:
1. Subject/Scene Diversity (Highest)
2. Scene Representation
3. Enhancement Potential (duplicates only)
4. People Variety (Lower)
5. Time of Day (Tiebreaker)

**Scene Detection (Hybrid)**:
- Visual similarity
- Time gaps (2+ hours = new scene)
- GPS gaps (1km+ = new location)

See [DDR-016](./design-decisions/DDR-016-quality-agnostic-photo-selection.md) for details.

---

## Media Triage Flow (Iteration 12)

```
┌─────────────────┐
│ Directory Scan  │  Recursive, images + videos
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ Pre-filter      │  Videos < 2s flagged locally
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ Media Processing│  Thumbnails (images) + Compress (videos)
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ Gemini API      │  Single batch call with all media
│ Triage          │  Returns JSON array of verdicts
└────────┬────────┘
         │
         ▼
┌─────────────────────────────────────────┐
│ Interactive Report                       │
│ 1. KEEP list with reasons               │
│ 2. DISCARD list with reasons             │
│ 3. Confirm deletion prompt               │
└─────────────────────────────────────────┘
```

### Triage Criteria

**Key Principle**: Be generous — if a normal person can understand the subject and light editing could make it decent, keep it.

**Discard if:**
- Too dark/blurry to recover any meaningful content
- Accidental shot (pocket photo, floor, finger over lens)
- No discernible subject or meaning
- Video too short (< 2 seconds, pre-filtered locally)

See [DDR-021](./design-decisions/DDR-021-media-triage-command.md) for details.

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

**Last Updated**: 2026-02-06

