# Media Analysis Design

This document describes the design decisions and implementation details for the media analysis feature (Iteration 7+).

---

## Overview

The media analysis feature enables users to upload images to Gemini and receive comprehensive analysis including:
- Visual content description
- EXIF metadata extraction (GPS, timestamp, camera info)
- Reverse geocoding via Gemini's Google Maps integration
- Social media content generation

---

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        Media Analysis Flow                       │
└─────────────────────────────────────────────────────────────────┘

┌──────────────┐     ┌──────────────┐     ┌──────────────────────┐
│  Image File  │────▶│ File Handler │────▶│ EXIF Metadata        │
│  (HEIC/JPEG) │     │              │     │ - GPS Coordinates    │
└──────────────┘     └──────────────┘     │ - Date/Time          │
                                          │ - Camera Info        │
                                          └──────────┬───────────┘
                                                     │
                     ┌───────────────────────────────┴───────────┐
                     │                                           │
                     ▼                                           ▼
           ┌─────────────────┐                    ┌─────────────────────┐
           │ Image Binary    │                    │ Metadata Context    │
           │ (genai.Blob)    │                    │ (Formatted Text)    │
           └────────┬────────┘                    └──────────┬──────────┘
                    │                                        │
                    └────────────────┬───────────────────────┘
                                     │
                                     ▼
                          ┌─────────────────────┐
                          │   Gemini API Call   │
                          │ - Image + Prompt    │
                          │ - Reverse Geocoding │
                          │ - Content Analysis  │
                          └──────────┬──────────┘
                                     │
                                     ▼
                          ┌─────────────────────┐
                          │  Structured Output  │
                          │ - Location Details  │
                          │ - Visual Analysis   │
                          │ - Social Media Post │
                          └─────────────────────┘
```

---

## Design Decisions

### 1. Hybrid Prompt Strategy for EXIF Metadata

**Decision**: Extract EXIF metadata locally using a Go library and pass it as text alongside the image.

**Rationale**:
- **Gemini cannot reliably parse EXIF**: AI models process pixel data, not binary file headers
- **Preprocessing strips metadata**: Image resizing/compression often discards EXIF
- **Token efficiency**: Structured text is more efficient than raw binary
- **Technical accuracy**: Precise GPS coordinates and timestamps require actual data, not visual estimation

**Implementation**:
```go
// 1. Load image and extract EXIF
mediaFile, _ := filehandler.LoadMediaFile(imagePath)

// 2. Format metadata as text context
metadataContext := mediaFile.Metadata.FormatMetadataContext()

// 3. Build prompt with metadata
prompt := chat.BuildSocialMediaImagePrompt(metadataContext)

// 4. Send image + prompt to Gemini
response, _ := chat.AskImageQuestion(ctx, client, mediaFile, prompt)
```

---

### 2. Pure Go EXIF Library

**Decision**: Use `github.com/evanoberholster/imagemeta` instead of external tools like `exiftool`.

**Rationale**:
- **No external dependencies**: Works without system tools installed
- **Cross-platform**: Same binary works on macOS, Linux, Windows
- **Faster execution**: No subprocess spawning overhead
- **HEIC/HEIF support**: Native support for modern Apple image formats
- **Timezone-aware**: Properly parses date/time with timezone information

**Library Comparison**:

| Library | HEIC Support | External Deps | Performance |
|---------|--------------|---------------|-------------|
| `evanoberholster/imagemeta` | ✅ Yes | None | Fast |
| `rwcarlsen/goexif` | ❌ No | None | Fast |
| `exiftool` (CLI) | ✅ Yes | System binary | Slower |
| `dsoprea/go-exif` | ❌ No | None | Fast |

**Trade-offs**:
- Library may not support all exotic camera formats
- Falls back gracefully if metadata cannot be extracted

---

### 3. Gemini Native Reverse Geocoding

**Decision**: Instruct Gemini to use its Google Maps integration for reverse geocoding rather than calling a separate geocoding API.

**Rationale**:
- **Simplicity**: No additional API keys or service calls required
- **Integration**: Gemini has native access to Google Maps data
- **Context-aware**: Can correlate location with visual content
- **Cost-effective**: Included in Gemini API call, no separate billing

**Prompt Design**:
```
### 1. Reverse Geocoding (REQUIRED - Use Google Maps Integration)
You have native access to Google Maps. Use it to perform reverse geocoding on the provided GPS coordinates.

