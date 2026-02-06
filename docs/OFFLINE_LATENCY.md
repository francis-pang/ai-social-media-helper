# Offline & Latency Handling Design Document

## Overview

This document describes how the Gemini Media Analysis CLI handles offline scenarios, network degradation, and high-latency conditions. The goal is to provide clear feedback to users and graceful degradation when network conditions are poor.

---

## Connectivity Detection

### Pre-Flight Check

Before executing network-dependent commands, perform a quick connectivity check:

```go
package network

import (
    "context"
    "net"
    "net/http"
    "time"
)

type ConnectivityStatus struct {
    Online        bool
    Latency       time.Duration
    LastChecked   time.Time
    ErrorMessage  string
}

type ConnectivityChecker struct {
    endpoints []string
    timeout   time.Duration
    cache     *ConnectivityStatus
    cacheTTL  time.Duration
}

func NewConnectivityChecker() *ConnectivityChecker {
    return &ConnectivityChecker{
        endpoints: []string{
            "https://generativelanguage.googleapis.com",  // Gemini API
            "https://www.google.com",                     // Fallback
        },
        timeout:  5 * time.Second,
        cacheTTL: 30 * time.Second,
    }
}

func (c *ConnectivityChecker) Check(ctx context.Context) *ConnectivityStatus {
    // Return cached result if recent
    if c.cache != nil && time.Since(c.cache.LastChecked) < c.cacheTTL {
        return c.cache
    }
    
    status := &ConnectivityStatus{LastChecked: time.Now()}
    
    for _, endpoint := range c.endpoints {
        start := time.Now()
        
        req, err := http.NewRequestWithContext(ctx, "HEAD", endpoint, nil)
        if err != nil {
            continue
        }
        
        client := &http.Client{Timeout: c.timeout}
        resp, err := client.Do(req)
        
        if err == nil {
            resp.Body.Close()
            status.Online = true
            status.Latency = time.Since(start)
            break
        }
        
        status.ErrorMessage = categorizeConnectivityError(err)
    }
    
    c.cache = status
    return status
}

func categorizeConnectivityError(err error) string {
    errStr := err.Error()
    
    switch {
    case contains(errStr, "no such host"):
        return "DNS resolution failed - check your internet connection"
    case contains(errStr, "connection refused"):
        return "Connection refused - server may be down"
    case contains(errStr, "network is unreachable"):
        return "Network unreachable - check your connection"
    case contains(errStr, "timeout"):
        return "Connection timed out - network may be slow"
    case contains(errStr, "certificate"):
        return "SSL/TLS error - check your network settings"
    default:
        return fmt.Sprintf("Network error: %s", err.Error())
    }
}
```

### Connectivity Check Flow

```
User runs command
        │
        ▼
┌───────────────────┐
│ Check cached      │
│ connectivity      │
└────────┬──────────┘
         │
    ┌────┴────┐
    │ Cached? │
    └────┬────┘
         │
    Yes──┴──No
     │      │
     ▼      ▼
┌────────┐ ┌────────────┐
│ Use    │ │ Quick ping │
│ cache  │ │ check      │
└────┬───┘ └─────┬──────┘
     │           │
     └─────┬─────┘
           │
      ┌────┴────┐
      │ Online? │
      └────┬────┘
           │
      Yes──┴──No
       │      │
       ▼      ▼
  ┌────────┐ ┌────────────────┐
  │Execute │ │Show offline    │
  │command │ │message, offer  │
  └────────┘ │offline actions │
             └────────────────┘
```

---

## Offline Mode

### Capabilities When Offline

| Feature | Offline Support | Notes |
|---------|-----------------|-------|
| View session list | ✅ Full | Local data only |
| View session history | ✅ Full | Local data only |
| View uploaded file refs | ✅ Full | Metadata only |
| Export session | ✅ Full | Export to file |
| Upload new files | ❌ None | Requires API |
| Ask questions | ❌ None | Requires API |
| Create new session | ✅ Partial | Creates locally, syncs later |
| Delete session | ✅ Full | Local delete |

### Offline Detection Messages

```go
func (app *App) HandleOffline(status *ConnectivityStatus) {
    fmt.Fprintln(os.Stderr, "")
    fmt.Fprintln(os.Stderr, "⚠️  You appear to be offline")
    fmt.Fprintln(os.Stderr, "")
    
    if status.ErrorMessage != "" {
        fmt.Fprintf(os.Stderr, "Reason: %s\n", status.ErrorMessage)
        fmt.Fprintln(os.Stderr, "")
    }
    
    fmt.Fprintln(os.Stderr, "Available offline commands:")
    fmt.Fprintln(os.Stderr, "  gemini-cli session list     - View your sessions")
    fmt.Fprintln(os.Stderr, "  gemini-cli session show     - View session history")
    fmt.Fprintln(os.Stderr, "  gemini-cli session export   - Export session to file")
    fmt.Fprintln(os.Stderr, "  gemini-cli config show      - View configuration")
    fmt.Fprintln(os.Stderr, "")
    fmt.Fprintln(os.Stderr, "Check your connection and try again.")
}
```

