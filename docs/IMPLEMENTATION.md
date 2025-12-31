# Implementation Details

This document describes the implementation patterns for each core component.

---

## 1. Configuration Management

**Location**: `config/config.go` (planned)

**Responsibilities:**
- Load API key from environment variable `GEMINI_API_KEY` or GPG-encrypted file
- Support optional config file using Viper
- Validate configuration values
- Provide default values where appropriate

**Configuration Values:**

| Setting | Environment Variable | Default Value |
|---------|---------------------|---------------|
| API Key | `GEMINI_API_KEY` | (from GPG file) |
| Model | `GEMINI_MODEL` | `gemini-3-flash-preview` |
| Log Level | `GEMINI_LOG_LEVEL` | `info` |
| Session Directory | `GEMINI_SESSION_DIR` | `~/.gemini-media-cli/sessions` |
| Timeout | - | 30 seconds |

**Key Features:**
- Environment variable priority for API key
- GPG-encrypted file fallback at `~/.gemini-media-cli/credentials.gpg`
- Sensible defaults for all settings
- Error wrapping for debugging
- Path expansion for user directories

See [CONFIGURATION.md](./CONFIGURATION.md) for full details.

---

## 2. Gemini Client

**Location**: `internal/gemini/client.go` (planned)

**Responsibilities:**
- Initialize Gemini API client with API key
- Upload files to Gemini API
- Generate content with uploaded files
- Handle API errors and retries
- Support context cancellation

**Implementation Pattern:**
```go
package gemini

import (
    "context"
    "fmt"
    "google.golang.org/genai"
)

type Client struct {
    client *genai.Client
    model  string
}

func NewClient(apiKey, model string) (*Client, error) {
    client, err := genai.NewClient(apiKey)
    if err != nil {
        return nil, fmt.Errorf("failed to create Gemini client: %w", err)
    }
    
    return &Client{
        client: client,
        model:  model,
    }, nil
}

func (c *Client) UploadFile(ctx context.Context, filePath string) (*genai.File, error) {
    file, err := genai.UploadFile(ctx, c.client, filePath)
    if err != nil {
        return nil, fmt.Errorf("failed to upload file %s: %w", filePath, err)
    }
    return file, nil
}

func (c *Client) GenerateContent(ctx context.Context, prompt string, files []*genai.File) (string, error) {
    // Build content with files and prompt
    // Generate response
    // Return text response
}
```

**Key Features:**
- Context-aware operations
- Error wrapping with context
- File reference management
- Streaming support for large files

---

## 3. File Handler

**Location**: `internal/filehandler/handler.go` (planned)

**Responsibilities:**
- Validate file types (images: jpg, png, gif, webp; videos: mp4, mov, avi)
- Check file sizes (handle large files beyond typical UI limits)
- Read files with streaming support
- Detect MIME types
- Provide file metadata

**Implementation Pattern:**
```go
package filehandler

import (
    "fmt"
    "io"
    "mime"
    "os"
    "path/filepath"
)

type FileInfo struct {
    Path     string
    Size     int64
    MimeType string
    Name     string
}

var (
    SupportedImageTypes = map[string]bool{
        "image/jpeg": true,
        "image/png":  true,
        "image/gif":  true,
        "image/webp": true,
    }
    
    SupportedVideoTypes = map[string]bool{
        "video/mp4":  true,
        "video/quicktime": true, // mov
        "video/x-msvideo": true, // avi
    }
)

func ValidateFile(path string) (*FileInfo, error) {
    // Check file exists
    stat, err := os.Stat(path)
    if err != nil {
        return nil, fmt.Errorf("file not found: %w", err)
    }
    
    // Check file size (e.g., max 2GB)
    const maxSize = 2 * 1024 * 1024 * 1024 // 2GB
    if stat.Size() > maxSize {
        return nil, fmt.Errorf("file too large: %d bytes (max: %d)", stat.Size(), maxSize)
    }
    
    // Detect MIME type
    ext := filepath.Ext(path)
    mimeType := mime.TypeByExtension(ext)
    if mimeType == "" {
        mimeType = "application/octet-stream"
    }
    
    // Validate MIME type
    if !SupportedImageTypes[mimeType] && !SupportedVideoTypes[mimeType] {
        return nil, fmt.Errorf("unsupported file type: %s", mimeType)
    }
    
    return &FileInfo{
        Path:     path,
        Size:     stat.Size(),
        MimeType: mimeType,
        Name:     filepath.Base(path),
    }, nil
}

func ReadFile(path string) (io.Reader, error) {
    file, err := os.Open(path)
    if err != nil {
        return nil, fmt.Errorf("failed to open file: %w", err)
    }
    return file, nil
}
```

**Key Features:**
- MIME type detection
- File size validation
- Streaming file reading
- Clear error messages

---

## 4. Session Manager

**Location**: `internal/session/manager.go` (planned)

**Responsibilities:**
- Create and manage multiple sessions
- Store conversation history per session
- Track uploaded file references
- Persist sessions to disk (JSON)
- Thread-safe operations
- Session switching and cleanup

**Data Structures:**
```go
type Message struct {
    Role      string    // "user" or "assistant"
    Content   string
    Timestamp time.Time
}

type Session struct {
    ID           string
    Files        []string      // File references
    Messages     []Message
    CreatedAt    time.Time
    LastActiveAt time.Time
}
```

**Session Storage:**
- **Location**: `~/.gemini-media-cli/sessions/`
- **Format**: JSON files (`{session-id}.json`)
- **Active Session**: Tracked in memory, persisted on changes
- **Recovery**: Load all sessions on startup

**Session Operations:**
- **Create**: Generate UUID, initialize empty session
- **List**: Show all sessions with metadata
- **Switch**: Change active session
- **Clear**: Remove current session's files/messages
- **Delete**: Remove session file from disk

**Thread Safety:**
- Use `sync.RWMutex` for concurrent access
- Read locks for reading sessions
- Write locks for modifications
- Safe for concurrent CLI operations

---

## 5. Chat Package

**Location**: `internal/chat/chat.go`

**Current Implementation:**
```go
package chat

const modelName = "gemini-3-flash-preview"

// AskTextQuestion sends a text-only question to the Gemini API
func AskTextQuestion(ctx context.Context, client *genai.Client, question string) (string, error)

// BuildDailyNewsQuestion constructs a date-embedded question for testing
func BuildDailyNewsQuestion() string
```

**Key Features:**
- Date-embedded prompts for testing variability
- Structured logging of request/response
- Error handling for empty responses

---

## 6. CLI Interface

**Location**: `cmd/gemini-cli/main.go`

**Command Structure:**
```
gemini-cli
├── upload <file>              # Upload media file
├── ask <question>             # Ask question about uploaded media
├── session
│   ├── new                    # Create new session
│   ├── list                   # List all sessions
│   ├── switch <id>            # Switch active session
│   └── clear                  # Clear current session
└── interactive                # Enter interactive mode
```

**Key Features:**
- Cobra CLI framework (planned)
- Context propagation
- Error handling
- Formatted output
- Interactive mode support

See [CLI_UX.md](./CLI_UX.md) for UX design details.

---

**Last Updated**: 2025-12-31

