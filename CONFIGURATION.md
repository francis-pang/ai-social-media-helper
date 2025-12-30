# Configuration Design Document

## Overview

This document defines the complete configuration system for the Gemini Media Analysis CLI. Configuration controls application behavior, resource limits, API settings, and operational parameters.

---

## Configuration Hierarchy

Configuration values are resolved in the following order (highest priority first):

1. **Command-line flags** (e.g., `--timeout 60s`)
2. **Environment variables** (e.g., `GEMINI_TIMEOUT=60s`)
3. **Configuration file** (`~/.gemini-media-cli/config.yaml`)
4. **Built-in defaults**

---

## Configuration Categories

### 1. API Configuration

| Key | Env Variable | CLI Flag | Default | Description |
|-----|--------------|----------|---------|-------------|
| `api.key` | `GEMINI_API_KEY` | `--api-key` | (required) | Gemini API key (see AUTHENTICATION.md) |
| `api.model` | `GEMINI_MODEL` | `--model` | `gemini-2.0-flash-exp` | Model to use for generation |
| `api.base_url` | `GEMINI_BASE_URL` | `--base-url` | (SDK default) | Override API endpoint (for testing/proxy) |
| `api.timeout` | `GEMINI_TIMEOUT` | `--timeout` | `120s` | Request timeout for API calls |

### 2. Resource Limits

| Key | Env Variable | CLI Flag | Default | Description |
|-----|--------------|----------|---------|-------------|
| `limits.max_file_size` | `GEMINI_MAX_FILE_SIZE` | `--max-file-size` | `2GB` | Maximum file size for uploads |
| `limits.max_concurrent_uploads` | `GEMINI_MAX_CONCURRENT_UPLOADS` | `--max-concurrent` | `3` | Max parallel file uploads |
| `limits.max_files_per_session` | `GEMINI_MAX_FILES_PER_SESSION` | - | `50` | Max files in a single session |
| `limits.temp_dir_max_size` | `GEMINI_TEMP_DIR_MAX_SIZE` | - | `10GB` | Max temp directory usage |
| `limits.max_prompt_length` | `GEMINI_MAX_PROMPT_LENGTH` | - | `30000` | Max characters in a prompt |

### 3. Session Configuration

| Key | Env Variable | CLI Flag | Default | Description |
|-----|--------------|----------|---------|-------------|
| `session.dir` | `GEMINI_SESSION_DIR` | `--session-dir` | `~/.gemini-media-cli/sessions` | Session storage directory |
| `session.auto_create` | `GEMINI_SESSION_AUTO_CREATE` | - | `true` | Auto-create session on first upload |
| `session.max_history` | `GEMINI_SESSION_MAX_HISTORY` | - | `100` | Max messages to retain per session |

### 4. Logging Configuration

| Key | Env Variable | CLI Flag | Default | Description |
|-----|--------------|----------|---------|-------------|
| `log.level` | `GEMINI_LOG_LEVEL` | `--log-level` | `info` | Log level: debug, info, warn, error |
| `log.format` | `GEMINI_LOG_FORMAT` | `--log-format` | `text` | Log format: text, json |
| `log.file` | `GEMINI_LOG_FILE` | `--log-file` | (stderr) | Log output file path |
| `log.redact_secrets` | `GEMINI_LOG_REDACT` | - | `true` | Redact API keys/secrets in logs |

### 5. Network Configuration

| Key | Env Variable | CLI Flag | Default | Description |
|-----|--------------|----------|---------|-------------|
| `network.proxy` | `HTTPS_PROXY` / `HTTP_PROXY` | `--proxy` | (system) | HTTP(S) proxy URL |
| `network.no_proxy` | `NO_PROXY` | - | (system) | Hosts to bypass proxy |
| `network.connect_timeout` | `GEMINI_CONNECT_TIMEOUT` | - | `10s` | TCP connection timeout |
| `network.idle_timeout` | `GEMINI_IDLE_TIMEOUT` | - | `90s` | Idle connection timeout |

### 6. Retry Configuration

| Key | Env Variable | CLI Flag | Default | Description |
|-----|--------------|----------|---------|-------------|
| `retry.max_attempts` | `GEMINI_RETRY_MAX_ATTEMPTS` | `--max-retries` | `3` | Max retry attempts for failed requests |
| `retry.initial_delay` | `GEMINI_RETRY_INITIAL_DELAY` | - | `1s` | Initial backoff delay |
| `retry.max_delay` | `GEMINI_RETRY_MAX_DELAY` | - | `30s` | Maximum backoff delay |
| `retry.multiplier` | `GEMINI_RETRY_MULTIPLIER` | - | `2.0` | Exponential backoff multiplier |

### 7. Output Configuration

| Key | Env Variable | CLI Flag | Default | Description |
|-----|--------------|----------|---------|-------------|
| `output.format` | `GEMINI_OUTPUT_FORMAT` | `--output` | `text` | Output format: text, json, markdown |
| `output.color` | `GEMINI_COLOR` | `--color` / `--no-color` | `auto` | Color output: auto, always, never |
| `output.verbose` | `GEMINI_VERBOSE` | `-v`, `--verbose` | `false` | Verbose output |
| `output.quiet` | `GEMINI_QUIET` | `-q`, `--quiet` | `false` | Suppress non-essential output |

