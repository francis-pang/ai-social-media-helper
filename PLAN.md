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
8. [Error Handling](#error-handling)
9. [Future Extensibility](#future-extensibility)
10. [Development Roadmap](#development-roadmap)

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
gemini-media-cli/
├── cmd/
│   └── gemini-cli/
│       └── main.go                    # Entry point (package main)
│
├── internal/                          # Private packages (not importable externally)
│   ├── gemini/
│   │   ├── client.go                 # Gemini API client wrapper
│   │   └── client_test.go            # Tests co-located
│   │
│   ├── filehandler/
│   │   ├── handler.go                # File validation & upload preparation
│   │   └── handler_test.go           # Tests co-located
│   │
│   ├── session/
│   │   ├── manager.go                # Session management
│   │   ├── session.go                # Session data structures
│   │   └── manager_test.go          # Tests co-located
│   │
│   └── storage/                       # Future: storage abstraction
│       ├── provider.go               # StorageProvider interface
│       ├── direct.go                 # DirectUploadProvider (current)
│       └── s3.go                      # S3StorageProvider (future)
│
├── config/
│   └── config.go                     # Configuration loading & validation
│
├── pkg/                               # Public packages (if needed)
│   └── models/
│       ├── session.go                # Session data models
│       └── file.go                   # File metadata models
│
├── go.mod                             # Go module definition
├── go.sum                             # Dependency checksums
├── .env.example                       # Example environment variables
├── .gitignore                         # Git ignore rules
├── Makefile                           # Build commands
├── README.md                          # User documentation
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
- Load API key from environment variable `GEMINI_API_KEY`
- Support optional config file using Viper
- Validate configuration values
- Provide default values where appropriate

**Implementation Pattern:**
```go
package config

import (
    "fmt"
    "os"
)

type Config struct {
    APIKey     string
    Model      string
    BaseURL    string
    Timeout    time.Duration
    SessionDir string
}

func LoadConfig() (*Config, error) {
    apiKey := os.Getenv("GEMINI_API_KEY")
    if apiKey == "" {
        return nil, fmt.Errorf("GEMINI_API_KEY environment variable not set")
    }
    
    sessionDir := os.Getenv("GEMINI_SESSION_DIR")
    if sessionDir == "" {
        homeDir, err := os.UserHomeDir()
        if err != nil {
            return nil, fmt.Errorf("failed to get home directory: %w", err)
        }
        sessionDir = filepath.Join(homeDir, ".gemini-media-cli", "sessions")
    }
    
    return &Config{
        APIKey:     apiKey,
        Model:      getEnvOrDefault("GEMINI_MODEL", "gemini-2.0-flash-exp"),
        BaseURL:    getEnvOrDefault("GEMINI_BASE_URL", ""),
        Timeout:    30 * time.Second,
        SessionDir: sessionDir,
    }, nil
}

func getEnvOrDefault(key, defaultValue string) string {
    if value := os.Getenv(key); value != "" {
        return value
    }
    return defaultValue
}
```

**Key Features:**
- Environment variable priority
- Sensible defaults
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
| **Language** | Go 1.21+ | Core language |
| **SDK** | `google.golang.org/genai` | Official Gemini API SDK |
| **CLI Framework** | `github.com/spf13/cobra` | Command-line interface |
| **Configuration** | `os` package + `github.com/spf13/viper` (optional) | Config management |
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

## Error Handling

### Error Handling Strategy

**Go Error Pattern:**
```go
result, err := someOperation()
if err != nil {
    return fmt.Errorf("context: %w", err)
}
```

### Error Categories

1. **Configuration Errors**:
   - Missing API key
   - Invalid config values
   - File system errors

2. **File Errors**:
   - File not found
   - Unsupported file type
   - File too large
   - Read permissions

3. **API Errors**:
   - Network failures
   - Authentication errors
   - Rate limiting
   - Invalid requests

4. **Session Errors**:
   - Session not found
   - JSON serialization errors
   - File system errors

### Error Recovery

- **Retries**: For transient network errors
- **Context Timeouts**: Prevent hanging operations
- **Graceful Degradation**: Continue with partial functionality
- **Clear Messages**: User-friendly error descriptions

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

### Phase 1: Core Functionality (Current)

- [x] Project structure setup
- [ ] Configuration management
- [ ] Gemini client implementation
- [ ] File handler with validation
- [ ] Session manager with persistence
- [ ] Basic CLI commands (upload, ask)
- [ ] Session management commands
- [ ] Unit tests

### Phase 2: Enhanced Features

- [ ] Interactive mode
- [ ] Progress indicators for uploads
- [ ] Better error messages
- [ ] Configuration file support
- [ ] Session export/import
- [ ] Batch file uploads

### Phase 3: Cloud Storage Integration

- [ ] Storage provider interface
- [ ] S3 integration
- [ ] Google Drive integration
- [ ] Storage provider selection
- [ ] Migration tools

### Phase 4: Advanced Features

- [ ] Streaming responses
- [ ] Multiple model support
- [ ] Response formatting options
- [ ] History search
- [ ] Session templates

---

## Getting Started

### Prerequisites

- Go 1.21 or later
- Gemini API key from Google AI Studio
- Git (for version control)

### Setup

1. **Clone repository**:
   ```bash
   cd /Users/fpang/code/miniature-disco/gemini-media-social-network
   ```

2. **Initialize Go module**:
   ```bash
   go mod init github.com/miniature-disco/gemini-media-cli
   ```

3. **Install dependencies**:
   ```bash
   go get google.golang.org/genai
   go get github.com/spf13/cobra
   go get github.com/google/uuid
   ```

4. **Set environment variable**:
   ```bash
   export GEMINI_API_KEY="your-api-key-here"
   ```

5. **Build**:
   ```bash
   go build -o gemini-cli ./cmd/gemini-cli
   ```

6. **Run**:
   ```bash
   ./gemini-cli upload image.jpg
   ./gemini-cli ask "What's in this image?"
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

**Last Updated**: 2025-12-30  
**Version**: 1.0.0  
**Status**: Planning Phase

