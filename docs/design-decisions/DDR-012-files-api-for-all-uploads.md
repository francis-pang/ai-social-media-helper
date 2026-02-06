# DDR-012: Files API for All Media Uploads

## Status
Accepted

## Context

With the release of **Gemini 3 Flash** (successor to Gemini 2.0 and 1.5 Flash), the Files API has become the recommended approach for all media uploads, not just large files. This decision documents the shift from a hybrid inline/Files API approach to Files API-only uploads.

### Previous Approach (DDR-011)
- Files ≤20MB: Inline blob upload
- Files >20MB: Files API upload

### New Approach
- All media files: Files API upload

## Decision

**Use the Files API for all media uploads, regardless of file size.**

### Rationale

1. **Consistency**: Single code path for all media uploads simplifies maintenance
2. **Cost Efficiency**: Files API upload is free (48-hour storage, up to 2GB per file)
3. **Future-Proofing**: Files API is the canonical approach for Gemini 3+
4. **Streaming**: Files API streams from disk; inline requires loading entire file into memory

### Gemini 3 Flash Pricing

| Action | Cost (Pay-As-You-Go) |
|--------|----------------------|
| **File Upload** | **$0.00** (free for 48 hours, up to 2GB) |
| **Input Tokens** | $0.10 per 1M tokens |
| **Output Tokens** | $0.40 per 1M tokens |

### Video Tokenization Rate

Gemini 3 Flash tokenizes video at a fixed rate of **263 tokens per second**:

| Video Length | Tokens | Approximate Cost |
|--------------|--------|------------------|
| 30 seconds | 7,890 | ~$0.001 |
| 5 minutes | 78,900 | ~$0.008 |
| 10 minutes | 157,800 | ~$0.016 |

**Audio** is tokenized simultaneously at **32 tokens per second**.

### Key Gemini 3 Flash Capabilities

1. **Native Multimodal Architecture**: Optimized for "long-context" multimodal reasoning
2. **1M+ Token Context Window**: Can maintain focus across very long videos
3. **Native Audio Analysis**: Correlates visual events with audio content
4. **System Instructions**: Metadata can be passed as ground truth context

## Implementation

### Workflow

```
1. Extract    → ffprobe/exiftool for metadata (local, instant)
2. Upload     → Files API streaming upload (free)
3. Wait       → Poll for PROCESSING → ACTIVE state
4. Inference  → Generate content with file URI + metadata prompt
5. Cleanup    → Delete file from Gemini storage
```

### System Instruction Pattern

For optimal results, pass extracted metadata as a System Instruction:

```go
model := client.GenerativeModel("gemini-3-flash")
model.SystemInstruction = &genai.Content{
    Parts: []genai.Part{
        genai.Text(`You are an expert media analyst. Use the provided 
        EXIF/FFmpeg metadata as the absolute ground truth for time, 
        location, and camera settings while describing the visual content.`),
    },
}
```

### Code Changes Required

Update `internal/chat/chat.go`:

```go
// Remove inline blob path - always use Files API
func AskMediaQuestion(ctx context.Context, client *genai.Client, 
    mediaFile *filehandler.MediaFile, question string) (string, error) {
    
    // Always use Files API for consistency
    file, err := uploadAndWaitForProcessing(ctx, client, mediaFile)
    if err != nil {
        return "", fmt.Errorf("failed to upload file: %w", err)
    }
    defer deleteFile(ctx, client, file)
    
    // Use FileData part
    parts := []genai.Part{
        genai.FileData{MIMEType: file.MIMEType, URI: file.URI},
        genai.Text(question),
    }
    
    return generateContent(ctx, client, parts)
}
```

## Consequences

### Positive
- Simplified codebase (single upload path)
- Memory efficient (no loading files into RAM)
- Consistent behavior across all file sizes
- Free storage for 48 hours
- Better integration with Gemini 3 architecture

### Negative
- Slightly higher latency for small files (upload + processing vs inline)
- Requires network even for small files
- Must manage 20GB storage quota across files

### Quota Management

| Limit | Value |
|-------|-------|
| Max file size | 2GB |
| Total storage | 20GB |
| Storage duration | 48 hours (auto-delete) |

**Recommendation**: Always delete files after inference to maintain quota headroom.

## Related Decisions

- DDR-011: Video Metadata Extraction and Large File Upload (superseded for upload approach)
- DDR-008: Pure Go EXIF Library (still used for images)

## References

- [Gemini Files API Documentation](https://ai.google.dev/gemini-api/docs/vision)
- [Gemini 3 Flash Pricing](https://ai.google.dev/pricing)

## Date
December 31, 2025