**For the GPS coordinates provided, look up and report:**
- **Exact Place Name**: The specific venue, business, landmark
- **Street Address**: The full street address
- **City**: City or town name
- **State/Region**: State, province, or region
- **Country**: Country name
- **Place Type**: Category (e.g., restaurant, park, stadium)
- **Known For**: Historical or cultural significance
- **Nearby Landmarks**: Other notable places nearby
```

**Future Enhancement**: Pre-resolve coordinates using Google Maps Geocoding API before sending to Gemini for more reliable results.

---

### 4. Supported Image Formats

**Decision**: Support HEIC/HEIF in addition to standard web formats.

**Rationale**:
- **iPhone photos**: HEIC is the default format on modern iPhones
- **Quality preservation**: HEIC maintains better quality at smaller file sizes
- **User convenience**: No need to convert before uploading

**Supported Formats**:

| Extension | MIME Type | Common Source |
|-----------|-----------|---------------|
| `.jpg`, `.jpeg` | `image/jpeg` | Most cameras, web |
| `.png` | `image/png` | Screenshots, graphics |
| `.gif` | `image/gif` | Animated images |
| `.webp` | `image/webp` | Modern web format |
| `.heic` | `image/heic` | iPhone (iOS 11+) |
| `.heif` | `image/heif` | HEIC variant |

---

### 5. Structured Prompt for Social Media Content

**Decision**: Use a structured, multi-section prompt to guide Gemini's response format.

**Rationale**:
- **Predictable output**: Consistent structure across different images
- **Comprehensive coverage**: Ensures all aspects are addressed
- **User personalization**: Prompt includes context about the user (name: Francis)
- **Actionable content**: Generates ready-to-use captions and hashtags

**Prompt Sections**:

1. **Reverse Geocoding**: Location details from GPS coordinates
2. **Temporal Analysis**: Time/date context and significance
3. **Visual Analysis**: Description of image content
4. **Social Media Generation**: Captions (3 variations) + hashtags

---

### 6. MediaFile Structure

**Decision**: Bundle file data with extracted metadata in a single struct.

**Rationale**:
- **Cohesion**: Related data stays together
- **Convenience**: Single object passed through the pipeline
- **Optional metadata**: Gracefully handles missing EXIF data

**Data Structure**:
```go
type MediaFile struct {
    Path     string          // Original file path
    MIMEType string          // Detected MIME type
    Data     []byte          // File contents for upload
    Size     int64           // File size in bytes
    Metadata *ImageMetadata  // Extracted EXIF (may be nil)
}

type ImageMetadata struct {
    Latitude    float64
    Longitude   float64
    HasGPS      bool
    DateTaken   time.Time
    HasDate     bool
    CameraMake  string
    CameraModel string
    RawFields   map[string]string  // For debugging
}
```

---

## Implementation Details

### File Handler Package

**Location**: `internal/filehandler/handler.go`

**Functions**:

| Function | Purpose |
|----------|---------|
| `LoadMediaFile(path)` | Load file, detect MIME type, extract metadata |
| `ExtractMetadata(path)` | Parse EXIF using imagemeta library |
| `GetMIMEType(ext)` | Map file extension to MIME type |
| `IsImage(ext)` | Check if extension is a supported image |
| `IsVideo(ext)` | Check if extension is a supported video |
| `FormatMetadataContext()` | Convert metadata to prompt-ready text |
| `CoordinatesToDMS(lat, lon)` | Convert decimal degrees to DMS format |

### Chat Package Extensions

**Location**: `internal/chat/chat.go`

**New Functions**:

| Function | Purpose |
|----------|---------|
| `AskImageQuestion(ctx, client, mediaFile, prompt)` | Send image + prompt to Gemini |
| `BuildSocialMediaImagePrompt(metadataContext)` | Construct the analysis prompt |

---

## Metadata Context Format

The extracted EXIF metadata is formatted as follows before inclusion in the prompt:

```
## EXTRACTED EXIF METADATA

**GPS Coordinates:**
- Latitude: 38.048868
- Longitude: -84.608130
- Google Maps: https://www.google.com/maps?q=38.048868,-84.608130

**Date/Time Taken:**
- Date: Tuesday, December 30, 2025
- Time: 9:21 AM
- Day of Week: Tuesday

**Camera:** samsung Galaxy Z Flip7
```

---

## Error Handling

| Scenario | Behavior |
|----------|----------|
| File not found | Fatal error with clear message |
| Unsupported format | Error listing supported extensions |
| EXIF extraction fails | Warning logged, continues without metadata |
| No GPS in EXIF | Prompt notes "GPS not available" |
| Gemini API error | Fatal error with typed classification |

---

## Future Enhancements

### 1. Pre-resolved Geocoding

Call Google Maps Geocoding API before Gemini to include resolved address in prompt:

```go
// Future implementation
type ResolvedLocation struct {
    PlaceName     string
    StreetAddress string
    City          string
    State         string
    Country       string
    PlaceType     string
}

func ResolveCoordinates(lat, lon float64) (*ResolvedLocation, error) {
    // Call Google Maps Geocoding API
    // Return structured location data
}
```

### 2. Video Support (Iteration 9) ✅

Extend `MediaFile` to handle video uploads:
- Support MP4, MOV, AVI, WebM, MKV formats
- Extract video metadata (duration, resolution, codec)
- Handle larger file uploads with Files API

### 3. Batch Processing (Iteration 8) ✅

Process multiple images from a directory:
- Recursive directory scanning
- Thumbnail generation for selection
- Metadata extraction for all images

### 4. Quality-Agnostic Photo Selection (Iteration 10) ✅

**Decision**: Photo quality is NOT a selection criterion since user has Google's enhancement tools (Magic Editor, Unblur, Portrait Light, etc.).

See [DDR-016: Quality-Agnostic Photo Selection](./design-decisions/DDR-016-quality-agnostic-photo-selection.md)

**Selection Priorities**:
1. Subject/Scene Diversity (Highest): food, architecture, landscape, people, activities
2. Scene Representation: ensure each sub-event/location is represented
3. Enhancement Potential (Duplicates Only): pick photo requiring least enhancement
4. People Variety (Lower): different groups/individuals
5. Time of Day (Tiebreaker): only to break ties

**Scene Detection (Hybrid)**:
- Visual similarity + time gaps (2+ hours) + GPS gaps (1km+)
- Gemini uses GPS for reverse geocoding to identify venues

**User Context**:
- Trip description provided via `--context` / `-c` flag
- Helps Gemini understand sub-events and priorities

**Output Format** (Three-part):
1. Ranked list with justification
2. Scene grouping explanation
3. Detailed exclusion report for every non-selected photo

---

**Last Updated**: 2025-12-31

