# DDR-000: Language Selection — Go over Java

**Date**: 2025-12-30
**Status**: Accepted

## Context

The Gemini Media Analysis CLI needed a primary language. Both Go and Java have official Gemini API SDKs, strong CLI ecosystems, and mature cloud SDKs (AWS, GCP). The application is a command-line tool that uploads media files to Google's Gemini API, requiring fast startup, efficient file streaming, and simple distribution.

## Decision

**Go** was selected as the primary language for the entire project.

## Rationale

1. **CLI-first design** — Go's ecosystem (Cobra, single-binary output) is purpose-built for CLI tools. Less boilerplate than Java's Picocli/class-based approach.
2. **Startup time** — Native binary starts in <10ms vs 100-500ms JVM initialization. Critical for CLI tools invoked frequently.
3. **Single-binary deployment** — `go build` produces one statically-linked binary with zero runtime dependencies. Java requires JVM on the target system.
4. **Efficient file handling** — Go's `io.Reader`/`io.Writer` interfaces provide simple, memory-efficient streaming for large media files.
5. **Cross-compilation** — `GOOS=linux GOARCH=amd64 go build` produces Lambda-ready binaries trivially.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Java + Picocli | JVM startup overhead (~500ms), heavier binaries (~50MB+ with dependencies), requires JVM on target |
| Python | No official Gemini SDK at the time, slower execution, complex packaging for CLI distribution |

## Consequences

**Positive:**
- Fast startup (<10ms) and low memory (~5-20MB)
- Single binary distribution — no runtime dependencies
- Excellent AWS Lambda support via `provided.al2023` runtime
- Goroutines for concurrent file processing

**Trade-offs:**
- Smaller ecosystem than Java for enterprise patterns
- No inheritance/class hierarchies (mitigated by Go's interface/composition model)
- Team must have Go experience

## Related Documents

- [DDR-001](./DDR-001-iterative-implementation.md) — Iterative implementation approach
