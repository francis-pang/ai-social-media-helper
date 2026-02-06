# Operations Design Document

## Overview

This document covers logging, observability, error handling, and retry strategies for the Gemini Media Analysis CLI. These operational concerns ensure the application is debuggable, reliable, and provides clear feedback to users.

---

## Logging

### Design Decision: zerolog

We chose **zerolog** over alternatives (zap, slog) for the following reasons:

| Criterion | zerolog | zap | slog (stdlib) |
|-----------|---------|-----|---------------|
| Performance | Zero allocation | Near-zero | Allocates |
| API simplicity | Fluent chaining | Dual APIs | Verbose |
| CLI console output | Excellent `ConsoleWriter` | Needs setup | Basic |
| Context support | Built-in `log.Ctx()` | Manual | Manual |

**Key factors for CLI tooling:**
- Zero allocation minimizes GC pressure during media processing
- Fluent API (`log.Info().Str("key", val).Msg("...")`) reduces boilerplate
- `ConsoleWriter` provides colored, human-readable output ideal for terminal use

### Log Levels

| Level | Purpose | Examples |
|-------|---------|----------|
| `error` | Failures that stop the operation | API errors, file not found, auth failures |
| `warn` | Potential issues, degraded operation | Retry attempts, slow responses, deprecated features |
| `info` | Normal operation milestones | Upload started/completed, session created |
| `debug` | Detailed diagnostic information | Request/response details, timing, internal state |

### Log Format

#### Text Format (Default)

Human-readable format for terminal use:

```
2025-12-30T10:15:32.123Z INFO  Uploading file: photo.jpg (2.3 MB)
2025-12-30T10:15:35.456Z INFO  Upload complete: photo.jpg → files/abc123
2025-12-30T10:15:35.789Z DEBUG API request completed in 3.21s
2025-12-30T10:15:36.012Z INFO  Generating response for: "What's in this image?"
2025-12-30T10:15:38.234Z INFO  Response received (245 tokens)
```

#### JSON Format

Structured format for log aggregation:

```json
{"time":"2025-12-30T10:15:32.123Z","level":"info","msg":"Uploading file","file":"photo.jpg","size":2411724}
{"time":"2025-12-30T10:15:35.456Z","level":"info","msg":"Upload complete","file":"photo.jpg","ref":"files/abc123","duration_ms":3333}
{"time":"2025-12-30T10:15:35.789Z","level":"debug","msg":"API request completed","duration_ms":3210,"status":200}
```

### Logging Conventions

| Convention | Example |
|------------|---------|
| Use `Str()` for string fields | `log.Info().Str("file", name).Msg("...")` |
| Use `Err()` for errors | `log.Error().Err(err).Msg("failed")` |
| Keep messages lowercase | `Msg("upload complete")` |
| Use snake_case for field names | `Str("session_id", id)` |
| Add context via `With()` | `log.With().Str("session", id).Logger()` |
| Use `Fatal()` for unrecoverable errors | `log.Fatal().Msg("cannot continue")` |

### Implementation

Logging is initialized via `GEMINI_LOG_LEVEL` environment variable:

```go
package logging

import (
    "os"
    "github.com/rs/zerolog"
    "github.com/rs/zerolog/log"
)

func Init() {
    level := os.Getenv("GEMINI_LOG_LEVEL")
    switch level {
    case "debug":
        zerolog.SetGlobalLevel(zerolog.DebugLevel)
    case "warn":
        zerolog.SetGlobalLevel(zerolog.WarnLevel)
    case "error":
        zerolog.SetGlobalLevel(zerolog.ErrorLevel)
    default:
        zerolog.SetGlobalLevel(zerolog.InfoLevel)
    }
    
    log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
}
```

### Secret Redaction

Automatically redact sensitive information in logs:

