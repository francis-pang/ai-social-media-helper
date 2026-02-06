# DDR-021: Media Triage Command with Batch AI Evaluation

**Date**: 2026-02-06  
**Status**: Accepted  
**Iteration**: 12

## Context

The existing CLI (`media-select`) helps select the best media for an Instagram carousel from a directory. However, users often have directories containing many unusable photos and videos — accidental shots, extremely blurry images, pitch-black photos, sub-2-second video clips, and other media that no amount of light editing could salvage.

Before curating for social media, users need a way to clean up their media library by identifying and removing unsaveable files. Currently, this requires manually reviewing every file, which is tedious for directories with hundreds of items.

Additionally, the project has only one CLI command. As new commands are added, the codebase needs a multi-binary layout so each command can be built and run independently.

## Decision

### 1. Multi-Binary CLI Layout

Reorganize the `cmd/` directory from a single binary to multiple binaries:

```
cmd/
  media-select/       # Renamed from gemini-cli (existing selection functionality)
    main.go
  media-triage/       # New triage command
    main.go
```

Both binaries share the same `internal/` packages. Build commands become:
- `go build -o media-select ./cmd/media-select`
- `go build -o media-triage ./cmd/media-triage`

### 2. Batch Triage via Single API Call

Follow the same pattern as `AskMediaSelection()` (DDR-020) — send all media in a single `GenerateContent` call rather than per-item calls:

- Images: generate thumbnails (inline blobs)
- Videos: compress with AV1+Opus, upload via Files API (file references)
- Single prompt asks Gemini to evaluate every item and return a JSON array

### 3. Triage Criteria

**Photos are unsaveable if:**
- Too dark to recover meaningful content
- Too blurry (subject unrecognizable)
- Accidental/meaningless shot (pocket photo, floor, blurred motion)
- No discernible subject or meaning to a normal viewer
- Completely overexposed / washed out

**Videos are unsaveable if:**
- Duration less than 2 seconds (pre-filtered locally, no AI cost)
- Too blurry throughout
- No meaningful content (accidental recording, black screen)
- A normal viewer cannot understand what the video is about

**Items are saveable if:**
- With light editing (crop, brightness, contrast, sharpening), the result would be a decent photo/video
- The content is meaningful to a normal person viewing it

### 4. Structured JSON Response

Gemini returns a JSON array with one verdict per item:

```json
[
  {"media": 1, "filename": "IMG_001.jpg", "saveable": true, "reason": "Clear landscape, minor exposure fixable"},
  {"media": 2, "filename": "VID_003.mp4", "saveable": false, "reason": "Completely blurry, no recognizable subject"}
]
```

### 5. Interactive Deletion

After displaying the triage report (KEEP and DISCARD lists), the CLI prompts the user to confirm deletion of discarded files. No files are deleted without explicit confirmation.

### Key Design Choices

| Decision | Choice | Rationale |
|----------|--------|-----------|
| API call strategy | Single batch call | Faster and cheaper than per-item calls |
| Video pre-filter | Duration < 2s flagged locally | Saves API tokens for obviously bad clips |
| Response format | JSON array | Machine-parseable for reliable extraction |
| Deletion | Interactive confirmation | Safety — no accidental data loss |
| Binary layout | Separate binaries under cmd/ | Each command is independently buildable |
| Reference photo | Not included | Triage is about quality/meaning, not person identification |

## Rationale

### Why batch instead of per-item?

The existing `AskMediaSelection()` already proves this pattern works. A single API call with all media is:
- **Faster**: One round-trip instead of N
- **Cheaper**: One prompt overhead instead of N
- **Contextual**: Gemini can see all media at once for consistent judgment

### Why a separate binary instead of a Cobra subcommand?

Separate binaries under `cmd/` follow Go conventions for multi-tool repositories. Each binary has a clear, focused purpose and can be built/deployed independently. The shared `internal/` packages provide code reuse without coupling the CLIs.

### Why pre-filter short videos locally?

Videos under 2 seconds are almost always accidental recordings (tapping record by mistake). Checking `VideoMetadata.Duration` locally avoids compressing, uploading, and paying for AI analysis of obviously unusable clips.

### Why no reference photo?

The existing `media-select` command includes a reference photo of Francis for person identification in social media selection. Triage does not need person identification — it only evaluates whether media is saveable based on quality and meaningfulness.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Per-item API calls | Much slower and more expensive for large directories |
| Cobra subcommands in single binary | Less flexible for deployment, tighter coupling |
| Auto-delete without confirmation | Too risky — accidental data loss |
| Quality scoring (1-10) instead of binary | Overcomplicates the decision; saveable/unsaveable is clearer |
| Move to trash folder instead of delete | Adds complexity; user can undo via OS trash if needed |

## Consequences

**Positive:**
- Fast, cheap batch evaluation of entire media directories
- Automated cleanup of obviously bad media before manual curation
- Clear multi-binary layout supports future CLI commands
- Reuses existing infrastructure (scan, thumbnail, compress, upload)

**Trade-offs:**
- Gemini's context window limits the batch size (very large directories may need chunking in future)
- JSON parsing adds a failure mode if Gemini returns malformed output
- Deleting files is destructive (mitigated by interactive confirmation)

## Implementation

### New Files

| File | Purpose |
|------|---------|
| `cmd/media-triage/main.go` | CLI entry point with flags, scan, report, deletion |
| `internal/chat/triage.go` | `AskMediaTriage()` — batch evaluation and JSON parsing |
| `internal/assets/prompts/triage-system.txt` | System instruction for triage evaluation |

### Modified Files

| File | Changes |
|------|---------|
| `cmd/gemini-cli/` | Renamed to `cmd/media-select/`, updated Cobra `Use` field |
| `internal/assets/prompts.go` | Added embed directive for triage prompt |

### Shared Code Reused

| Package | Functions Used |
|---------|----------------|
| `internal/filehandler` | `ScanDirectoryMediaWithOptions()`, `GenerateThumbnail()`, `CompressVideoForGemini()` |
| `internal/auth` | `GetAPIKey()`, `ValidateAPIKey()` |
| `internal/chat` | `GetModelName()`, model constants |
| `internal/logging` | `Init()` |

## Related Decisions

- DDR-014: Thumbnail-Based Multi-Image Selection Strategy
- DDR-016: Quality-Agnostic Metadata-Driven Photo Selection
- DDR-018: Video Compression for Gemini 3 Pro Optimization
- DDR-019: Externalized Prompt Templates
- DDR-020: Mixed Media Selection Strategy

## Testing Approach

1. **Unit tests**: JSON response parsing with valid/malformed inputs
2. **Integration tests**: Mixed media batch triage
3. **Manual testing**: Real directories with known good/bad media
