# DDR-010: HEIC/HEIF Image Format Support

**Date**: 2025-12-31  
**Status**: Accepted  
**Iteration**: 7

## Context

Modern iPhones (iOS 11+) use HEIC (High Efficiency Image Container) as the default photo format. Users uploading iPhone photos would encounter issues if HEIC wasn't supported.

## Decision

Support Apple's HEIC/HEIF formats in addition to standard web formats (JPEG, PNG, GIF, WebP).

## Rationale

- **iPhone default**: HEIC is the default format on iOS 11+ (since 2017)
- **Quality preservation**: HEIC provides better quality at smaller file sizes than JPEG
- **User convenience**: No manual conversion required before upload
- **Market share**: iPhones represent ~50% of US smartphone market

## Supported Formats

| Extension | MIME Type | Common Source |
|-----------|-----------|---------------|
| `.jpg`, `.jpeg` | `image/jpeg` | Most cameras, web |
| `.png` | `image/png` | Screenshots, graphics |
| `.gif` | `image/gif` | Animated images |
| `.webp` | `image/webp` | Modern web format |
| `.heic` | `image/heic` | iPhone (iOS 11+) |
| `.heif` | `image/heif` | HEIC variant |

## Technical Implementation

HEIC support required:
1. MIME type mapping in `filehandler`
2. EXIF library with HEIC support (DDR-008: `imagemeta`)
3. Gemini API accepts HEIC images natively

```go
var SupportedImageExtensions = map[string]string{
    ".jpg":  "image/jpeg",
    ".jpeg": "image/jpeg",
    ".png":  "image/png",
    ".gif":  "image/gif",
    ".webp": "image/webp",
    ".heic": "image/heic",
    ".heif": "image/heif",
}
```

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Convert HEIC to JPEG before upload | Quality loss; extra processing step |
| Require users to convert manually | Poor UX; friction for iPhone users |
| Support JPEG only | Excludes default iPhone photos |

## Consequences

- **Positive**: iPhone users can upload photos directly
- **Positive**: Full EXIF metadata preserved (including GPS, timestamp)
- **Trade-off**: Required selecting an EXIF library with HEIC support
- **Trade-off**: HEIC files may be slightly slower to process

## Related Documents

- [DDR-008](./DDR-008-pure-go-exif-library.md) - EXIF library selection