```go
var secretPatterns = []struct {
    pattern *regexp.Regexp
    replace string
}{
    // Gemini API key pattern
    {regexp.MustCompile(`AIzaSy[a-zA-Z0-9_-]{33}`), "AIzaSy****...****"},
    // Generic API key patterns
    {regexp.MustCompile(`api[_-]?key[=:]\s*["']?([a-zA-Z0-9_-]{20,})["']?`), "api_key=****"},
    // Bearer tokens
    {regexp.MustCompile(`Bearer\s+[a-zA-Z0-9._-]+`), "Bearer ****"},
}

func RedactSecrets(s string) string {
    result := s
    for _, p := range secretPatterns {
        result = p.pattern.ReplaceAllString(result, p.replace)
    }
    return result
}
```

### Log Contexts

Add context to log entries for traceability:

```go
// Create logger with session context
sessionLog := log.With().
    Str("session_id", session.ID).
    Str("command", "upload").
    Logger()

// All subsequent logs include this context
sessionLog.Info().Str("file", filename).Msg("starting upload")
sessionLog.Debug().Int64("size", fileSize).Msg("file validated")
```

---

## Observability

### Metrics

Track key operational metrics (in-memory for CLI, optionally exportable):

| Metric | Type | Description |
|--------|------|-------------|
| `commands_total` | Counter | Total commands executed by type |
| `uploads_total` | Counter | Total file uploads attempted |
| `uploads_success` | Counter | Successful uploads |
| `uploads_failed` | Counter | Failed uploads |
| `upload_bytes_total` | Counter | Total bytes uploaded |
| `upload_duration_seconds` | Histogram | Upload duration distribution |
| `api_requests_total` | Counter | Total API requests |
| `api_errors_total` | Counter | API errors by type |
| `api_latency_seconds` | Histogram | API request latency |
| `retries_total` | Counter | Total retry attempts |
| `sessions_active` | Gauge | Currently active sessions |

### Implementation

```go
package metrics

import (
    "sync"
    "time"
)

type Metrics struct {
    mu sync.RWMutex
    
    CommandsTotal   map[string]int64
    UploadsTotal    int64
    UploadsSuccess  int64
    UploadsFailed   int64
    UploadBytes     int64
    APIRequestsTotal int64
    APIErrorsTotal  map[string]int64
    RetriesTotal    int64
    
    uploadDurations []time.Duration
    apiLatencies    []time.Duration
}

func NewMetrics() *Metrics {
    return &Metrics{
        CommandsTotal:  make(map[string]int64),
        APIErrorsTotal: make(map[string]int64),
    }
}

func (m *Metrics) RecordCommand(cmd string) {
    m.mu.Lock()
    defer m.mu.Unlock()
    m.CommandsTotal[cmd]++
}

func (m *Metrics) RecordUpload(success bool, bytes int64, duration time.Duration) {
    m.mu.Lock()
    defer m.mu.Unlock()
    
    m.UploadsTotal++
    if success {
        m.UploadsSuccess++
        m.UploadBytes += bytes
    } else {
        m.UploadsFailed++
    }
    m.uploadDurations = append(m.uploadDurations, duration)
}

func (m *Metrics) RecordAPIRequest(latency time.Duration, err error) {
    m.mu.Lock()
    defer m.mu.Unlock()
    
    m.APIRequestsTotal++
    m.apiLatencies = append(m.apiLatencies, latency)
    
    if err != nil {
        errType := categorizeError(err)
        m.APIErrorsTotal[errType]++
    }
}

func (m *Metrics) Summary() string {
    m.mu.RLock()
    defer m.mu.RUnlock()
    
    // Return formatted summary
    return fmt.Sprintf(
        "Commands: %d | Uploads: %d/%d | API Errors: %d",
        sumMapValues(m.CommandsTotal),
        m.UploadsSuccess,
        m.UploadsTotal,
        sumMapValues(m.APIErrorsTotal),
    )
}
```

### Debug Command

```bash
# Show session metrics
gemini-cli debug metrics

# Output:
Session Metrics:
  Commands executed:  12
  Uploads attempted:  5
  Uploads succeeded:  4
  Uploads failed:     1
  Bytes uploaded:     125.3 MB
  API requests:       18
  API errors:         2
  Average latency:    1.23s
