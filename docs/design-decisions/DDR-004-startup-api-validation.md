# DDR-004: Startup API Key Validation

**Date**: 2025-12-30  
**Status**: Accepted  
**Iteration**: 5

## Context

Users may have invalid, expired, or misconfigured API keys. We needed to decide when and how to detect authentication issues.

## Decision

Validate API key with a real API call before any operations. Fail fast with clear, typed error messages.

## Rationale

- **Fail fast**: Users learn about auth issues immediately, not after lengthy operations
- **Clear diagnostics**: Typed errors provide specific guidance for each issue
- **No wasted work**: Prevents failed uploads due to bad credentials
- **Free tier compatible**: Uses `gemini-3-flash-preview` (free of charge)

## Validation Approach

1. Make a minimal "hi" request to the generative model
2. Parse the response or error
3. Classify errors into typed categories
4. Exit with actionable error message

Latency: ~1-2 seconds on fast connections

## Error Classification

| Type | When Returned | User Action |
|------|---------------|-------------|
| `ErrTypeNoKey` | No API key found in any source | Set env var or run setup script |
| `ErrTypeInvalidKey` | Key rejected by API (400/401/403) | Regenerate API key |
| `ErrTypeNetworkError` | Connection failures, server errors (5xx) | Check internet connection |
| `ErrTypeQuotaExceeded` | Rate limited (429) | Wait for quota reset |
| `ErrTypeUnknown` | Unclassified errors | Check logs for details |

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Validate only on first real operation | Poor UX; user waits for upload then fails |
| Validate key format only | Doesn't catch revoked/invalid keys |
| List models API | Doesn't verify generation permissions |
| Skip validation | Confusing errors deep in workflows |

## Consequences

- **Positive**: Immediate feedback on credential issues
- **Positive**: Actionable error messages
- **Trade-off**: ~1-2 second startup latency
- **Trade-off**: Uses one API call from quota (minimal impact)