---

## Configuration File Format

### Location

The configuration file is located at `~/.gemini-media-cli/config.yaml`.

### Example Configuration

```yaml
# ~/.gemini-media-cli/config.yaml

# API Settings
api:
  # API key should be stored securely - see AUTHENTICATION.md
  # Do NOT put your API key here
  model: "gemini-2.0-flash-exp"
  timeout: "120s"

# Resource Limits
limits:
  max_file_size: "2GB"
  max_concurrent_uploads: 3
  max_files_per_session: 50
  temp_dir_max_size: "10GB"
  max_prompt_length: 30000

# Session Settings
session:
  dir: "~/.gemini-media-cli/sessions"
  auto_create: true
  max_history: 100

# Logging
log:
  level: "info"
  format: "text"
  redact_secrets: true

# Network
network:
  connect_timeout: "10s"
  idle_timeout: "90s"

# Retry Policy
retry:
  max_attempts: 3
  initial_delay: "1s"
  max_delay: "30s"
  multiplier: 2.0

# Output
output:
  format: "text"
  color: "auto"
  verbose: false
```

---

## Implementation

### Config Struct

```go
package config

import (
    "time"
)

type Config struct {
    API     APIConfig     `yaml:"api"`
    Limits  LimitsConfig  `yaml:"limits"`
    Session SessionConfig `yaml:"session"`
    Log     LogConfig     `yaml:"log"`
    Network NetworkConfig `yaml:"network"`
    Retry   RetryConfig   `yaml:"retry"`
    Output  OutputConfig  `yaml:"output"`
}

type APIConfig struct {
    Key     string        `yaml:"-"`           // Never persist to file
    Model   string        `yaml:"model"`
    BaseURL string        `yaml:"base_url"`
    Timeout time.Duration `yaml:"timeout"`
}

type LimitsConfig struct {
    MaxFileSize          int64 `yaml:"max_file_size"`
    MaxConcurrentUploads int   `yaml:"max_concurrent_uploads"`
    MaxFilesPerSession   int   `yaml:"max_files_per_session"`
    TempDirMaxSize       int64 `yaml:"temp_dir_max_size"`
    MaxPromptLength      int   `yaml:"max_prompt_length"`
}

type SessionConfig struct {
    Dir        string `yaml:"dir"`
    AutoCreate bool   `yaml:"auto_create"`
    MaxHistory int    `yaml:"max_history"`
}

type LogConfig struct {
    Level        string `yaml:"level"`
    Format       string `yaml:"format"`
    File         string `yaml:"file"`
    RedactSecrets bool  `yaml:"redact_secrets"`
}

type NetworkConfig struct {
    Proxy          string        `yaml:"proxy"`
    NoProxy        string        `yaml:"no_proxy"`
    ConnectTimeout time.Duration `yaml:"connect_timeout"`
    IdleTimeout    time.Duration `yaml:"idle_timeout"`
}

type RetryConfig struct {
    MaxAttempts  int           `yaml:"max_attempts"`
    InitialDelay time.Duration `yaml:"initial_delay"`
    MaxDelay     time.Duration `yaml:"max_delay"`
    Multiplier   float64       `yaml:"multiplier"`
}

type OutputConfig struct {
    Format  string `yaml:"format"`
    Color   string `yaml:"color"`
    Verbose bool   `yaml:"verbose"`
    Quiet   bool   `yaml:"quiet"`
}
```

### Loading Order

```go
func LoadConfig(flags *Flags) (*Config, error) {
    // 1. Start with defaults
    cfg := DefaultConfig()
    
    // 2. Load from config file (if exists)
    if err := cfg.LoadFromFile(); err != nil {
        // Only error if file exists but is invalid
        if !os.IsNotExist(err) {
            return nil, fmt.Errorf("config file error: %w", err)
        }
    }
    
    // 3. Override with environment variables
    cfg.LoadFromEnv()
    
    // 4. Override with CLI flags
    cfg.LoadFromFlags(flags)
    
    // 5. Validate final configuration
    if err := cfg.Validate(); err != nil {
        return nil, fmt.Errorf("config validation: %w", err)
    }
    
    return cfg, nil
}
```

### Default Values

```go
func DefaultConfig() *Config {
    return &Config{
        API: APIConfig{
            Model:   "gemini-2.0-flash-exp",
            Timeout: 120 * time.Second,
        },
        Limits: LimitsConfig{
            MaxFileSize:          2 * 1024 * 1024 * 1024, // 2GB
            MaxConcurrentUploads: 3,
            MaxFilesPerSession:   50,
            TempDirMaxSize:       10 * 1024 * 1024 * 1024, // 10GB
            MaxPromptLength:      30000,
        },
        Session: SessionConfig{
            Dir:        "~/.gemini-media-cli/sessions",
            AutoCreate: true,
            MaxHistory: 100,
        },
        Log: LogConfig{
            Level:         "info",
            Format:        "text",
            RedactSecrets: true,
        },
        Network: NetworkConfig{
            ConnectTimeout: 10 * time.Second,
            IdleTimeout:    90 * time.Second,
        },
        Retry: RetryConfig{
            MaxAttempts:  3,
            InitialDelay: 1 * time.Second,
            MaxDelay:     30 * time.Second,
            Multiplier:   2.0,
        },
        Output: OutputConfig{
            Format:  "text",
            Color:   "auto",
            Verbose: false,
            Quiet:   false,
        },
    }
}
```