```

---

## Error Handling

### Error Categories

| Category | HTTP Codes | Retriable | User Action |
|----------|------------|-----------|-------------|
| `auth_error` | 401, 403 | No | Check API key |
| `not_found` | 404 | No | Verify resource exists |
| `validation_error` | 400 | No | Fix input |
| `rate_limit` | 429 | Yes | Wait and retry |
| `server_error` | 500-599 | Yes | Automatic retry |
| `network_error` | - | Yes | Check connection |
| `timeout_error` | - | Yes | Increase timeout or retry |
| `file_error` | - | No | Check file path/permissions |

### Error Types

```go
package errors

import (
    "errors"
    "fmt"
)

// Sentinel errors for type checking
var (
    ErrAuthentication = errors.New("authentication failed")
    ErrNotFound       = errors.New("resource not found")
    ErrValidation     = errors.New("validation failed")
    ErrRateLimit      = errors.New("rate limit exceeded")
    ErrServer         = errors.New("server error")
    ErrNetwork        = errors.New("network error")
    ErrTimeout        = errors.New("request timeout")
    ErrFile           = errors.New("file error")
)

// AppError wraps errors with additional context
type AppError struct {
    Category  string
    Message   string
    Cause     error
    Retriable bool
    Details   map[string]any
}

func (e *AppError) Error() string {
    if e.Cause != nil {
        return fmt.Sprintf("%s: %s: %v", e.Category, e.Message, e.Cause)
    }
    return fmt.Sprintf("%s: %s", e.Category, e.Message)
}

func (e *AppError) Unwrap() error {
    return e.Cause
}

func (e *AppError) IsRetriable() bool {
    return e.Retriable
}

// Error constructors
func NewAuthError(msg string, cause error) *AppError {
    return &AppError{
        Category:  "auth",
        Message:   msg,
        Cause:     cause,
        Retriable: false,
    }
}

func NewRateLimitError(retryAfter int) *AppError {
    return &AppError{
        Category:  "rate_limit",
        Message:   fmt.Sprintf("rate limit exceeded, retry after %ds", retryAfter),
        Retriable: true,
        Details:   map[string]any{"retry_after": retryAfter},
    }
}

func NewNetworkError(cause error) *AppError {
    return &AppError{
        Category:  "network",
        Message:   "network request failed",
        Cause:     cause,
        Retriable: true,
    }
}
```

### Error Classification

```go
func ClassifyError(err error) *AppError {
    if err == nil {
        return nil
    }
    
    // Already classified
    var appErr *AppError
    if errors.As(err, &appErr) {
        return appErr
    }
    
    errStr := err.Error()
    
    // Check for specific patterns
    switch {
    case strings.Contains(errStr, "401") || strings.Contains(errStr, "403"):
        return NewAuthError("API authentication failed", err)
        
    case strings.Contains(errStr, "429"):
        return NewRateLimitError(60) // Default retry after
        
    case strings.Contains(errStr, "500") || strings.Contains(errStr, "502") ||
         strings.Contains(errStr, "503") || strings.Contains(errStr, "504"):
        return &AppError{
            Category:  "server",
            Message:   "server error",
            Cause:     err,
            Retriable: true,
        }
        
    case strings.Contains(errStr, "timeout") || errors.Is(err, context.DeadlineExceeded):
        return &AppError{
            Category:  "timeout",
            Message:   "request timed out",
            Cause:     err,
            Retriable: true,
        }
        
    case strings.Contains(errStr, "connection refused") ||
         strings.Contains(errStr, "no such host") ||
         strings.Contains(errStr, "network is unreachable"):
        return NewNetworkError(err)
        
    default:
        return &AppError{
            Category:  "unknown",
            Message:   err.Error(),
            Cause:     err,
            Retriable: false,
        }
    }
}
```

### User-Friendly Error Messages

```go
func FormatErrorForUser(err error) string {
    appErr := ClassifyError(err)
    if appErr == nil {
        return ""
    }
    
    var suggestion string
    switch appErr.Category {
    case "auth":
        suggestion = "Check your API key with 'gemini-cli auth verify'"
    case "rate_limit":
        suggestion = "Wait a moment and try again"
    case "network":
        suggestion = "Check your internet connection"
    case "timeout":
        suggestion = "Try increasing --timeout or check your connection"
    case "file":
        suggestion = "Verify the file path and permissions"
    case "server":
        suggestion = "This is a temporary issue, please try again"
    }
    
    msg := fmt.Sprintf("Error: %s", appErr.Message)
    if suggestion != "" {
        msg += fmt.Sprintf("\n\nSuggestion: %s", suggestion)
    }
    
    return msg
}
```

---

## Retry Strategy

### Configuration

```go
type RetryConfig struct {
    MaxAttempts  int           // Maximum number of attempts (including first)
    InitialDelay time.Duration // Initial delay between retries
    MaxDelay     time.Duration // Maximum delay between retries
    Multiplier   float64       // Exponential backoff multiplier
    Jitter       float64       // Random jitter factor (0-1)
}

