# Operations Design Document

## Overview

This document covers logging, observability, error handling, and retry strategies for the Gemini Media Analysis CLI. These operational concerns ensure the application is debuggable, reliable, and provides clear feedback to users.

---

## Logging

> See also [DDR-002](./design-decisions/DDR-002-logging-before-features.md) (original zerolog adoption) and [DDR-051](./design-decisions/DDR-051-comprehensive-logging-overhaul.md) (comprehensive logging overhaul).

### Design Decision: zerolog

We chose **zerolog** over alternatives (zap, slog) for the following reasons:

| Criterion | zerolog | zap | slog (stdlib) |
|-----------|---------|-----|---------------|
| Performance | Zero allocation | Near-zero | Allocates |
| API simplicity | Fluent chaining | Dual APIs | Verbose |
| CLI console output | Excellent `ConsoleWriter` | Needs setup | Basic |
| Context support | Built-in `log.Ctx()` | Manual | Manual |

**Key factors:**
- Zero allocation minimizes GC pressure during media processing
- Fluent API (`log.Info().Str("key", val).Msg("...")`) reduces boilerplate
- `ConsoleWriter` provides colored, human-readable output ideal for terminal use
- JSON output mode for CloudWatch Logs Insights queries in Lambda

### Environment Policy

Since there is no staging or developmental environment, production is treated as the developmental environment:

- **Default log level: `debug`** — gives full diagnostic visibility without the firehose of trace
- **Trace** is wired up but must be explicitly enabled via `GEMINI_LOG_LEVEL=trace` for deep debugging sessions
- Aggressive CloudWatch log retention lifecycle policy is already in place, so over-logging is acceptable

### Log Levels

| Level | Question it answers | When to use | Examples |
|-------|-------------------|-------------|----------|
| `trace` | "What exactly is happening in this code path?" | Only during deep debugging sessions; not enabled by default | DynamoDB PK/SK details, marshal/unmarshal, per-frame histograms, prompt building, form parameter names |
| `debug` | "Why did this request/operation behave this way?" | Key decisions, downstream calls, intermediate states | API calls with timing, S3 ops, handler entry, config values, cache hits/misses, DynamoDB results |
| `info` | "What is the system doing, at a business level?" | Business events, lifecycle milestones, configuration | Cold starts, init summaries, job start/complete, phase transitions, "post published to Instagram" |
| `warn` | "Something is off; it might become a problem." | Recoverable issues, fallback behavior, skipped items | Skipped files in batch, Imagen fallback, missing optional config, transient retry |
| `error` | "A real failure occurred; behavior was affected." | Failed operations, permanent failures, data integrity | Job failed, API call failed permanently, S3 upload failed, invariant violated |
| `fatal` | "The process cannot safely continue." | Startup failures with no recovery path | Required env var missing, AWS config load failed, required SSM parameter missing |

### Per-Log-Line Mental Checklist

When adding a new log statement, ask:

1. **If this appears every second in prod, is that okay?** — If no, lower the level or sample.
2. **If this condition causes user impact, would on-call want to see it?** — If yes, at least Error, maybe Warn.
3. **If I had only logs, not a debugger, would this help me reconstruct what happened?** — If no, add IDs and concrete context.
4. **Is this level consistent with the rest of the codebase?** — If no, adjust to match existing conventions.

### Log Output Format

#### Lambda (JSON)

When `AWS_LAMBDA_FUNCTION_NAME` is set, output is JSON for CloudWatch Logs Insights:

```json
{"level":"info","requestId":"a1b2c3d4","functionName":"media-lambda","sessionId":"550e8400-...","jobId":"triage-abc123","msg":"Triage job dispatched to Worker Lambda","time":"2026-02-09T10:15:32Z"}
{"level":"debug","requestId":"a1b2c3d4","method":"POST","path":"/api/triage/start","status":200,"duration":45,"msg":"POST /api/triage/start 200","time":"2026-02-09T10:15:32Z"}
```

**CloudWatch Logs Insights example queries:**

```
# Find all logs for a specific job
fields @timestamp, level, msg
| filter jobId = "triage-abc123"
| sort @timestamp asc

# Find all errors in the last hour
fields @timestamp, functionName, msg, error
| filter level = "error"
| sort @timestamp desc
| limit 50

# Cold start frequency
fields @timestamp, functionName
| filter msg = "Cold start — first invocation"
| stats count() by functionName
```

#### CLI (Console)

When running locally (no `AWS_LAMBDA_FUNCTION_NAME`), output uses `ConsoleWriter` for human-readable terminal format:

