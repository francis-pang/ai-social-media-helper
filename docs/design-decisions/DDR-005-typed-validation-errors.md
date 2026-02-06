# DDR-005: Typed Validation Errors

**Date**: 2025-12-30  
**Status**: Accepted  
**Iteration**: 5

## Context

API validation can fail for multiple reasons (no key, invalid key, network issues, quota exceeded). We needed a way to handle these cases distinctly and provide appropriate user guidance.

## Decision

Create explicit `ValidationErrorType` enum with typed `ValidationError` struct.

## Rationale

- **Compile-time safety**: Missing error handlers are caught at build time
- **Consistent UX**: Each error type maps to a specific, tested message
- **Testability**: Error types can be asserted in unit tests
- **Extensibility**: New types integrate without breaking existing code

## Implementation

```go
type ValidationErrorType int

const (
    ErrTypeNoKey ValidationErrorType = iota
    ErrTypeInvalidKey
    ErrTypeNetworkError
    ErrTypeQuotaExceeded
    ErrTypeUnknown
)

type ValidationError struct {
    Type    ValidationErrorType
    Message string
    Err     error
}
```

## Dual Error Detection Strategy

Errors are classified using both:
1. **HTTP status codes**: Reliable for Google API errors wrapped in `googleapi.Error`
2. **Pattern matching**: Catches errors before HTTP layer (DNS, connection issues)

Pattern keywords:
- Invalid key: "api key not valid", "api_key_invalid", "permission denied"
- Quota: "quota", "resource exhausted", "rate limit"
- Network: "connection", "timeout", "dial", "no such host"

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| String error matching only | Fragile; messages may change |
| Single generic error | No actionable guidance for users |
| Panic on auth failures | Poor UX; no graceful recovery |

## Consequences

- **Positive**: Clear, consistent error handling
- **Positive**: Each error type has specific remediation steps
- **Trade-off**: More code to maintain for error classification

