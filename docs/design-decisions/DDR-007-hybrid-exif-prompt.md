# DDR-007: Hybrid Prompt Strategy for EXIF Metadata

**Date**: 2025-12-31  
**Status**: Accepted  
**Iteration**: 7

## Context

When analyzing images with Gemini, we wanted to include EXIF metadata (GPS coordinates, timestamp, camera info) in the analysis. The question was whether to rely on Gemini to extract this metadata from the image or to extract it ourselves.

## Decision

Extract EXIF metadata locally using a Go library and pass it as formatted text alongside the image in the prompt.

## Rationale

- **Gemini cannot reliably parse EXIF**: AI models process pixel data, not binary file headers
- **Preprocessing strips metadata**: Image resizing/compression during API processing often discards EXIF
- **Token efficiency**: Structured text is more efficient than raw binary data
- **Technical accuracy**: Precise GPS coordinates and timestamps require actual data, not visual estimation

## Implementation Flow

```
1. Load image with filehandler.LoadMediaFile()
2. Extract EXIF using imagemeta library
3. Format metadata as text context
4. Include metadata text in prompt alongside image
5. Send image blob + prompt text to Gemini
```

## Metadata Context Format

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

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Ask Gemini to extract EXIF | Cannot reliably parse binary metadata from images |
| Send raw EXIF as JSON | Token-inefficient; model may not understand format |
| Skip metadata entirely | Loses valuable context for accurate analysis |

## Consequences

- **Positive**: Accurate GPS and timestamp data in analysis
- **Positive**: Gemini can use real coordinates for reverse geocoding
- **Trade-off**: Requires local EXIF extraction library
- **Trade-off**: Slightly larger prompts due to metadata text

## Related Documents

- [DDR-008](./DDR-008-pure-go-exif-library.md) - EXIF library selection
- [media_analysis.md](../media_analysis.md) - Full media analysis design