```
10:15:32 INF Logger initialized level=debug
10:15:32 INF API Lambda init complete function=media-lambda goVersion=go1.24.0 region=ap-southeast-2 ...
10:15:33 DBG Handler entry: handleTriageStart method=POST path=/api/triage/start
10:15:33 INF Triage job dispatched to Worker Lambda jobId=triage-abc123 sessionId=550e8400-...
```

### Logging Conventions

#### Field naming

| Convention | Example | Notes |
|------------|---------|-------|
| Use `Str()` for string fields | `log.Info().Str("jobId", id).Msg("...")` | |
| Use `Err()` for errors | `log.Error().Err(err).Msg("failed")` | Always attach the error object |
| Use `Int()` for counts | `log.Debug().Int("count", len(items)).Msg("...")` | |
| Use `Dur()` for timing | `log.Debug().Dur("elapsed", time.Since(start)).Msg("...")` | |
| Use `Bool()` for flags | `log.Info().Bool("instagramEnabled", true).Msg("...")` | |
| Keep messages sentence-cased | `Msg("Triage job dispatched")` | Start with capital, no trailing period |
| Use camelCase for field names | `Str("sessionId", id)` | Consistent across codebase |
| Add context via `With()` | `log.With().Str("jobId", id).Logger()` | For sub-loggers in long functions |

#### Message style

- **Good:** `"Failed to download file for triage"` — actionable, clear operation context
- **Bad:** `"Error in download"` — vague, no operation context
- **Good:** `"Triage complete"` with `.Int("keep", 5).Int("discard", 3)` — summary in fields
- **Bad:** `"Triage complete: 5 keep, 3 discard"` — summary in message string (not queryable)

#### Context enrichment

Every long-running handler or job should create a sub-logger with correlation IDs:

```go
logger := log.With().
    Str("sessionId", event.SessionID).
    Str("jobId", event.JobID).
    Logger()

logger.Info().Msg("Starting triage processing")
// All subsequent logs include sessionId and jobId
```

For Lambda handlers, use the logging helper:

```go
logger := logging.WithLambdaContext(ctx)
// Includes requestId and functionName automatically
```

### Sensitive Data Rules

| Category | Rule | Example |
|----------|------|---------|
| API keys | **Never log** | Gemini API key, Instagram app secret |
| Access tokens | **Never log** | Instagram access token, OAuth tokens |
| SSM parameter values | **Never log** | Any `*result.Parameter.Value` |
| Presigned URLs | **Never log full URL** | Log the S3 key only: `log.Debug().Str("key", key).Msg("Presigned URL generated")` |
| Session IDs | Allowed | `log.Debug().Str("sessionId", id)` |
| Job IDs | Allowed | `log.Debug().Str("jobId", id)` |
| S3 keys | Allowed | `log.Debug().Str("key", "session-uuid/photo.jpg")` |
| Container IDs | Allowed | `log.Info().Str("containerId", id)` |
| File names | Allowed | `log.Debug().Str("filename", name)` |
| File sizes | Allowed | `log.Debug().Int64("size", bytes)` |
| Durations | Allowed | `log.Debug().Dur("elapsed", d)` |

### What to Log: Per-Component Guide

#### Lambda init (every Lambda)

```go
func init() {
    initStart := time.Now()
    logging.Init()

    // ... setup ...

    log.Info().
        Str("function", "media-lambda").
        Str("goVersion", runtime.Version()).
        Str("region", cfg.Region).
        Bool("dynamoEnabled", sessionStore != nil).
        Bool("instagramEnabled", igClient != nil).
        Dur("initDuration", time.Since(initStart)).
        Msg("API Lambda init complete")
}
```

#### Cold start detection (every Lambda)

```go
var coldStart = true

func handler(ctx context.Context, event Event) error {
    if coldStart {
        coldStart = false
        log.Info().Str("function", "worker-lambda").Msg("Cold start — first invocation")
    }
    // ...
}
```

#### SSM parameter loads (every Lambda that uses SSM)

```go
ssmStart := time.Now()
result, err := ssmClient.GetParameter(ctx, &ssm.GetParameterInput{...})
if err != nil {
    log.Fatal().Err(err).Str("param", paramName).Msg("Failed to read API key from SSM")
}
os.Setenv("GEMINI_API_KEY", *result.Parameter.Value)
log.Debug().Str("param", paramName).Dur("elapsed", time.Since(ssmStart)).Msg("Gemini API key loaded from SSM")
```

#### HTTP handlers (API Lambda)

```go
func handleTriageStart(w http.ResponseWriter, r *http.Request) {
    log.Debug().Str("method", r.Method).Str("path", r.URL.Path).Msg("Handler entry: handleTriageStart")
    // ... validation ...
    if err != nil {
        log.Warn().Str("param", "sessionId").Msg("Invalid session ID format")
        httpError(w, http.StatusBadRequest, "invalid session ID")
        return
    }
    // ... dispatch ...
    log.Info().Str("jobId", jobID).Str("sessionId", req.SessionID).Msg("Triage job dispatched")
}
```

