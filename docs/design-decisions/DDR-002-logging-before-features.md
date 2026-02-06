# DDR-002: Logging Infrastructure First

**Date**: 2025-12-30  
**Status**: Accepted  
**Iteration**: 2

## Context

When building the CLI, we needed logging for debugging and observability. The question was whether to add logging as an afterthought or implement it early in the project.

## Decision

Implement logging infrastructure (Iteration 2) before core functionality, using `zerolog` for structured logging.

## Rationale

- **Debugging support**: All subsequent code benefits from structured logging from day one
- **Observability from day one**: Issues during development are traceable
- **Consistent patterns**: Logging conventions established early are followed throughout
- **Lower cost**: Adding logging later requires touching every file
- **Environment-configurable**: Log levels can be adjusted via `GEMINI_LOG_LEVEL`

## Implementation

```go
package logging

func Init() {
    level := os.Getenv("GEMINI_LOG_LEVEL")
    switch level {
    case "debug":
        zerolog.SetGlobalLevel(zerolog.DebugLevel)
    // ... other levels
    }
    log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
}
```

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Add logging later | Requires retrofitting; inconsistent patterns |
| Use `fmt.Printf` | No structure, no levels, harder to filter |
| Use `log` stdlib | Limited features, no structured output |

## Consequences

- **Positive**: Full visibility into application behavior from the start
- **Positive**: Consistent logging patterns across all packages
- **Trade-off**: Small overhead even for trivial operations

