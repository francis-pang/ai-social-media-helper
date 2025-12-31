# Design Decisions

This document captures key architectural and implementation decisions made during development.

---

## Iterative Implementation Approach

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

---

## Logging Before Features

**Decision**: Implement logging infrastructure (Iteration 2) before core functionality.

**Rationale**:
- **Debugging support**: All subsequent code benefits from structured logging
- **Observability from day one**: Issues during development are traceable
- **Consistent patterns**: Logging conventions established early are followed throughout
- **Lower cost**: Adding logging later requires touching every file

---

## GPG Credential Storage

**Decision**: Use GPG encryption for API key storage rather than plaintext or third-party secrets managers.

**Rationale**:
- **Security**: AES-256 encryption protects keys at rest
- **Developer familiarity**: GPG is standard tooling for developers
- **Portability**: Encrypted files can be synced across machines
- **No dependencies**: Uses system GPG binary, no additional packages

**Trade-offs Accepted**:
- Requires GPG key setup (documented in setup script)
- Passphrase entry needed (gpg-agent caches for session)

See [AUTHENTICATION.md](./AUTHENTICATION.md) for full details.

---

## Non-Interactive GPG Decryption

**Decision**: Support passphrase file (`.gpg-passphrase`) for automated/non-interactive environments.

**Rationale**:
- **CI/CD compatibility**: Automated pipelines cannot enter passphrases interactively
- **Development convenience**: Avoids repeated passphrase entry during development
- **Security via gitignore**: Passphrase file is gitignored, never committed

**Implementation**:
- Passphrase file: `.gpg-passphrase` in project root or executable directory
- GPG flags: `--pinentry-mode loopback --passphrase-file`
- Fallback: Interactive GPG agent if passphrase file is missing

---

## Startup API Key Validation

**Decision**: Validate API key with a real API call before any operations.

**Rationale**:
- **Fail fast**: Users learn about auth issues immediately
- **Clear diagnostics**: Typed errors provide specific guidance
- **No wasted work**: Prevents failed uploads due to bad credentials
- **Free tier compatible**: Uses `gemini-3-flash-preview` (free of charge)

**Validation Approach**:
- Minimal "hi" request to generative model
- ~1-2 second latency on fast connections
- Five distinct error types with user-friendly messages

---

## Typed Validation Errors

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

---

## Model Selection: gemini-3-flash-preview

**Decision**: Use `gemini-3-flash-preview` as the default model for validation and text generation.

**Reference**: [Gemini API Pricing](https://ai.google.dev/gemini-api/docs/pricing)

**Rationale**:
- **Free tier compatible**: Explicitly free of charge per the pricing documentation
- **Low latency**: Flash models are optimized for speed
- **Multimodal support**: Handles text, images, videos, and audio
- **Latest model**: Most intelligent Flash model with superior search and grounding
- **Consistent**: Same model used for validation and chat operations

**Alternatives Evaluated**:

| Model | Issue |
|-------|-------|
| `gemini-2.0-flash` | Rate limited to 0 requests on free tier |
| `gemini-2.0-flash-lite` | Rate limited to 0 requests on free tier |
| `gemini-2.5-flash` | Works, but `gemini-3-flash-preview` is the latest free-tier model |
| `gemini-pro` | Higher latency, unnecessary for validation |
| List models API | Doesn't verify generation permissions |

---

## Dual Error Detection Strategy

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

## Date-Embedded Questions for Testing

**Decision**: Build questions that include the current date for testing variability.

**Rationale**:
- **Testable responses**: Different dates produce different AI responses
- **Reproducibility context**: Logs show what date was used in the prompt
- **Real-world simulation**: Mimics actual usage patterns for news/current events queries

**Implementation** (`internal/chat/chat.go`):
```go
func BuildDailyNewsQuestion() string {
    now := time.Now()
    dateStr := now.Format("Monday, January 2, 2006")
    return fmt.Sprintf(
        "Today is %s. What are the major news events...",
        dateStr,
    )
}
```

---

**Last Updated**: 2025-12-31

