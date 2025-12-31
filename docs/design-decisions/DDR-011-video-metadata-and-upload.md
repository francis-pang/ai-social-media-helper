# DDR-011: Video Metadata Extraction and Large File Upload

## Status
Accepted

## Context

Iteration 9 required adding video support to the Gemini Media CLI. This introduced several challenges:

1. **Metadata extraction**: Videos don't use EXIF; they use container-specific formats (MP4 atoms, MKV metadata)
2. **Large file handling**: The test video was 600MB, exceeding Gemini's 20MB inline data limit
3. **Unified architecture**: Need to support both images and videos in a single directory (Iteration 10)

## Decision

### 1. MediaMetadata Interface Pattern

Created a common `MediaMetadata` interface that both `ImageMetadata` and `VideoMetadata` implement:

```go
type MediaMetadata interface {
    FormatMetadataContext() string
    GetMediaType() string
    HasGPSData() bool
    GetGPS() (latitude, longitude float64)
    HasDateData() bool
    GetDate() time.Time
}
```

**Rationale**: Enables polymorphic handling in `LoadMediaFile()` and `BuildSocialMediaPrompt()`, making mixed media support straightforward for Iteration 10.

### 2. ffprobe for Video Metadata (External Tool)

Chose `ffprobe` (FFmpeg suite) over pure Go libraries for video metadata extraction.

| Option | GPS Support | Vendor Metadata | Maintenance |
|--------|-------------|-----------------|-------------|
| Pure Go (go-mp4) | ❌ Limited | ❌ | Lower |
| ffprobe | ✅ Full | ✅ Samsung, Apple, etc. | FFmpeg team |

**Rationale**: 
- ffprobe handles vendor-specific atoms (Samsung GPS, Android metadata)
- Well-documented JSON output format
- Already handles all container formats (MP4, MOV, MKV, AVI)
- Similar pattern to GPG integration in auth package

### 3. Files API for Large Videos

Implemented automatic detection and routing:

```
File Size ≤ 20MB → Inline Blob upload
File Size > 20MB → Files API with streaming upload
```

**Key implementation details**:
- Don't load large files into memory (`[]byte`)
- Stream upload using `os.Open()` + `UploadFile()`
- Wait loop for `PROCESSING` → `ACTIVE` state transition
- Auto-delete uploaded files after inference to manage quota

### 4. Metadata Extracted Locally, Not by Gemini

Gemini does not parse binary metadata from uploaded videos. It samples frames/audio for visual analysis. Therefore:

1. Extract metadata locally with ffprobe
2. Include metadata as text context in the prompt
3. Upload video for visual/audio analysis only

This is both more efficient and more reliable for GPS/timestamp data.

## Consequences

### Positive
- Clean separation between image and video handling
- Files API handles videos up to 2GB
- Automatic cleanup prevents quota exhaustion
- ffprobe provides comprehensive metadata including vendor extensions

### Negative
- External dependency on ffprobe (must be installed)
- Large video uploads take 2-4 minutes (upload + processing)
- Files API quota: 20GB total storage limit

## Implementation Files

- `internal/filehandler/handler.go`: MediaMetadata interface, ffprobe integration
- `internal/chat/chat.go`: Files API upload, processing wait loop
- `cmd/gemini-cli/main.go`: Updated workflow

## Related Decisions

- DDR-008: Pure Go EXIF Library (images use imagemeta, videos use ffprobe)
- DDR-009: Gemini Reverse Geocoding (GPS extracted locally, geocoding by Gemini)

## Date
December 31, 2025

