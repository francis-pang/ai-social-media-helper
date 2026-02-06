# DDR-013: Unified Metadata Extraction Architecture

**Date**: 2025-12-31  
**Status**: Accepted  
**Iteration**: 10

## Context

The Gemini Media CLI needs to extract metadata from various media formats (JPEG, PNG, HEIC, MP4, MOV, MKV) to provide contextual information in AI prompts. The challenge was to design a unified architecture that:

1. Handles diverse formats with different metadata structures
2. Balances purity (no external dependencies) with completeness (full metadata extraction)
3. Provides a consistent interface for consuming code
4. Manages memory efficiently for large media files

An initial recommendation suggested using pure Go libraries for all formats:
- `rwcarlsen/goexif` for JPEG/PNG
- `evanoerlo/heic` for HEIC (note: this library doesn't exist)
- `abema/go-mp4` for MP4

This DDR evaluates these recommendations against our requirements and documents the chosen architecture.

## Decision

Implement a **Split-Provider Model** with a unified `MediaMetadata` interface. This strategy maintains pure Go for images while acknowledging that video metadata is too fragmented across vendors to handle reliably without specialized tools.

| Media Category | Extraction Method | Library/Tool | Rationale |
|----------------|-------------------|--------------|-----------|
| **HEIC, JPEG, TIFF** | Pure Go | `evanoberholster/imagemeta` | Native HEIC/EXIF support without CGo |
| **PNG, WebP** | Pure Go | `evanoberholster/imagemeta` | Graceful handling of limited metadata |
| **MP4, MOV, MKV, AVI** | External Tool | `ffprobe` (FFmpeg) | Reliable vendor-specific GPS extraction |

Both implementations share a common `MediaMetadata` interface that abstracts the extraction method from consuming code.

## Rationale

### Why `evanoberholster/imagemeta` is the Image Winner

This library is currently the most active and feature-complete pure-Go metadata library for our use case.

#### Technical Capabilities

| Feature | Implementation Detail |
|---------|----------------------|
| **HEIC Support** | Parses BMFF (Base Media File Format) container structure natively to locate the EXIF block |
| **Memory Efficiency** | Uses `io.Reader` and `io.Seeker` pattern—reads only metadata bytes, not entire 20MB photos |
| **GPS Accuracy** | Handles complex EXIF "Rational" math (degrees/minutes/seconds) and converts to float64 |
| **Timezone Handling** | Preserves timezone information from DateTimeOriginal and OffsetTimeOriginal |
| **Format Detection** | Auto-detects JPEG vs HEIC vs TIFF from file headers |

#### HEIC/BMFF Parsing Detail

HEIC files are not simple images—they are ISO Base Media File Format (BMFF) containers. The library:

1. Parses the `ftyp` box to identify the container type
2. Navigates the `meta` box hierarchy
3. Locates the `iloc` (item location) and `iprp` (item properties) boxes
4. Extracts the raw EXIF bytes from the appropriate item
5. Parses EXIF data using standard IFD (Image File Directory) structure

This is why legacy libraries like `rwcarlsen/goexif` fail on HEIC—they expect raw EXIF at the file start.

#### GPS Coordinate Extraction

EXIF stores GPS as "Rational" values (pairs of 32-bit integers representing numerator/denominator):

```
Latitude:  40° 44' 55.0404" N
Stored as: [40/1, 44/1, 550404/10000] with GPSLatitudeRef = "N"
```

The library handles:
- Rational to decimal conversion
- Reference direction (N/S for latitude, E/W for longitude)
- Edge cases like 0/0 (undefined) values

### Why NOT `rwcarlsen/goexif`

The suggested `rwcarlsen/goexif` library was explicitly rejected because:

- **No HEIC Support**: iPhone photos (iOS 11+, since 2017) default to HEIC format
- **Maintenance Status**: Last release was 2019; limited recent activity
- **Limited Format Support**: Only JPEG and some TIFF variants
- **No BMFF Parsing**: Cannot navigate container formats

### Why NOT `bep/imagemeta` as Primary

Research identified `github.com/bep/imagemeta` as potentially stronger for PNG `tEXt`/`iTXt` chunks. However:

| Consideration | Decision |
|---------------|----------|
| PNG rarely has meaningful metadata | Most are screenshots or web graphics |
| EXIF in PNG is non-standard | Few cameras output PNG |
| Added dependency for edge case | Not worth the complexity |
| `evanoberholster/imagemeta` handles PNG | Graceful no-metadata response |

**Future Option**: If PNG metadata becomes important (e.g., AI-generated images with embedded prompts), consider adding `bep/imagemeta` as a fallback for PNG specifically.

### The Video Dilemma

Pure Go libraries for video exist but are fundamentally **low-level tools**. They provide raw "atoms" (MP4) or "elements" (MKV), but don't translate vendor-specific metadata automatically.

#### Why NOT Pure Go for Video (`abema/go-mp4`)

| Requirement | `abema/go-mp4` | `ffprobe` |
|-------------|----------------|-----------|
| GPS Coordinates | ❌ Manual atom parsing required | ✅ Automatic extraction |
| Creation Time | ⚠️ Basic (`mvhd` box only) | ✅ Multiple fallback sources |
| Vendor Metadata | ❌ No Samsung/Apple/DJI support | ✅ All vendors supported |
| Multiple Containers | ❌ MP4/MOV only | ✅ MP4, MOV, MKV, AVI, WebM |
| Implementation Effort | High (custom atom parsing) | Low (JSON output) |

#### Vendor-Specific GPS Atoms

GPS location in videos is stored differently by each manufacturer:

| Vendor | Atom/Box Path | Format |
|--------|---------------|--------|
| **Apple** | `moov/udta/©xyz` | ISO 6709 string: `+37.7749-122.4194/` |
| **Samsung** | `moov/udta/com.android.gps_latitude` | Float or string |
| **DJI** | Custom XMP or `DGRJ` box | Proprietary binary |
| **GoPro** | `udta/GPMF` stream | Binary telemetry |

Building parsers for each vendor would require:
- Reverse-engineering proprietary formats
- Ongoing maintenance as vendors change implementations
- Testing across dozens of device/firmware combinations

### Why `ffprobe` for Video

| Criteria | Score | Notes |
|----------|-------|-------|
| GPS Extraction | ✅ | All vendor formats supported via unified `-print_format json` |
| Creation Time | ✅ | Checks `creation_time` in format tags, stream tags, and file metadata |
| Video Properties | ✅ | Duration, resolution, codec, frame rate, bit rate, color space |
| Format Support | ✅ | MP4, MOV, MKV, AVI, WebM, and dozens more |
| JSON Output | ✅ | Clean structured output, easy to unmarshal to Go structs |
| Reliability | ✅ | Battle-tested in production by millions of applications |
| Installation | ⚠️ | Requires FFmpeg (but widely available via package managers) |

The ffprobe dependency follows the same pattern as GPG for credential storage (DDR-003)—a well-maintained external tool that provides superior functionality.

#### Alternative Considered: `go-exiftool`

Research also identified `github.com/barasher/go-exiftool` as an option (wraps ExifTool instead of ffprobe).

| Criteria | `ffprobe` | `go-exiftool` |
|----------|-----------|---------------|
| Installation | FFmpeg (widely installed) | ExifTool (Perl required) |
| Video Focus | ✅ Primary purpose | ⚠️ General-purpose |
| Streaming Data | ✅ Duration, codec, bitrate | ❌ Metadata only |
| JSON Output | ✅ Native | ✅ Native |
| Binary Size | Larger (full FFmpeg) | Smaller (Perl script) |

**Decision**: Stick with `ffprobe` because:
1. FFmpeg is more commonly pre-installed
2. We need video stream properties (duration, resolution) not just metadata
3. Perl dependency for ExifTool is less common than FFmpeg

## Architecture

### The Split-Provider Model

```
                    ┌──────────────────────────────────────┐
                    │         MediaMetadata Interface      │
                    │  ┌──────────────────────────────┐   │
                    │  │ FormatMetadataContext()      │   │
                    │  │ GetMediaType() string        │   │
                    │  │ HasGPSData() bool            │   │
                    │  │ GetGPS() (lat, lon float64)  │   │
                    │  │ HasDateData() bool           │   │
                    │  │ GetDate() time.Time          │   │
                    │  └──────────────────────────────┘   │
                    └──────────────────────────────────────┘
                                      │
                 ┌────────────────────┴────────────────────┐
                 │                                         │
    ┌────────────▼────────────┐           ┌───────────────▼───────────────┐
    │     ImageMetadata       │           │       VideoMetadata           │
    │   (Pure Go Provider)    │           │   (External Tool Provider)    │
    ├─────────────────────────┤           ├───────────────────────────────┤
    │ • evanoberholster/      │           │ • ffprobe subprocess          │
    │   imagemeta             │           │ • JSON output parsing         │
    │ • BMFF/HEIC parsing     │           │ • Vendor-agnostic GPS         │
    │ • EXIF Rational math    │           │ • Stream properties           │
    │ • io.Reader streaming   │           │ • ISO 6709 location parsing   │
    └─────────────────────────┘           └───────────────────────────────┘
```

### The `MediaMetadata` Interface

```go
type MediaMetadata interface {
    // FormatMetadataContext returns formatted text for AI prompts
    FormatMetadataContext() string
    
    // GetMediaType returns "image" or "video"
    GetMediaType() string
    
    // GPS methods
    HasGPSData() bool
    GetGPS() (latitude, longitude float64)
    
    // Date methods
    HasDateData() bool
    GetDate() time.Time
}
```

This interface enables polymorphic handling—the consuming code (prompt builder, chat handler) doesn't need to know whether metadata came from imagemeta or ffprobe.

### Memory-Efficient Processing

The `io.Reader` and `io.Seeker` pattern ensures we never load entire media files into memory:

```go
// ✅ Good: Stream from file (reads only needed bytes)
file, _ := os.Open(filePath)
defer file.Close()
exifData, _ := imagemeta.Decode(file)  // Seeks to metadata, reads ~64KB

// ❌ Bad: Load entire file (wastes memory)
data, _ := os.ReadFile(filePath)  // Loads 20MB HEIC into RAM
```

For a 20MB HEIC photo, the streaming approach reads approximately 64KB of metadata versus loading the full 20MB.

### Unified Flow

```
┌─────────────────┐
│ LoadMediaFile() │
└────────┬────────┘
         │
         ├──► IsImage(ext)?
         │         │
         │         ▼
         │    ┌────────────────────────────────┐
         │    │ ExtractImageMetadata()         │
         │    │ • os.Open() → io.Reader        │
         │    │ • imagemeta.Decode()           │
         │    │ • Extract GPS (Rational→float) │
         │    │ • Extract DateTime             │
         │    │ • Extract Make/Model           │
         │    └────────────────────────────────┘
         │         │
         │         ▼
         │    ImageMetadata implements MediaMetadata
         │
         └──► IsVideo(ext)?
                   │
                   ▼
              ┌────────────────────────────────┐
              │ ExtractVideoMetadata()         │
              │ • exec.Command("ffprobe")      │
              │ • Parse JSON output            │
              │ • Extract GPS (ISO 6709)       │
              │ • Extract creation_time        │
              │ • Extract stream properties    │
              └────────────────────────────────┘
                   │
                   ▼
              VideoMetadata implements MediaMetadata
```

## PNG Metadata Limitation

PNGs use `tEXt`, `iTXt`, and `zTXt` chunks—not EXIF. Most PNGs lack meaningful metadata:

| PNG Source | Typical Metadata | GPS/Date Available? |
|------------|------------------|---------------------|
| Screenshots | None or minimal | ❌ No |
| Converted from JPEG | Usually stripped | ❌ No |
| Camera (rare) | May have XMP | ⚠️ Sometimes |
| Graphics software | Creation software only | ❌ No |
| AI-generated | May have prompt in `tEXt` | ❌ No GPS |

The implementation gracefully handles missing metadata rather than failing. If PNG metadata becomes critical in the future, `bep/imagemeta` could be added as a specialized PNG handler.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| `exiftool` CLI for all formats | Perl dependency; less common than FFmpeg |
| `go-exiftool` wrapper | Same Perl dependency; no stream properties |
| `rwcarlsen/goexif` | No HEIC support (critical for iPhone photos) |
| `bep/imagemeta` as primary | Optimized for PNG/WebP; HEIC support unclear |
| Pure Go for video (`abema/go-mp4`) | No vendor GPS parsing; significant implementation effort |
| Cloud-based extraction | Adds latency; privacy concerns; API costs |
| Skip metadata entirely | Loses valuable context for AI analysis (DDR-007) |

## Consequences

### Positive

- **Single interface** for all media types (`MediaMetadata`)
- **Best-of-breed** tools for each format category
- **Memory efficient** streaming via `io.Reader`/`io.Seeker` pattern
- **Comprehensive GPS** extraction including vendor-specific formats
- **Graceful degradation** when metadata unavailable
- **HEIC native support** via BMFF parsing—no conversion needed
- **Accurate GPS** via proper Rational math conversion

### Trade-offs

- **ffprobe dependency** for video (install via `brew install ffmpeg` or package manager)
- **Two extraction paths** to maintain (but isolated behind interface)
- **Startup validation** needed to check ffprobe availability

### Mitigations

- Clear error messages when ffprobe not found
- Video features degrade gracefully without ffprobe
- Documentation includes FFmpeg installation instructions
- Interface abstraction allows swapping implementations later

## Implementation Files

- `internal/filehandler/handler.go`: MediaMetadata interface and implementations
- `internal/chat/chat.go`: Consumes MediaMetadata via interface

## Related Documents

- [DDR-007](./DDR-007-hybrid-exif-prompt.md) - Hybrid prompt strategy (extract locally, pass as text)
- [DDR-008](./DDR-008-pure-go-exif-library.md) - Pure Go EXIF library selection
- [DDR-010](./DDR-010-heic-format-support.md) - HEIC format support
- [DDR-011](./DDR-011-video-metadata-and-upload.md) - Video metadata with ffprobe
- [DDR-012](./DDR-012-files-api-for-all-uploads.md) - Files API for uploads

