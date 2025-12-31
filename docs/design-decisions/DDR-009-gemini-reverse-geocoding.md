# DDR-009: Gemini Native Reverse Geocoding

**Date**: 2025-12-31  
**Status**: Accepted  
**Iteration**: 7

## Context

After extracting GPS coordinates from image EXIF metadata (DDR-007), we needed to convert these coordinates into human-readable location information (city, venue name, etc.). This process is called reverse geocoding.

## Decision

Instruct Gemini to use its native Google Maps integration for reverse geocoding rather than calling a separate geocoding API.

## Rationale

- **Simplicity**: No additional API keys or service calls required
- **Integration**: Gemini has native access to Google Maps data
- **Context-aware**: Can correlate location with visual content in the image
- **Cost-effective**: Included in Gemini API call, no separate billing
- **Richer context**: Gemini can provide cultural/historical significance, not just address

## Prompt Design

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

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Google Maps Geocoding API | Requires separate API key; adds complexity |
| OpenStreetMap Nominatim | Additional HTTP call; rate limits |
| Local geocoding database | Large data files; limited accuracy |
| Skip reverse geocoding | Loses valuable location context |

## Consequences

- **Positive**: Simple implementation with no additional dependencies
- **Positive**: Rich contextual information (not just address)
- **Trade-off**: Dependent on Gemini's Google Maps access working correctly
- **Trade-off**: Results may vary between API calls

## Future Enhancement

Pre-resolve coordinates using Google Maps Geocoding API before sending to Gemini for more reliable and consistent results. This would provide:
- Cached/consistent location names
- Fallback if Gemini's geocoding fails
- Richer structured data (place_id, formatted_address, etc.)

This enhancement is documented in the codebase as a TODO for future implementation.