#### Worker Lambda job handlers

```go
func handleTriage(ctx context.Context, event WorkerEvent) error {
    jobStart := time.Now()
    log.Debug().Str("sessionId", event.SessionID).Str("model", model).Msg("Starting triage processing")

    // Log each external call with timing
    log.Debug().Str("prefix", prefix).Msg("Listing S3 objects")
    // ... S3 call ...
    log.Debug().Int("objectCount", len(result.Contents)).Msg("S3 objects listed")

    // Log skipped items as Warn
    if !filehandler.IsSupported(ext) {
        log.Warn().Str("key", key).Str("ext", ext).Msg("Skipping unsupported file type")
        continue
    }

    // Log completion with summary
    log.Info().
        Int("keep", len(keep)).
        Int("discard", len(discard)).
        Dur("duration", time.Since(jobStart)).
        Msg("Triage complete")
}
```

#### External API calls (Gemini, Instagram, DynamoDB)

```go
// Always log: call start, timing, result summary
apiStart := time.Now()
log.Debug().Str("model", model).Int("promptLen", len(prompt)).Msg("Sending to Gemini API")
resp, err := client.GenerateContent(ctx, parts...)
if err != nil {
    log.Error().Err(err).Dur("elapsed", time.Since(apiStart)).Msg("Gemini API call failed")
    return err
}
log.Debug().Int("responseLen", len(resp.Text())).Dur("elapsed", time.Since(apiStart)).Msg("Gemini API response received")
```

#### Error paths (must never be silent)

```go
// BAD — silent return nil:
if job == nil {
    return nil
}

// GOOD — log the error before returning:
if job == nil {
    log.Error().Str("jobId", event.JobID).Str("sessionId", event.SessionID).Msg("Enhancement job not found for feedback")
    return nil
}
```

### Implementation

Logging is initialized via `GEMINI_LOG_LEVEL` environment variable:

```go
package logging

import (
    "context"
    "os"

    "github.com/aws/aws-lambda-go/lambdacontext"
    "github.com/rs/zerolog"
    "github.com/rs/zerolog/log"
)

func Init() {
    level := os.Getenv("GEMINI_LOG_LEVEL")
    switch level {
    case "trace":
        zerolog.SetGlobalLevel(zerolog.TraceLevel)
    case "info":
        zerolog.SetGlobalLevel(zerolog.InfoLevel)
    case "warn":
        zerolog.SetGlobalLevel(zerolog.WarnLevel)
    case "error":
        zerolog.SetGlobalLevel(zerolog.ErrorLevel)
    default:
        // Default to debug — this is the only environment (no staging/dev).
        zerolog.SetGlobalLevel(zerolog.DebugLevel)
    }

    // Use JSON output in Lambda for CloudWatch; console writer for local/CLI.
    if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" {
        log.Logger = zerolog.New(os.Stderr).With().Timestamp().Logger()
    } else {
        log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
    }

    log.Info().Str("level", zerolog.GlobalLevel().String()).Msg("Logger initialized")
}

// WithLambdaContext returns a sub-logger enriched with Lambda request ID
// and function name extracted from the Lambda context.
func WithLambdaContext(ctx context.Context) zerolog.Logger {
    if lc, ok := lambdacontext.FromContext(ctx); ok {
        return log.With().
            Str("requestId", lc.AwsRequestID).
            Str("functionName", lambdacontext.FunctionName).
            Logger()
    }
    return log.Logger
}

// WithJob returns a sub-logger enriched with sessionId and jobId fields.
func WithJob(sessionId, jobId string) zerolog.Logger {
    return log.With().
        Str("sessionId", sessionId).
        Str("jobId", jobId).
        Logger()
}
```

### Controlling Log Level at Runtime

Set the `GEMINI_LOG_LEVEL` environment variable on the Lambda function:

| Value | Effect | When to use |
|-------|--------|-------------|
| (unset) | `debug` — full diagnostic output | Default for all environments |
| `trace` | Everything including DynamoDB PK/SK, marshal details, per-frame data | Targeted deep debugging only |
| `info` | Business events only, reduced volume | If log costs become a concern |
| `warn` | Only warnings and errors | Minimal logging |
| `error` | Errors only | Emergency noise reduction |

To change at runtime without redeployment:

```bash
aws lambda update-function-configuration \
    --function-name ai-social-media-worker \
    --environment "Variables={GEMINI_LOG_LEVEL=trace,...}"
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

**Last Updated**: 2026-02-09  
**Version**: 2.0.0
