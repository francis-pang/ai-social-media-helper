# Testing Strategy

## Unit Tests

- Test each package independently
- Use Go's `testing` package
- Co-locate tests with source (`*_test.go`)
- Mock external dependencies

### Example Test Structure

```go
// internal/auth/auth_test.go
package auth

import (
    "os"
    "testing"
)

func TestGetAPIKey_FromEnvironment(t *testing.T) {
    // Set up
    os.Setenv("GEMINI_API_KEY", "test-key")
    defer os.Unsetenv("GEMINI_API_KEY")
    
    // Execute
    key, err := GetAPIKey()
    
    // Assert
    if err != nil {
        t.Fatalf("expected no error, got %v", err)
    }
    if key != "test-key" {
        t.Errorf("expected 'test-key', got '%s'", key)
    }
}

func TestGetAPIKey_NoKeyFound(t *testing.T) {
    // Ensure no env var
    os.Unsetenv("GEMINI_API_KEY")
    
    // Execute
    _, err := GetAPIKey()
    
    // Assert
    if err == nil {
        t.Fatal("expected error when no API key found")
    }
}
```

---

## Integration Tests

- Test CLI commands end-to-end
- Test file upload flow
- Test session persistence
- Test error scenarios

### Example Integration Test

```go
func TestUploadCommand_Integration(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping integration test in short mode")
    }
    
    // Create temp file
    tmpFile, err := os.CreateTemp("", "test-*.jpg")
    if err != nil {
        t.Fatal(err)
    }
    defer os.Remove(tmpFile.Name())
    
    // Run upload command
    cmd := exec.Command("./gemini-cli", "upload", tmpFile.Name())
    output, err := cmd.CombinedOutput()
    
    // Assert success
    if err != nil {
        t.Fatalf("upload failed: %s", output)
    }
}
```

---

## Test Coverage Goals

| Category | Target |
|----------|--------|
| Overall | Minimum 80% |
| Critical paths | 100% |
| Error handling | All error paths tested |
| Edge cases | Documented and tested |

### Running Tests

```bash
# Run all tests
go test ./...

# Run with coverage
go test -cover ./...

# Generate coverage report
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# Run only short tests
go test -short ./...

# Run with verbose output
go test -v ./...
```

---

## Test Categories

### 1. Configuration Tests
- Environment variable loading
- GPG file decryption
- Default value application
- Invalid configuration handling

### 2. File Handler Tests
- MIME type detection
- File size validation
- Unsupported file types
- Missing files
- Permission errors

### 3. API Client Tests
- Successful API calls
- Error response handling
- Timeout behavior
- Retry logic

### 4. Session Tests
- Session creation
- Session persistence
- Session loading
- Concurrent access

### 5. CLI Tests
- Command parsing
- Argument validation
- Output formatting
- Error display

---

## Mocking Strategy

### External Dependencies

Use interfaces for mockability:

```go
// Mockable interface
type GeminiClient interface {
    GenerateContent(ctx context.Context, prompt string) (string, error)
    UploadFile(ctx context.Context, path string) (*File, error)
}

// Real implementation
type realClient struct {
    client *genai.Client
}

// Mock for testing
type mockClient struct {
    response string
    err      error
}

func (m *mockClient) GenerateContent(ctx context.Context, prompt string) (string, error) {
    return m.response, m.err
}
```

---

## Continuous Integration

### GitHub Actions Workflow

```yaml
name: Test

on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      
      - uses: actions/setup-go@v5
        with:
          go-version: '1.23'
      
      - name: Run tests
        run: go test -v -coverprofile=coverage.out ./...
      
      - name: Upload coverage
        uses: codecov/codecov-action@v4
        with:
          file: ./coverage.out
```

---

**Last Updated**: 2025-12-31

