# Gemini Media Analysis CLI - Implementation Plan

## Overview

This document outlines the complete implementation plan for building a **Go-based CLI application** that enables users to upload images and videos directly to Google's Gemini API, maintain stateful conversation sessions for in-depth media analysis, and is architected for future cloud storage integration (AWS S3 and Google Drive).

**Project Name**: Gemini Media Analysis CLI  
**Language**: Go 1.21+  
**Repository**: `/Users/fpang/code/miniature-disco/gemini-media-social-network`

## Table of Contents

1. [Architecture Overview](#architecture-overview)
2. [Project Structure](#project-structure)
3. [Implementation Details](#implementation-details)
4. [Technical Stack](#technical-stack)
5. [Data Flow](#data-flow)
6. [File Upload Strategy](#file-upload-strategy)
7. [Session Management](#session-management)
8. [Gemini Model Configuration](#gemini-model-configuration)
9. [Error Handling](#error-handling)
10. [Design Decisions](#design-decisions)
11. [Future Extensibility](#future-extensibility)
12. [Development Roadmap](#development-roadmap)
13. [Getting Started](#getting-started)

---

## Architecture Overview

### Purpose

The application enables users to:
- Upload media files (images/videos) directly to Gemini API, bypassing typical UI file size limits
- Maintain stateful conversation sessions for asking multiple questions about uploaded media
- Perform in-depth analysis of visual content using Gemini's multimodal capabilities

### Language Choice: Go

**Selected for:**
- ⚡ **Fast startup times** - Native binary, no JVM overhead
- 📦 **Single binary deployment** - Easy distribution, no dependencies
- 🚀 **Efficient file handling** - Excellent streaming support for large files
- 🛠️ **CLI-first design** - Strong ecosystem for command-line tools
- 🔒 **Type safety** - Compile-time error checking
- 🧵 **Concurrency** - Built-in goroutines for efficient concurrent operations

### Core Components

1. **CLI Interface** - Command parsing and user interaction
2. **File Handler** - File validation, reading, and preparation
3. **Gemini Client** - API communication and file uploads
4. **Session Manager** - Stateful conversation management
5. **Configuration** - API key and settings management

---

## Project Structure

### Go Package-Based Organization

```
gemini-media-social-network/
├── cmd/
│   └── gemini-cli/
│       └── main.go                    # Entry point with validation
│
├── internal/                          # Private packages
│   ├── auth/
│   │   ├── auth.go                   # API key retrieval (env + GPG)
│   │   ├── auth_test.go              # Auth tests
│   │   └── validate.go               # API key validation with error types
│   │
│   ├── logging/
│   │   └── logger.go                 # Structured logging with zerolog
│   │
│   ├── filehandler/                  # (Future)
│   │   ├── handler.go                # File validation & upload preparation
│   │   └── handler_test.go           # Tests co-located
│   │
│   ├── session/                      # (Future)
│   │   ├── manager.go                # Session management
│   │   ├── session.go                # Session data structures
│   │   └── manager_test.go           # Tests co-located
│   │
│   └── storage/                      # (Future)
│       ├── provider.go               # StorageProvider interface
│       ├── direct.go                 # DirectUploadProvider
│       └── s3.go                     # S3StorageProvider
│
├── scripts/
│   └── setup-gpg-credentials.sh      # GPG credential setup helper
│
├── go.mod                             # Go module definition
├── go.sum                             # Dependency checksums
├── .gitignore                         # Git ignore rules
├── README.md                          # User documentation
├── AUTHENTICATION.md                  # Authentication design doc
├── CLI_UX.md                          # CLI UX design doc
├── CONFIGURATION.md                   # Configuration design doc
├── OPERATIONS.md                      # Logging/observability design doc
└── PLAN.md                            # This file
```

### Key Go Conventions

- **Package = Directory**: Each directory is a package
- **Flat Structure**: Fewer nested directories than Java
- **Co-located Tests**: `*_test.go` files next to source code
- **Internal Packages**: `internal/` prevents external imports
- **Single Main**: `cmd/` contains executable entry points
- **Explicit Exports**: Capitalized names = public API

---

## Implementation Details

### 1. Configuration Management (`config/config.go`)

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

---

### 2. Gemini Client (`internal/gemini/client.go`)

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

### 3. File Handler (`internal/filehandler/handler.go`)

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

### 4. Session Manager (`internal/session/manager.go`)

**Responsibilities:**
- Create and manage multiple sessions
- Store conversation history per session
- Track uploaded file references
- Persist sessions to disk (JSON)
- Thread-safe operations
- Session switching and cleanup

**Implementation Pattern:**
```go
package session

import (
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "sync"
    "time"
    
    "github.com/google/uuid"
)

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

type Manager struct {
    mu       sync.RWMutex
    sessions map[string]*Session
    activeID string
    baseDir  string
}

func NewManager(baseDir string) (*Manager, error) {
    // Create directory if it doesn't exist
    if err := os.MkdirAll(baseDir, 0755); err != nil {
        return nil, fmt.Errorf("failed to create session directory: %w", err)
    }
    
    m := &Manager{
        sessions: make(map[string]*Session),
        baseDir:  baseDir,
    }
    
    // Load existing sessions
    if err := m.loadSessions(); err != nil {
        return nil, fmt.Errorf("failed to load sessions: %w", err)
    }
    
    return m, nil
}

func (m *Manager) CreateSession() (*Session, error) {
    m.mu.Lock()
    defer m.mu.Unlock()
    
    id := uuid.New().String()
    session := &Session{
        ID:           id,
        Files:        []string{},
        Messages:     []Message{},
        CreatedAt:    time.Now(),
        LastActiveAt: time.Now(),
    }
    
    m.sessions[id] = session
    m.activeID = id
    
    if err := m.saveSession(session); err != nil {
        return nil, fmt.Errorf("failed to save session: %w", err)
    }
    
    return session, nil
}

func (m *Manager) GetActiveSession() *Session {
    m.mu.RLock()
    defer m.mu.RUnlock()
    
    return m.sessions[m.activeID]
}

func (m *Manager) AddMessage(sessionID string, role, content string) error {
    m.mu.Lock()
    defer m.mu.Unlock()
    
    session, ok := m.sessions[sessionID]
    if !ok {
        return fmt.Errorf("session not found: %s", sessionID)
    }
    
    session.Messages = append(session.Messages, Message{
        Role:      role,
        Content:   content,
        Timestamp: time.Now(),
    })
    session.LastActiveAt = time.Now()
    
    return m.saveSession(session)
}

func (m *Manager) saveSession(session *Session) error {
    filePath := filepath.Join(m.baseDir, session.ID+".json")
    data, err := json.MarshalIndent(session, "", "  ")
    if err != nil {
        return fmt.Errorf("failed to marshal session: %w", err)
    }
    
    return os.WriteFile(filePath, data, 0644)
}

func (m *Manager) loadSessions() error {
    entries, err := os.ReadDir(m.baseDir)
    if err != nil {
        return err
    }
    
    for _, entry := range entries {
        if filepath.Ext(entry.Name()) != ".json" {
            continue
        }
        
        filePath := filepath.Join(m.baseDir, entry.Name())
        data, err := os.ReadFile(filePath)
        if err != nil {
            continue // Skip corrupted files
        }
        
        var session Session
        if err := json.Unmarshal(data, &session); err != nil {
            continue // Skip invalid JSON
        }
        
        m.sessions[session.ID] = &session
    }
    
    return nil
}
```

**Key Features:**
- Thread-safe with `sync.RWMutex`
- JSON persistence
- Session recovery on startup
- Automatic directory creation

---

### 5. CLI Interface (`cmd/gemini-cli/main.go`)

**Responsibilities:**
- Parse command-line arguments using Cobra
- Execute commands (upload, ask, session management)
- Provide interactive mode
- Display formatted output
- Handle errors gracefully

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

**Implementation Pattern:**
```go
package main

import (
    "fmt"
    "os"
    
    "github.com/spf13/cobra"
)

var (
    cfg *config.Config
    geminiClient *gemini.Client
    sessionManager *session.Manager
)

var rootCmd = &cobra.Command{
    Use:   "gemini-cli",
    Short: "Gemini Media Analysis CLI",
    Long:  "A CLI tool for uploading media files to Gemini API and conducting in-depth analysis.",
}

var uploadCmd = &cobra.Command{
    Use:   "upload [file]",
    Short: "Upload a media file to Gemini",
    Args:  cobra.ExactArgs(1),
    RunE: func(cmd *cobra.Command, args []string) error {
        filePath := args[0]
        
        // Validate file
        fileInfo, err := filehandler.ValidateFile(filePath)
        if err != nil {
            return fmt.Errorf("validation failed: %w", err)
        }
        
        fmt.Printf("Uploading %s (%s)...\n", fileInfo.Name, formatSize(fileInfo.Size))
        
        // Upload to Gemini
        ctx := cmd.Context()
        file, err := geminiClient.UploadFile(ctx, filePath)
        if err != nil {
            return fmt.Errorf("upload failed: %w", err)
        }
        
        // Add to active session
        sess := sessionManager.GetActiveSession()
        if sess == nil {
            sess, err = sessionManager.CreateSession()
            if err != nil {
                return err
            }
        }
        
        sess.Files = append(sess.Files, file.Name)
        if err := sessionManager.SaveSession(sess); err != nil {
            return err
        }
        
        fmt.Printf("✓ File uploaded successfully: %s\n", file.Name)
        return nil
    },
}

var askCmd = &cobra.Command{
    Use:   "ask [question]",
    Short: "Ask a question about uploaded media",
    Args:  cobra.MinimumNArgs(1),
    RunE: func(cmd *cobra.Command, args []string) error {
        sess := sessionManager.GetActiveSession()
        if sess == nil || len(sess.Files) == 0 {
            return fmt.Errorf("no active session or files uploaded. Use 'upload' first")
        }
        
        question := args[0]
        fmt.Printf("Asking: %s\n\n", question)
        
        // Generate response
        ctx := cmd.Context()
        response, err := geminiClient.GenerateContent(ctx, question, sess.Files)
        if err != nil {
            return fmt.Errorf("generation failed: %w", err)
        }
        
        fmt.Println(response)
        
        // Save to session history
        sessionManager.AddMessage(sess.ID, "user", question)
        sessionManager.AddMessage(sess.ID, "assistant", response)
        
        return nil
    },
}

func main() {
    // Initialize configuration
    var err error
    cfg, err = config.LoadConfig()
    if err != nil {
        fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
        os.Exit(1)
    }
    
    // Initialize Gemini client
    geminiClient, err = gemini.NewClient(cfg.APIKey, cfg.Model)
    if err != nil {
        fmt.Fprintf(os.Stderr, "Error creating Gemini client: %v\n", err)
        os.Exit(1)
    }
    
    // Initialize session manager
    sessionManager, err = session.NewManager(cfg.SessionDir)
    if err != nil {
        fmt.Fprintf(os.Stderr, "Error creating session manager: %v\n", err)
        os.Exit(1)
    }
    
    // Add commands
    rootCmd.AddCommand(uploadCmd)
    rootCmd.AddCommand(askCmd)
    rootCmd.AddCommand(sessionCmd)
    rootCmd.AddCommand(interactiveCmd)
    
    // Execute
    if err := rootCmd.Execute(); err != nil {
        fmt.Fprintf(os.Stderr, "Error: %v\n", err)
        os.Exit(1)
    }
}
```

**Key Features:**
- Cobra CLI framework
- Context propagation
- Error handling
- Formatted output
- Interactive mode support

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

**Go-Specific Characteristics:**
- ✅ Context propagation for cancellation/timeouts
- ✅ Error handling via `(result, error)` tuples
- ✅ Thread-safe operations with mutexes
- ✅ Streaming file I/O for large files
- ✅ No shared mutable state

---

## File Upload Strategy

### Current Implementation (Phase 1)

1. **User provides file path** via CLI: `gemini-cli upload /path/to/video.mp4`
2. **File validation**:
   - Check file exists (`os.Stat()`)
   - Validate file size (max 2GB)
   - Detect MIME type (`mime` package)
   - Verify supported format
3. **File reading**:
   - Open file with `os.Open()`
   - Stream via `io.Reader` for memory efficiency
4. **Upload to Gemini**:
   - Use Gemini SDK's file upload API
   - Return file reference (struct)
5. **Session storage**:
   - Add file reference to active session
   - Persist session to disk
6. **Subsequent queries**:
   - Use file reference in conversation requests
   - Maintain context across questions

### Future Implementation (Phase 2)

**Cloud Storage Integration:**
- Upload files to AWS S3 or Google Drive first
- Store file URLs/references
- Gemini API accesses files from cloud storage
- Reduces local network bandwidth usage

---

## Session Management

### Session Structure

```go
type Session struct {
    ID           string      // UUID
    Files        []string    // File references from Gemini
    Messages     []Message   // Conversation history
    CreatedAt    time.Time
    LastActiveAt time.Time
}
```

### Session Storage

- **Location**: `~/.gemini-media-cli/sessions/`
- **Format**: JSON files (`{session-id}.json`)
- **Active Session**: Tracked in memory, persisted on changes
- **Recovery**: Load all sessions on startup

### Session Operations

- **Create**: Generate UUID, initialize empty session
- **List**: Show all sessions with metadata
- **Switch**: Change active session
- **Clear**: Remove current session's files/messages
- **Delete**: Remove session file from disk

### Thread Safety

- Use `sync.RWMutex` for concurrent access
- Read locks for reading sessions
- Write locks for modifications
- Safe for concurrent CLI operations

---

## Gemini Model Configuration

### Default Model

**Model**: `gemini-3-flash-preview`

This model is selected for:
- ✅ **Free tier compatibility** - Works within Google's free tier limits
- ✅ **Fast response times** - Optimized for quick interactions
- ✅ **Multimodal support** - Handles both images and videos
- ✅ **Low latency validation** - Minimal overhead for API key validation

### Free Tier Limits

| Metric | Limit |
|--------|-------|
| Requests Per Minute (RPM) | 5-15 (varies by region) |
| Tokens Per Minute (TPM) | 250,000 |
| Requests Per Day (RPD) | ~1,000 |

---

## Error Handling

### Error Handling Strategy

The CLI uses typed errors with context wrapping for clear debugging and user feedback. All errors include:
- Original error context (wrapped)
- User-friendly message
- Specific error category for programmatic handling

### API Key Validation Errors

The CLI validates API keys on startup and categorizes failures into specific types:

| Error Type | Trigger | User Action |
|------------|---------|-------------|
| **No Key** | API key not found in environment or GPG file | Run `scripts/setup-gpg-credentials.sh` or set `GEMINI_API_KEY` |
| **Invalid Key** | Key is malformed, revoked, or unauthorized (HTTP 400/401/403) | Regenerate key at [Google AI Studio](https://aistudio.google.com/app/apikey) |
| **Network Error** | Connection timeout, DNS failure, server error (HTTP 5xx) | Check internet connection; retry later |
| **Quota Exceeded** | Rate limit hit (HTTP 429) | Wait for quota reset; check usage at [AI Dev Usage](https://ai.dev/usage) |
| **Unknown** | Unclassified API errors | Check logs for details; report if persistent |

### Error Categories

1. **Configuration Errors**:
   - Missing API key → Clear setup instructions provided
   - Invalid config values → Validation on startup
   - File system errors → Permission and path guidance

2. **File Errors**:
   - File not found → Path validation with helpful message
   - Unsupported file type → List of supported formats
   - File too large → Size limit (2GB) specified
   - Read permissions → Permission check guidance

3. **API Errors**:
   - Network failures → Retry with exponential backoff
   - Authentication errors → Key validation with specific feedback
   - Rate limiting → Quota information and retry timing
   - Invalid requests → Request validation before sending

4. **Session Errors**:
   - Session not found → Available sessions listed
   - JSON serialization errors → Session file repair guidance
   - File system errors → Directory creation and permissions

### Error Recovery

- **Automatic Retries**: For transient network errors with exponential backoff
- **Context Timeouts**: 30-second default timeout prevents hanging operations
- **Graceful Degradation**: Continue with partial functionality when possible
- **Clear Messages**: User-friendly error descriptions with actionable guidance
- **Debug Logging**: Set `GEMINI_LOG_LEVEL=debug` for detailed diagnostics

---

## Design Decisions

This section documents key architectural and implementation decisions made during development.

### Iterative Implementation Approach

**Decision**: Build the CLI through small, focused iterations rather than implementing all features at once.

**Rationale**:
- **Testable increments**: Each iteration produces working, testable code
- **Early feedback**: Issues are discovered before compounding
- **Flexibility**: Plan can be adjusted based on learnings
- **Momentum**: Regular completions maintain development momentum

**Iteration Structure**:
- Iterations 1-6: Foundation (connection, logging, auth, validation)
- Iterations 7-10: Media uploads (images, videos, directories)
- Iterations 11-13: Session management
- Iterations 14-16: CLI polish
- Iterations 17-19: Advanced features

### Logging Before Features

**Decision**: Implement logging infrastructure (Iteration 2) before core functionality.

**Rationale**:
- **Debugging support**: All subsequent code benefits from structured logging
- **Observability from day one**: Issues during development are traceable
- **Consistent patterns**: Logging conventions established early are followed throughout
- **Lower cost**: Adding logging later requires touching every file

### GPG Credential Storage

**Decision**: Use GPG encryption for API key storage rather than plaintext or third-party secrets managers.

**Rationale**:
- **Security**: AES-256 encryption protects keys at rest
- **Developer familiarity**: GPG is standard tooling for developers
- **Portability**: Encrypted files can be synced across machines
- **No dependencies**: Uses system GPG binary, no additional packages

**Trade-offs Accepted**:
- Requires GPG key setup (documented in setup script)
- Passphrase entry needed (gpg-agent caches for session)

### Startup API Key Validation

**Decision**: Validate API key with a real API call before any operations.

**Rationale**:
- **Fail fast**: Users learn about auth issues immediately
- **Clear diagnostics**: Typed errors provide specific guidance
- **No wasted work**: Prevents failed uploads due to bad credentials
- **Free tier compatible**: Uses `gemini-3-flash-preview` to minimize quota impact

**Validation Approach**:
- Minimal "hi" request to generative model
- ~1-2 second latency on fast connections
- Five distinct error types with user-friendly messages

### Typed Validation Errors

**Decision**: Create explicit `ValidationErrorType` enum with typed `ValidationError` struct.

**Rationale**:
- **Compile-time safety**: Missing error handlers are caught at build time
- **Consistent UX**: Each error type maps to a specific, tested message
- **Testability**: Error types can be asserted in unit tests
- **Extensibility**: New types integrate without breaking existing code

**Error Types Implemented**:

| Type | When Returned |
|------|---------------|
| `ErrTypeNoKey` | No API key found in any source |
| `ErrTypeInvalidKey` | Key rejected by API (400/401/403) |
| `ErrTypeNetworkError` | Connection failures, server errors (5xx) |
| `ErrTypeQuotaExceeded` | Rate limited (429) |
| `ErrTypeUnknown` | Unclassified errors |

### Model Selection: gemini-3-flash-preview

**Decision**: Use `gemini-3-flash-preview` as the default model for validation and operations.

**Rationale**:
- **Free tier compatible**: Works within Google's free tier limits
- **Low latency**: Flash models are optimized for speed
- **Multimodal support**: Handles images and videos
- **Active development**: Preview model receives latest improvements

**Alternatives Evaluated**:

| Model | Issue |
|-------|-------|
| `gemini-2.0-flash-lite` | Rate limited to 0 requests on free tier |
| `gemini-pro` | Higher latency, unnecessary for validation |
| List models API | Doesn't verify generation permissions |

### Dual Error Detection Strategy

**Decision**: Classify errors using both HTTP status codes AND error message patterns.

**Rationale**:
- **HTTP codes**: Reliable for Google API errors wrapped in `googleapi.Error`
- **Pattern matching**: Catches errors before HTTP layer (DNS, connection issues)
- **Maximizes coverage**: Combined approach handles more edge cases
- **Graceful degradation**: Falls back to "unknown" type if unclassified

**Pattern Keywords**:
- Invalid key: "api key not valid", "api_key_invalid", "permission denied"
- Quota: "quota", "resource exhausted", "rate limit"
- Network: "connection", "timeout", "dial", "no such host"

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

## Development Roadmap

### Phase 1: Foundation (Iterations 1-6)

- [x] **Iteration 1**: Basic connection validation - go.mod and minimal main.go
- [x] **Iteration 2**: Logging infrastructure with zerolog
- [x] **Iteration 3**: GPG encryption setup script
- [x] **Iteration 4**: GPG integration in Go (internal/auth package)
- [x] **Iteration 5**: API key validation with typed error handling
- [ ] **Iteration 6**: Hardcoded text question/answer

### Phase 2: Media Uploads (Iterations 7-10)

- [ ] **Iteration 7**: Single image upload with hardcoded path
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
- Gemini API key from Google AI Studio
- GPG (for secure credential storage)
- Git (for version control)

### New User Setup

Follow these steps to set up the CLI on a new machine:

#### Step 1: Obtain a Gemini API Key

1. Visit [Google AI Studio](https://aistudio.google.com/)
2. Sign in with your Google account
3. Navigate to "Get API key" → [API Keys](https://aistudio.google.com/app/apikey)
4. Click "Create API key" and copy the generated key

#### Step 2: Set Up GPG (if not already configured)

```bash
# Check if you have a GPG key
gpg --list-keys

# If no key exists, generate one
gpg --full-generate-key
```

#### Step 3: Store Your API Key

**Option A: Using the setup script (recommended)**
```bash
cd gemini-media-cli
./scripts/setup-gpg-credentials.sh
```

**Option B: Using environment variable**
```bash
export GEMINI_API_KEY="your-api-key-here"
```

Add to `~/.zshrc` or `~/.bashrc` for persistence.

#### Step 4: Build and Verify

```bash
cd gemini-media-cli
go build -o gemini-cli ./cmd/gemini-cli
./gemini-cli
```

Expected output on success:
```
INF connection successful - Gemini client initialized
INF API key validated successfully
INF API key validation complete - ready for operations
```

### Verification and Debugging

Enable debug logging to see detailed information:
```bash
GEMINI_LOG_LEVEL=debug ./gemini-cli
```

This shows:
- Which credential source is being used (environment variable or GPG file)
- API key validation progress
- Any errors with detailed context

### Common Setup Issues

| Issue | Cause | Solution |
|-------|-------|----------|
| "API key not found" | No key in env or GPG file | Run setup script or set `GEMINI_API_KEY` |
| "GPG decryption failed" | GPG agent not running or key issue | Run `gpg --decrypt ~/.gemini-media-cli/credentials.gpg` to test |
| "Invalid API key" | Key is malformed or revoked | Regenerate at [Google AI Studio](https://aistudio.google.com/app/apikey) |
| "API quota exceeded" | Free tier limits reached | Wait for quota reset or check [usage](https://ai.dev/usage) |

### Developer Setup

For developers contributing to the project:

1. **Clone repository**:
   ```bash
   git clone <repository-url>
   cd gemini-media-social-network/gemini-media-cli
   ```

2. **Install dependencies**:
   ```bash
   go mod download
   ```

3. **Build**:
   ```bash
   go build -o gemini-cli ./cmd/gemini-cli
   ```

4. **Run tests**:
   ```bash
   go test ./...
   ```

5. **Run with debug logging**:
   ```bash
   GEMINI_LOG_LEVEL=debug ./gemini-cli
   ```

---

## Testing Strategy

### Unit Tests

- Test each package independently
- Use Go's `testing` package
- Co-locate tests with source (`*_test.go`)
- Mock external dependencies

### Integration Tests

- Test CLI commands end-to-end
- Test file upload flow
- Test session persistence
- Test error scenarios

### Test Coverage Goals

- Minimum 80% code coverage
- Critical paths: 100% coverage
- Error handling: All error paths tested

---

## Documentation

### Code Documentation

- Package-level comments
- Exported function documentation
- Example usage in comments
- README.md for users

### API Documentation

- Command reference
- Configuration options
- Error codes and meanings
- Examples and tutorials

---

## License

[To be determined]

---

## Contributing

[To be added]

---

**Last Updated**: 2025-12-31  
**Version**: 1.1.0  
**Status**: Implementation Phase (Iteration 5 Complete)