var DefaultRetryConfig = RetryConfig{
    MaxAttempts:  3,
    InitialDelay: 1 * time.Second,
    MaxDelay:     30 * time.Second,
    Multiplier:   2.0,
    Jitter:       0.1,
}
```

### Retry Logic

```go
package retry

import (
    "context"
    "math"
    "math/rand"
    "time"
)

type Retryer struct {
    config RetryConfig
    logger *logging.Logger
}

func NewRetryer(cfg RetryConfig, logger *logging.Logger) *Retryer {
    return &Retryer{config: cfg, logger: logger}
}

func (r *Retryer) Do(ctx context.Context, operation func() error) error {
    var lastErr error
    
    for attempt := 1; attempt <= r.config.MaxAttempts; attempt++ {
        // Execute the operation
        err := operation()
        if err == nil {
            return nil
        }
        
        lastErr = err
        
        // Check if retriable
        appErr := ClassifyError(err)
        if appErr != nil && !appErr.IsRetriable() {
            r.logger.Debug("Non-retriable error, not retrying",
                slog.String("error", err.Error()),
                slog.Int("attempt", attempt))
            return err
        }
        
        // Check if we have attempts left
        if attempt >= r.config.MaxAttempts {
            r.logger.Warn("Max retry attempts reached",
                slog.Int("attempts", attempt),
                slog.String("error", err.Error()))
            break
        }
        
        // Calculate delay with exponential backoff
        delay := r.calculateDelay(attempt)
        
        r.logger.Warn("Operation failed, retrying",
            slog.Int("attempt", attempt),
            slog.Int("max_attempts", r.config.MaxAttempts),
            slog.Duration("delay", delay),
            slog.String("error", err.Error()))
        
        // Wait before retry
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-time.After(delay):
            // Continue to next attempt
        }
    }
    
    return fmt.Errorf("operation failed after %d attempts: %w", 
        r.config.MaxAttempts, lastErr)
}