### Command-Level Offline Handling

```go
// Commands that require network
var networkRequiredCommands = map[string]bool{
    "upload":      true,
    "ask":         true,
    "interactive": true,
    "auth verify": true,
}

// Commands that work offline
var offlineCapableCommands = map[string]bool{
    "session list":   true,
    "session show":   true,
    "session export": true,
    "session delete": true,
    "config show":    true,
    "config init":    true,
    "help":           true,
    "version":        true,
}

func (app *App) ExecuteCommand(cmd string, args []string) error {
    // Skip connectivity check for offline-capable commands
    if offlineCapableCommands[cmd] {
        return app.runCommand(cmd, args)
    }
    
    // Check connectivity for network-required commands
    if networkRequiredCommands[cmd] {
        status := app.connectivity.Check(context.Background())
        if !status.Online {
            app.HandleOffline(status)
            return fmt.Errorf("command requires network connectivity")
        }
    }
    
    return app.runCommand(cmd, args)
}
```

---

## Latency Handling

### Latency Thresholds

| Latency | Classification | User Experience |
|---------|----------------|-----------------|
| < 500ms | Excellent | Normal operation |
| 500ms - 2s | Good | Normal operation |
| 2s - 5s | Slow | Show progress indicator |
| 5s - 15s | Very Slow | Show progress + warning |
| > 15s | Poor | Warning + suggestion to retry |

### Progress Indicators

```go
package progress

import (
    "fmt"
    "io"
    "sync"
    "time"
)

type Spinner struct {
    mu       sync.Mutex
    message  string
    frames   []string
    current  int
    stop     chan struct{}
    stopped  bool
    writer   io.Writer
}

func NewSpinner(message string) *Spinner {
    return &Spinner{
        message: message,
        frames:  []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
        stop:    make(chan struct{}),
        writer:  os.Stderr,
    }
}

func (s *Spinner) Start() {
    go func() {
        ticker := time.NewTicker(100 * time.Millisecond)
        defer ticker.Stop()
        
        for {
            select {
            case <-s.stop:
                return
            case <-ticker.C:
                s.mu.Lock()
                fmt.Fprintf(s.writer, "\r%s %s", s.frames[s.current], s.message)
                s.current = (s.current + 1) % len(s.frames)
                s.mu.Unlock()
            }
        }
    }()
}

func (s *Spinner) UpdateMessage(msg string) {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.message = msg
}

func (s *Spinner) Stop() {
    if !s.stopped {
        close(s.stop)
        s.stopped = true
        fmt.Fprint(s.writer, "\r\033[K") // Clear line
    }
}

func (s *Spinner) Success(msg string) {
    s.Stop()
    fmt.Fprintf(s.writer, "✓ %s\n", msg)
}

func (s *Spinner) Error(msg string) {
    s.Stop()
    fmt.Fprintf(s.writer, "✗ %s\n", msg)
}
```

### Upload Progress

```go
type UploadProgress struct {
    spinner     *Spinner
    startTime   time.Time
    totalBytes  int64
    uploadedBytes int64
    lastUpdate  time.Time
}

func NewUploadProgress(filename string, totalBytes int64) *UploadProgress {
    return &UploadProgress{
        spinner:    NewSpinner(fmt.Sprintf("Uploading %s...", filename)),
        startTime:  time.Now(),
        totalBytes: totalBytes,
    }
}

func (p *UploadProgress) Start() {
    p.spinner.Start()
}

func (p *UploadProgress) Update(uploadedBytes int64) {
    p.uploadedBytes = uploadedBytes
    
    // Update every 500ms at most
    if time.Since(p.lastUpdate) < 500*time.Millisecond {
        return
    }
    p.lastUpdate = time.Now()
    
    elapsed := time.Since(p.startTime)
    percent := float64(uploadedBytes) / float64(p.totalBytes) * 100
    speed := float64(uploadedBytes) / elapsed.Seconds() // bytes/sec
    
    var speedStr string
    switch {
    case speed > 1024*1024:
        speedStr = fmt.Sprintf("%.1f MB/s", speed/(1024*1024))
    case speed > 1024:
        speedStr = fmt.Sprintf("%.1f KB/s", speed/1024)
    default:
        speedStr = fmt.Sprintf("%.0f B/s", speed)
    }
    
    // Estimate remaining time
    remaining := time.Duration(float64(p.totalBytes-uploadedBytes) / speed * float64(time.Second))
    
    msg := fmt.Sprintf("Uploading... %.1f%% (%s, ~%s remaining)", 
        percent, speedStr, formatDuration(remaining))
    
    p.spinner.UpdateMessage(msg)
}

func (p *UploadProgress) Complete() {
    elapsed := time.Since(p.startTime)
    speed := float64(p.totalBytes) / elapsed.Seconds()
    
    var speedStr string
    if speed > 1024*1024 {
        speedStr = fmt.Sprintf("%.1f MB/s", speed/(1024*1024))
    } else {
        speedStr = fmt.Sprintf("%.1f KB/s", speed/1024)
    }
    
    p.spinner.Success(fmt.Sprintf("Upload complete (%s in %s, %s)", 
        formatBytes(p.totalBytes), formatDuration(elapsed), speedStr))
}
```

