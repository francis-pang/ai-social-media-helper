# DDR-020: Mixed Media Selection Strategy

**Date**: 2026-01-01  
**Status**: Accepted  
**Iteration**: 11

## Context

The current photo selection system (DDR-016) only scans directories for images, excluding videos. Modern smartphone photo libraries typically contain a mix of photos and videos from the same events, and Instagram carousels support up to 20 media items of any combination.

When selecting media for an Instagram post, videos and photos should compete equally—a compelling 15-second video may better represent a moment than multiple similar photos.

## Decision

Extend the directory scanning and selection system to handle mixed media directories, where photos and videos are ranked equally in a unified selection process.

### Key Design Choices

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Video presentation | Compress + upload full video | Videos provide temporal context that thumbnails cannot |
| Selection output | Unified ranked list | Photos and videos compete equally |
| Instagram constraint | Any combination up to 20 items | Instagram allows mixed carousels |
| Compression | Always compress all videos | Consistent token costs (DDR-018) |
| Temp files | System temp dir (`/tmp`) | Auto-cleaned by OS |
| Video weighting | Equal weight with photos | Best media wins regardless of type |
| Audio analysis | Included in selection prompt | Audio adds context value |
| Files API | Always use for videos | Required for video uploads |

### Unified Selection Strategy

1. **Scan directory for all supported media** (images + videos)
2. **For images**: Generate thumbnails (existing behavior)
3. **For videos**: Compress with AV1+Opus (DDR-018) and upload to Files API
4. **Send all media to Gemini** with unified selection prompt
5. **Receive ranked list** of up to 20 items (any combination)
6. **Clean up**: Delete temp compressed files and uploaded Gemini files

### Selection Prompt Updates

The selection prompt is updated to:
- Reference "media items" instead of "photos"
- Include video metadata (duration, resolution, has audio)
- Add audio analysis guidance for videos
- Clarify that videos are compressed previews

### Output Format Updates

```
RANK | ITEM | TYPE | SCENE | JUSTIFICATION
-----|------|------|-------|---------------
1    | Media 1: sunset.jpg | Photo | Beach | Golden hour lighting
2    | Media 2: waves.mp4 | Video | Beach | Captures wave motion, ambient sounds
3    | Media 3: dinner.jpg | Photo | Restaurant | Food presentation
...
```

## Rationale

### Why unified ranking?

A single ranked list ensures the best 20 media items are selected regardless of type. Separate photo/video quotas could exclude better content.

### Why upload full videos (not thumbnails)?

Videos contain temporal information (motion, transitions, audio) that static thumbnails cannot capture. Gemini 3.1 Pro's native video understanding can evaluate the full content.

### Why equal weighting?

No type is inherently more "Instagram-worthy." A perfectly timed photo can be better than a mediocre video, and vice versa. Let Gemini evaluate based on content quality and storytelling value.

### Why include audio analysis?

Videos with music, speech, or interesting ambient sounds may be more engaging than silent videos. Audio context helps selection decisions.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Separate video selection | Doesn't allow fair competition with photos |
| Video thumbnails only | Loses temporal/audio context |
| Higher weight for videos | No objective reason videos are better |
| Exclude videos from selection | Misses potential best content |
| User-specified video quota | Adds complexity without clear benefit |

## Consequences

**Positive:**
- Complete media coverage from mixed directories
- Fair comparison between photos and videos
- Better Instagram carousel representation
- Audio context influences selection

**Trade-offs:**
- Longer processing time (video compression + upload)
- Higher Files API usage during selection
- More complex cleanup requirements
- FFmpeg dependency required for video support

## Implementation

### Modified Files

| File | Changes |
|------|---------|
| `internal/filehandler/directory.go` | Add `ScanDirectoryMedia()` for mixed scanning |
| `internal/chat/selection.go` | Rename to `AskMediaSelection()`, handle video upload |
| `internal/chat/chat.go` | Expose model parameter for selection |
| `cmd/gemini-cli/main.go` | Add `--model` flag, update UI for mixed media |

### Directory Scanning

```go
// ScanDirectoryMedia scans for both images AND videos
func ScanDirectoryMedia(dirPath string) ([]*MediaFile, error) {
    return ScanDirectoryMediaWithOptions(dirPath, ScanOptions{})
}

func ScanDirectoryMediaWithOptions(dirPath string, opts ScanOptions) ([]*MediaFile, error) {
    // Uses IsSupported(ext) instead of IsImage(ext)
    // Processes both image and video files
}
```

### Video Upload in Selection

```go
func AskMediaSelection(ctx context.Context, client *genai.Client, files []*MediaFile, ...) {
    var uploadedFiles []*genai.File  // Track for cleanup
    var cleanupFuncs []func()        // Track temp file cleanup
    
    defer func() {
        // Cleanup temp compressed files
        for _, cleanup := range cleanupFuncs {
            cleanup()
        }
        // Delete uploaded Gemini files
        for _, f := range uploadedFiles {
            client.DeleteFile(ctx, f.Name)
        }
    }()
    
    for _, file := range files {
        if IsImage(ext) {
            // Generate thumbnail (existing)
        } else if IsVideo(ext) {
            // Compress → Upload → Add file reference
        }
    }
}
```

## Related Decisions

- DDR-016: Quality-Agnostic Metadata-Driven Photo Selection
- DDR-017: Francis Reference Photo for Person Identification
- DDR-018: Video Compression for Gemini 3.1 Pro Optimization
- DDR-019: Externalized Prompt Templates

## Testing Approach

1. **Unit tests**: Mixed-media scanning with mock files
2. **Integration tests**: 
   - Image-only directories (regression)
   - Video-only directories
   - Mixed directories
   - Cleanup verification (temp files + Gemini files)
3. **Manual testing**: Real mixed media directory