func (r *Retryer) calculateDelay(attempt int) time.Duration {
    // Exponential backoff: delay = initial * (multiplier ^ (attempt - 1))
    delay := float64(r.config.InitialDelay) * 
        math.Pow(r.config.Multiplier, float64(attempt-1))
    
    // Apply jitter
    if r.config.Jitter > 0 {
        jitter := delay * r.config.Jitter * (2*rand.Float64() - 1)
        delay += jitter
    }
    
    // Cap at max delay
    if delay > float64(r.config.MaxDelay) {
        delay = float64(r.config.MaxDelay)
    }
    
    return time.Duration(delay)
}
```

### Retry Behavior by Error Type

| Error Type | Retry? | Special Handling |
|------------|--------|------------------|
| `auth_error` | No | Fail immediately |
| `validation_error` | No | Fail immediately |
| `not_found` | No | Fail immediately |
| `rate_limit` | Yes | Use `Retry-After` header if available |
| `server_error` (5xx) | Yes | Standard exponential backoff |
| `network_error` | Yes | Standard exponential backoff |
| `timeout_error` | Yes | May increase timeout on retry |

### Rate Limit Handling

```go
func (r *Retryer) DoWithRateLimitAwareness(ctx context.Context, operation func() error) error {
    return r.Do(ctx, func() error {
        err := operation()
        if err == nil {
            return nil
        }
        
        // Check for rate limit with specific delay
        appErr := ClassifyError(err)
        if appErr != nil && appErr.Category == "rate_limit" {
            if retryAfter, ok := appErr.Details["retry_after"].(int); ok {
                r.logger.Info("Rate limited, waiting",
                    slog.Int("retry_after_seconds", retryAfter))
                
                select {
                case <-ctx.Done():
                    return ctx.Err()
                case <-time.After(time.Duration(retryAfter) * time.Second):
                    // Return original error to trigger retry
                    return err
                }
            }
        }
        
        return err
    })
}
```

---

## Timeout Management

### Timeout Hierarchy

```
Total Command Timeout (e.g., 5 minutes)
├── File Upload Timeout (per file, e.g., 2 minutes)
│   ├── Connection Timeout (10 seconds)
│   └── Transfer Timeout (based on file size)
├── API Request Timeout (per request, e.g., 2 minutes)
│   ├── Connection Timeout (10 seconds)
│   └── Response Timeout (remaining time)
└── Session Save Timeout (5 seconds)
```

### Implementation

```go
func (c *Client) UploadWithTimeout(ctx context.Context, filePath string, fileSize int64) (*File, error) {
    // Calculate dynamic timeout based on file size
    // Assume minimum 1MB/s upload speed
    minDuration := time.Duration(fileSize/(1024*1024)) * time.Second
    timeout := max(2*time.Minute, minDuration*2) // At least 2 min, or 2x expected
    
    ctx, cancel := context.WithTimeout(ctx, timeout)
    defer cancel()
    
    c.logger.Debug("Starting upload with timeout",
        slog.Duration("timeout", timeout),
        slog.Int64("file_size", fileSize))
    
    return c.Upload(ctx, filePath)
}
```

---

## Diagnostic Output

### Verbose Mode

When `--verbose` is enabled:

```
$ gemini-cli upload photo.jpg --verbose

[10:15:32.123] DEBUG Config loaded from ~/.gemini-media-cli/config.yaml
[10:15:32.125] DEBUG API key retrieved from keychain
[10:15:32.126] INFO  Validating file: photo.jpg
[10:15:32.128] DEBUG File stats: size=2411724, mime=image/jpeg
[10:15:32.129] INFO  Starting upload: photo.jpg (2.3 MB)
[10:15:32.130] DEBUG Creating multipart upload request
[10:15:32.131] DEBUG Request headers: Content-Type=multipart/form-data
[10:15:35.342] DEBUG Upload response: status=200, file_id=files/abc123
[10:15:35.343] INFO  ✓ Upload complete: photo.jpg
[10:15:35.344] DEBUG Adding file to session: session_id=xyz789
[10:15:35.346] DEBUG Session saved to disk

Upload successful!
File reference: files/abc123
Session: xyz789
```

### Debug Dump

```bash
# Export diagnostic information
gemini-cli debug dump > debug-info.txt
```

Contents:
```
Gemini Media CLI Debug Information
Generated: 2025-12-30T10:20:00Z
Version: 1.0.0

=== Configuration ===
API Model: gemini-2.0-flash-exp
Timeout: 2m0s
Session Dir: ~/.gemini-media-cli/sessions
Log Level: info

=== Authentication ===
Key Source: keychain
Key Valid: true (verified)

=== Active Session ===
ID: xyz789
Files: 3
Messages: 12
Created: 2025-12-30T09:00:00Z

=== Recent Errors ===
[10:15:30] network: connection timeout (retried, succeeded)
[10:12:15] rate_limit: 429 Too Many Requests (waited 60s)

