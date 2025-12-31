# DDR-008: Pure Go EXIF Library

**Date**: 2025-12-31  
**Status**: Accepted  
**Iteration**: 7

## Context

Following the decision to extract EXIF metadata locally (DDR-007), we needed to select a library or tool for EXIF extraction. The primary requirement was HEIC/HEIF support since iPhone photos use this format by default.

## Decision

Use `github.com/evanoberholster/imagemeta` instead of external tools like `exiftool`.

## Rationale

- **No external dependencies**: Works without system tools installed
- **Cross-platform**: Same binary works on macOS, Linux, Windows
- **Faster execution**: No subprocess spawning overhead
- **HEIC/HEIF support**: Native support for modern Apple image formats
- **Timezone-aware**: Properly parses date/time with timezone information
- **Single binary deployment**: Aligns with Go's deployment model

## Library Comparison

| Library | HEIC Support | External Deps | Performance |
|---------|--------------|---------------|-------------|
| `evanoberholster/imagemeta` | ✅ Yes | None | Fast |
| `rwcarlsen/goexif` | ❌ No | None | Fast |
| `dsoprea/go-exif` | ❌ No | None | Fast |
| `exiftool` (CLI) | ✅ Yes | System binary | Slower (subprocess) |

## Implementation

```go
import "github.com/evanoberholster/imagemeta"

func ExtractMetadata(filePath string) (*ImageMetadata, error) {
    file, _ := os.Open(filePath)
    defer file.Close()
    
    exifData, err := imagemeta.Decode(file)
    if err != nil {
        return nil, err
    }
    
    return &ImageMetadata{
        Latitude:    exifData.GPS.Latitude(),
        Longitude:   exifData.GPS.Longitude(),
        DateTaken:   exifData.DateTimeOriginal(),
        CameraMake:  exifData.Make,
        CameraModel: exifData.Model,
    }, nil
}
```

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| `exiftool` CLI | Requires system installation; subprocess overhead |
| `rwcarlsen/goexif` | No HEIC support; iPhone photos wouldn't work |
| Build custom parser | Too much effort; reinventing the wheel |

## Consequences

- **Positive**: Single binary with no external dependencies
- **Positive**: Works with iPhone HEIC photos out of the box
- **Trade-off**: Library may not support all exotic camera formats
- **Mitigation**: Falls back gracefully if metadata cannot be extracted

## Related Documents

- [DDR-007](./DDR-007-hybrid-exif-prompt.md) - Hybrid prompt strategy
- [DDR-010](./DDR-010-heic-format-support.md) - HEIC format support

