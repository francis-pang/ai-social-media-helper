# DDR-017: Francis Reference Photo for Person Identification

**Date**: 2026-01-01  
**Status**: Accepted  
**Iteration**: 10

## Context

The existing photo selection and analysis prompts reference Francis as "the owner of this photo/video" (see `chat.go` prompts). However, Gemini has no visual reference to identify Francis in photos. The prompt tells Gemini *about* Francis but does not enable Gemini to *recognize* Francis.

This creates a gap where:
- The "People Variety" criterion in DDR-016 cannot properly prioritize photos containing the photo owner
- Single-photo analysis cannot accurately describe which person is Francis
- Gemini must guess who Francis is based on context alone

A reference photo of Francis exists at `~/path/to/reference-photo.jpg` (taken August 12, 2025 in Los Angeles).

## Decision

1. **Store a reference photo** of Francis in the project at `internal/assets/reference-photos/francis-reference.jpg`
2. **Embed the photo** in the binary using Go's `//go:embed` directive
3. **Include the reference photo** as the first image in all Gemini API requests that involve photo analysis or selection
4. **Add prompt context** explaining that the first image is a reference photo for identification purposes

**No changes to selection priorities** - DDR-016 criteria remain exactly as documented:
1. Subject/Scene Diversity (Highest Priority)
2. Scene Representation
3. Enhancement Potential (For Duplicates Only)
4. People Variety (Lower Priority)
5. Time of Day (Tiebreaker Only)

The reference photo simply enables Gemini to correctly identify Francis when evaluating the existing criteria.

## Rationale

1. **Bridges Existing Gap**: Prompts already mention Francis; this provides the missing visual reference
2. **Embedded in Binary**: Using `go:embed` ensures the reference is always available without external dependencies
3. **Full Quality**: Reference photo is sent at full quality (not thumbnailed) for accurate identification
4. **Single Person Design**: Hardcoded for Francis only; no need for multi-person complexity at this time
5. **Committed to Git**: Small JPEG (~1.8MB) is acceptable for version control and ensures consistency

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| User-configurable reference photo location | Adds unnecessary complexity; single user system |
| XDG data directory storage | Requires external file management; embedding is simpler |
| Multiple named persons support | Over-engineering; only Francis needs identification |
| Thumbnailed reference photo | May lose facial detail needed for accurate identification |
| External file path at runtime | Risk of missing file; embedding guarantees availability |

## Consequences

**Positive:**
- Gemini can now visually identify Francis in photos
- "People Variety" criterion works correctly for photos containing the owner
- Single-photo analysis accurately describes Francis when present
- Reference is always available (embedded in binary)
- Consistent identification across all API calls

**Trade-offs:**
- Binary size increases by ~1.8MB (acceptable for CLI tool)
- Each API request includes an additional image (minimal token overhead)
- Hardcoded to Francis only (acceptable for single-user design)
- Reference photo must be manually updated if Francis's appearance changes significantly

## Implementation Details

### File Location

```
internal/
  assets/
    reference-photos/
      francis-reference.jpg    # ~1.8MB, copied from external source
```

### Go Embed Directive

```go
//go:embed assets/reference-photos/francis-reference.jpg
var francisReferencePhoto []byte
```

### API Request Order

1. **Francis reference photo** (full quality, with explanatory label)
2. **Target photo(s)** (thumbnails for selection, full for analysis)
3. **Text prompt** with metadata and instructions

### Prompt Addition

```
REFERENCE PHOTO: The first image is a reference photo of Francis, the owner of these photos. 
Use this to identify Francis in the candidate photos.
```

## Related Documents

- [DDR-016: Quality-Agnostic Metadata-Driven Photo Selection](DDR-016-quality-agnostic-photo-selection.md)
- [DDR-014: Thumbnail-Based Multi-Image Selection Strategy](DDR-014-thumbnail-selection-strategy.md)