=== Metrics ===
Commands: 15
Uploads: 5/5 successful
API Requests: 22
Avg Latency: 1.45s
```

---

## Design Decisions

This section documents key design decisions made during implementation.

### API Key Validation Strategy

**Decision**: Validate API keys on startup with a lightweight API call before proceeding with any operations.

**Rationale**:
- **Fail fast**: Users receive immediate feedback if credentials are misconfigured
- **Clear error messages**: Typed errors distinguish between no key, invalid key, network issues, and quota problems
- **Reduced debugging time**: Users don't need to wait until a media upload fails to discover auth issues

**Implementation**:
- Makes a minimal request ("hi") to `gemini-3-flash-preview` model
- Classifies errors into 5 distinct types for targeted user guidance
- Logs validation progress at debug level for troubleshooting

### Typed Error Classification

**Decision**: Use typed `ValidationError` with explicit `ValidationErrorType` enum rather than string-based error matching.

**Rationale**:
- **Type safety**: Compiler catches missing error type handling
- **Consistent user messaging**: Each error type maps to a specific user-friendly message
- **Extensible**: New error types can be added without changing handling code
- **Testable**: Error types can be asserted in unit tests

**Error Type Hierarchy**:

| Type | Trigger | Retriable |
|------|---------|-----------|
| `ErrTypeNoKey` | No API key in env or GPG file | No |
| `ErrTypeInvalidKey` | HTTP 400/401/403, malformed key | No |
| `ErrTypeNetworkError` | HTTP 5xx, connection failures | Yes |
| `ErrTypeQuotaExceeded` | HTTP 429, rate limits | Yes (with delay) |
| `ErrTypeUnknown` | Unclassified errors | No |

### Google API Error Detection

**Decision**: Use both HTTP status code classification and error message pattern matching.

**Rationale**:
- **HTTP codes**: Reliable for Google API errors wrapped in `googleapi.Error`
- **Pattern matching**: Catches errors before they reach HTTP layer (connection failures, DNS issues)
- **Dual approach**: Maximizes error classification accuracy

**Pattern Keywords**:

| Error Type | Keywords Detected |
|------------|-------------------|
| Invalid Key | "api key not valid", "api_key_invalid", "permission denied" |
| Quota | "quota", "resource exhausted", "rate limit" |
| Network | "connection", "timeout", "dial", "no such host", "unreachable" |

### Model Selection

**Decision**: Use `gemini-3-flash-preview` for API key validation and text generation.

**Rationale**:
- **Free tier compatible**: Explicitly free of charge per [Gemini API pricing](https://ai.google.dev/gemini-api/docs/pricing)
- **Minimal resource usage**: Flash models are optimized for speed, not deep reasoning
- **Low latency**: Validation completes in ~1-2 seconds
- **Consistent model**: Same model used for validation and chat operations
- **Multimodal**: Supports text, image, video, and audio inputs

**Alternatives Considered**:
- `gemini-2.0-flash`: Rate limited to 0 requests on free tier (rejected)
- `gemini-2.0-flash-lite`: Rate limited on free tier (rejected)
- `gemini-2.5-flash`: Works but `gemini-3-flash-preview` is the latest free-tier model
- `gemini-pro`: Higher latency, overkill for validation (rejected)
- List models API: Doesn't verify key has generation permissions (rejected)

---

## Summary

| Concern | Approach |
|---------|----------|
| **Logging** | Structured logging with zerolog, configurable levels, secret redaction |
| **Metrics** | In-memory counters, exportable via debug command |
| **Errors** | Categorized errors, user-friendly messages, retriable classification |
| **Retries** | Exponential backoff with jitter, configurable limits |
| **Timeouts** | Hierarchical, dynamic based on operation |
| **Diagnostics** | Verbose mode, debug dump command |
| **Validation** | Startup API key validation with typed errors |

---

**Last Updated**: 2025-12-31  
**Version**: 1.1.0