### Slow Network Warning

```go
func (c *Client) ExecuteWithLatencyWarning(ctx context.Context, op func() error) error {
    start := time.Now()
    
    // Start a goroutine to warn about slow operation
    warningShown := false
    done := make(chan struct{})
    
    go func() {
        select {
        case <-done:
            return
        case <-time.After(5 * time.Second):
            if !warningShown {
                warningShown = true
                fmt.Fprintln(os.Stderr, "")
                fmt.Fprintln(os.Stderr, "⚠️  This is taking longer than expected...")
                fmt.Fprintln(os.Stderr, "    Network may be slow. Still working...")
            }
        }
        
        select {
        case <-done:
            return
        case <-time.After(10 * time.Second): // 15 seconds total
            fmt.Fprintln(os.Stderr, "")
            fmt.Fprintln(os.Stderr, "⚠️  Very slow response detected.")
            fmt.Fprintln(os.Stderr, "    Press Ctrl+C to cancel and try again later.")
        }
    }()
    
    err := op()
    close(done)
    
    elapsed := time.Since(start)
    if elapsed > 5*time.Second && err == nil {
        fmt.Fprintf(os.Stderr, "\n(Completed in %s - network was slow)\n", formatDuration(elapsed))
    }
    
    return err
}
```

---

## Connection Recovery

### Automatic Retry on Connection Loss

```go
func (c *Client) UploadWithRecovery(ctx context.Context, filePath string) (*File, error) {
    var lastErr error
    
    for attempt := 1; attempt <= 3; attempt++ {
        // Check connectivity before attempt
        status := c.connectivity.Check(ctx)
        if !status.Online {
            c.logger.Warn("Waiting for network connection...",
                slog.Int("attempt", attempt))
            
            // Wait for connection with exponential backoff
            waitTime := time.Duration(attempt*attempt) * 5 * time.Second
            
            select {
            case <-ctx.Done():
                return nil, ctx.Err()
            case <-time.After(waitTime):
                continue
            }
        }
        
        // Attempt upload
        file, err := c.Upload(ctx, filePath)
        if err == nil {
            if attempt > 1 {
                c.logger.Info("Upload succeeded after recovery",
                    slog.Int("attempts", attempt))
            }
            return file, nil
        }
        
        lastErr = err
        
        // Check if error is connection-related
        if !isConnectionError(err) {
            return nil, err // Don't retry non-connection errors
        }
        
        c.logger.Warn("Connection lost during upload, will retry",
            slog.Int("attempt", attempt),
            slog.String("error", err.Error()))
    }
    
    return nil, fmt.Errorf("upload failed after connection recovery attempts: %w", lastErr)
}

func isConnectionError(err error) bool {
    errStr := err.Error()
    connectionErrors := []string{
        "connection reset",
        "connection refused", 
        "broken pipe",
        "network is unreachable",
        "no route to host",
        "EOF",
    }
    
    for _, ce := range connectionErrors {
        if strings.Contains(strings.ToLower(errStr), ce) {
            return true
        }
    }
    
    return false
}
```

### Interactive Mode Connection Handling

```go
func (app *App) RunInteractive() error {
    fmt.Println("Gemini Media CLI - Interactive Mode")
    fmt.Println("Type 'help' for commands, 'exit' to quit")
    fmt.Println("")
    
    reader := bufio.NewReader(os.Stdin)
    
    for {
        // Check connection status (cached, fast)
        status := app.connectivity.Check(context.Background())
        
        var prompt string
        if status.Online {
            prompt = "gemini> "
        } else {
            prompt = "gemini (offline)> "
        }
        
        fmt.Print(prompt)
        
        input, err := reader.ReadString('\n')
        if err != nil {
            if err == io.EOF {
                fmt.Println("\nGoodbye!")
                return nil
            }
            return err
        }
        
        input = strings.TrimSpace(input)
        if input == "" {
            continue
        }
        
        if input == "exit" || input == "quit" {
            fmt.Println("Goodbye!")
            return nil
        }
        
        // Handle command
        if err := app.handleInteractiveCommand(input, status); err != nil {
            fmt.Fprintf(os.Stderr, "Error: %s\n", err)
        }
    }
}

func (app *App) handleInteractiveCommand(input string, status *ConnectivityStatus) error {
    parts := strings.Fields(input)
    if len(parts) == 0 {
        return nil
    }
    
    cmd := parts[0]
    
    // Check if command requires network
    if networkRequiredCommands[cmd] && !status.Online {
        return fmt.Errorf("'%s' requires network connection. You are currently offline", cmd)
    }
    
    // Execute command
    return app.ExecuteCommand(cmd, parts[1:])
}
```