---

## Validation Rules

### Required Fields

- `api.key` must be set (via env var or secure storage)

### Value Constraints

| Field | Constraint |
|-------|------------|
| `api.timeout` | >= 1s, <= 10m |
| `limits.max_file_size` | >= 1MB, <= 20GB |
| `limits.max_concurrent_uploads` | >= 1, <= 10 |
| `limits.max_files_per_session` | >= 1, <= 200 |
| `log.level` | One of: debug, info, warn, error |
| `log.format` | One of: text, json |
| `output.format` | One of: text, json, markdown |
| `output.color` | One of: auto, always, never |
| `retry.max_attempts` | >= 0, <= 10 |
| `retry.multiplier` | >= 1.0, <= 5.0 |

### Validation Implementation

```go
func (c *Config) Validate() error {
    var errs []error
    
    // API key is required
    if c.API.Key == "" {
        errs = append(errs, fmt.Errorf("API key is required"))
    }
    
    // Timeout bounds
    if c.API.Timeout < time.Second || c.API.Timeout > 10*time.Minute {
        errs = append(errs, fmt.Errorf("timeout must be between 1s and 10m"))
    }
    
    // File size bounds
    if c.Limits.MaxFileSize < 1024*1024 || c.Limits.MaxFileSize > 20*1024*1024*1024 {
        errs = append(errs, fmt.Errorf("max file size must be between 1MB and 20GB"))
    }
    
    // Log level validation
    validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
    if !validLevels[c.Log.Level] {
        errs = append(errs, fmt.Errorf("invalid log level: %s", c.Log.Level))
    }
    
    if len(errs) > 0 {
        return fmt.Errorf("validation errors: %v", errs)
    }
    
    return nil
}
```

---

## Size Parsing

Support human-readable size formats:

```go
func ParseSize(s string) (int64, error) {
    s = strings.TrimSpace(strings.ToUpper(s))
    
    multipliers := map[string]int64{
        "B":  1,
        "KB": 1024,
        "MB": 1024 * 1024,
        "GB": 1024 * 1024 * 1024,
        "TB": 1024 * 1024 * 1024 * 1024,
    }
    
    for suffix, mult := range multipliers {
        if strings.HasSuffix(s, suffix) {
            numStr := strings.TrimSuffix(s, suffix)
            num, err := strconv.ParseFloat(numStr, 64)
            if err != nil {
                return 0, fmt.Errorf("invalid size: %s", s)
            }
            return int64(num * float64(mult)), nil
        }
    }
    
    // Try parsing as plain number (bytes)
    return strconv.ParseInt(s, 10, 64)
}
```

---

## Environment Variable Naming

All environment variables use the prefix `GEMINI_` and follow this pattern:

- Nested keys use underscore: `api.timeout` → `GEMINI_API_TIMEOUT`
- Use uppercase: `GEMINI_MAX_FILE_SIZE`
- Exception: `HTTPS_PROXY`, `HTTP_PROXY`, `NO_PROXY` follow system conventions

---

## Configuration Commands

### Show Current Configuration

```bash
gemini-cli config show
```

Output:
```
API:
  Model:    gemini-2.0-flash-exp
  Timeout:  2m0s
  Key:      ****...**** (redacted)

Limits:
  Max File Size:           2.0 GB
  Max Concurrent Uploads:  3
  Max Files Per Session:   50

Session:
  Directory:   ~/.gemini-media-cli/sessions
  Auto Create: true
  Max History: 100

...
```

### Initialize Configuration File

```bash
gemini-cli config init
```

Creates `~/.gemini-media-cli/config.yaml` with documented defaults.

### Validate Configuration

```bash
gemini-cli config validate
```

Checks current configuration for errors without running any commands.

---

## Security Considerations

1. **API keys should NEVER be stored in the config file**
   - Use secure storage (see AUTHENTICATION.md)
   - Environment variables for CI/scripts only

2. **Config file permissions**
   - Created with `0600` permissions (owner read/write only)
   - Warning if file has broader permissions

3. **Log redaction**
   - `log.redact_secrets: true` (default) masks API keys in logs
   - Pattern: `sk-...` → `sk-****...****`

---

## Future Considerations

- **Profile support**: Named configuration profiles for different use cases
- **Remote configuration**: Fetch config from a URL (for team sharing)
- **Schema versioning**: Handle config file format changes gracefully

---

**Last Updated**: 2025-12-30  
**Version**: 1.0.0