---

## Network Quality Feedback

### Connection Quality Indicator

```go
type ConnectionQuality int

const (
    QualityExcellent ConnectionQuality = iota
    QualityGood
    QualitySlow
    QualityVerySlow
    QualityPoor
    QualityOffline
)

func (q ConnectionQuality) String() string {
    switch q {
    case QualityExcellent:
        return "Excellent"
    case QualityGood:
        return "Good"
    case QualitySlow:
        return "Slow"
    case QualityVerySlow:
        return "Very Slow"
    case QualityPoor:
        return "Poor"
    case QualityOffline:
        return "Offline"
    default:
        return "Unknown"
    }
}

func (q ConnectionQuality) Emoji() string {
    switch q {
    case QualityExcellent:
        return "🟢"
    case QualityGood:
        return "🟢"
    case QualitySlow:
        return "🟡"
    case QualityVerySlow:
        return "🟠"
    case QualityPoor:
        return "🔴"
    case QualityOffline:
        return "⚫"
    default:
        return "⚪"
    }
}

func classifyLatency(latency time.Duration) ConnectionQuality {
    switch {
    case latency < 500*time.Millisecond:
        return QualityExcellent
    case latency < 2*time.Second:
        return QualityGood
    case latency < 5*time.Second:
        return QualitySlow
    case latency < 15*time.Second:
        return QualityVerySlow
    default:
        return QualityPoor
    }
}
```

### Status Command

```bash
$ gemini-cli status

Connection Status
─────────────────
Status:   🟢 Online
Latency:  234ms (Excellent)
Endpoint: generativelanguage.googleapis.com

Session Status
──────────────
Active Session: abc123
Files:          3
Messages:       12
Last Active:    2 minutes ago
```

---

## Timeout Guidance

### Dynamic Timeout Suggestions

```go
func (app *App) suggestTimeout(fileSize int64, currentLatency time.Duration) time.Duration {
    // Base timeout on file size (assume minimum 500KB/s)
    uploadTime := time.Duration(fileSize/500000) * time.Second
    
    // Add buffer based on current latency
    latencyBuffer := currentLatency * 10 // Account for variance
    
    // Minimum 30 seconds, maximum 10 minutes
    suggested := uploadTime + latencyBuffer
    if suggested < 30*time.Second {
        suggested = 30 * time.Second
    }
    if suggested > 10*time.Minute {
        suggested = 10 * time.Minute
    }
    
    return suggested
}

func (app *App) handleTimeoutError(err error, fileSize int64) {
    status := app.connectivity.Check(context.Background())
    
    fmt.Fprintln(os.Stderr, "")
    fmt.Fprintln(os.Stderr, "⏱️  Request timed out")
    fmt.Fprintln(os.Stderr, "")
    
    if status.Online {
        suggested := app.suggestTimeout(fileSize, status.Latency)
        fmt.Fprintf(os.Stderr, "Your current network latency is %s.\n", status.Latency)
        fmt.Fprintf(os.Stderr, "For a file of this size (%s), try:\n", formatBytes(fileSize))
        fmt.Fprintln(os.Stderr, "")
        fmt.Fprintf(os.Stderr, "  gemini-cli upload <file> --timeout %s\n", suggested)
    } else {
        fmt.Fprintln(os.Stderr, "You appear to be offline. Check your connection.")
    }
}
```

---

## Summary

| Scenario | Behavior |
|----------|----------|
| **Offline** | Detect early, show offline-capable commands |
| **Slow connection** | Show progress, warn after 5s, suggest cancel after 15s |
| **Connection lost mid-operation** | Automatic retry with backoff |
| **Timeout** | Clear message with suggested timeout value |
| **Interactive mode** | Show connection status in prompt |

### User Messaging Principles

1. **Early detection** - Check connectivity before long operations
2. **Clear feedback** - Always explain what's happening
3. **Actionable suggestions** - Provide specific remediation steps
4. **Graceful degradation** - Offer offline alternatives when possible
5. **Progress visibility** - Show progress for operations > 1 second

---

**Last Updated**: 2025-12-30  
**Version**: 1.0.0

