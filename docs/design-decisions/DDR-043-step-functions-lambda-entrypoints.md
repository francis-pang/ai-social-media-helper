# DDR-043: Step Functions Lambda Entrypoints

**Date**: 2026-02-09  
**Status**: Accepted  
**Iteration**: Phase 2 Cloud Deployment

## Context

DDR-035 defines a multi-Lambda architecture with five specialized functions. The API Lambda (`cmd/media-lambda`) already exists and handles all HTTP requests via API Gateway. The four processing Lambdas (Thumbnail, Selection, Enhancement, Video) do not yet exist as code. They are invoked by Step Functions — not by API Gateway — so they use a fundamentally different handler model: direct JSON event invocation instead of HTTP request/response.

The processing logic (thumbnail generation, Gemini selection, photo enhancement, video enhancement) already exists inside `cmd/media-lambda/selection_run.go` and `cmd/media-lambda/enhancement_run.go`, where it runs in background goroutines. This code needs to be extracted into standalone Lambda entrypoints that:

1. Receive a typed JSON event from Step Functions
2. Process exactly one item (or one batch for Selection)
3. Return a typed JSON result (or an error for Step Functions retry)
4. Read/write to S3 and DynamoDB using shared `internal/` packages

## Decision

Create four new Lambda entrypoints under `cmd/`, each as a thin wrapper around existing `internal/` packages. Each Lambda uses `lambda.Start(handler)` with a typed handler function signature `func(ctx, Event) (Result, error)` — the standard AWS Lambda Go SDK pattern for direct invocation.

### 1. Thumbnail Lambda (`cmd/thumbnail-lambda/main.go`)

**Invoked by**: Step Functions SelectionPipeline → Map state (one invocation per media file)

| Property | Value |
|----------|-------|
| Input | `{sessionId, key, bucket}` |
| Output | `{thumbnailKey, originalKey, success, error}` |
| Container | Heavy (needs ffmpeg for video frame extraction) |
| Memory | 512 MB |
| Timeout | 2 minutes |
| AWS Clients | S3 |
| Gemini | Not needed |
| DynamoDB | Not needed |

**Processing**: Downloads the media file from S3. For images, generates a 400px JPEG thumbnail using `filehandler.GenerateThumbnail()`. For videos, extracts a frame at 1 second using ffmpeg via `filehandler.GenerateVideoThumbnail()`. Uploads the thumbnail to `{sessionId}/thumbnails/{baseName}.jpg` in S3.

**Error handling**: Returns an error to Step Functions. The Map state has a retry policy (2 attempts with exponential backoff). If a single thumbnail fails after retries, the Map state continues processing remaining files (tolerated failure).

### 2. Selection Lambda (`cmd/selection-lambda/main.go`)

**Invoked by**: Step Functions SelectionPipeline → after all thumbnails complete (fan-in)

| Property | Value |
|----------|-------|
| Input | `{sessionId, jobId, tripContext, model, mediaKeys[], thumbnailKeys[], bucket}` |
| Output | `{selectedCount, excludedCount, sceneGroupCount}` |
| Container | Heavy (needs ffmpeg for video compression before Gemini upload) |
| Memory | 4 GB |
| Timeout | 15 minutes |
| AWS Clients | S3, DynamoDB |
| Gemini | Yes (selection analysis) |

**Processing**: Downloads all thumbnails and original media files from S3. Loads each as a `filehandler.MediaFile` for metadata extraction. Calls `chat.AskMediaSelectionJSON()` with all media + trip context. Parses the structured JSON response, maps results to S3 keys and thumbnail URLs, and writes the complete `SelectionJob` to DynamoDB via `store.PutSelectionJob()`.

**Error handling**: Returns an error to Step Functions. The state has a retry policy (1 attempt). On permanent failure, the API Lambda returns the error to the frontend via DynamoDB status polling.

### 3. Enhancement Lambda (`cmd/enhance-lambda/main.go`)

**Invoked by**: Step Functions EnhancementPipeline → Map state (one invocation per photo)

| Property | Value |
|----------|-------|
| Input | `{sessionId, jobId, key, itemIndex, bucket}` |
| Output | `{enhancedKey, enhancedThumbKey, phase, phase1Text, imagenEdits}` |
| Container | Light (no ffmpeg needed for photo enhancement) |
| Memory | 2 GB |
| Timeout | 5 minutes |
| AWS Clients | S3, DynamoDB |
| Gemini | Yes (image editing) |

**Processing**: Downloads one photo from S3. Determines MIME type and dimensions. Calls `chat.RunFullEnhancement()` for the multi-phase enhancement pipeline (Gemini creative edit → quality analysis → Imagen surgical edit). Uploads the enhanced image to `{sessionId}/enhanced/{filename}` and a 400px thumbnail to `{sessionId}/thumbnails/enhanced-{baseName}.jpg`. Updates the corresponding item in the `EnhancementJob` DynamoDB record.

**Error handling**: Returns an error to Step Functions. The Map state has a retry policy (2 attempts with exponential backoff). If one photo fails, others continue (partial success). The DynamoDB item is updated with `phase: "error"` and the error message.

### 4. Video Processing Lambda (`cmd/video-lambda/main.go`)

**Invoked by**: Step Functions EnhancementPipeline → Map state (one invocation per video)

| Property | Value |
|----------|-------|
| Input | `{sessionId, jobId, key, itemIndex, bucket}` |
| Output | `{enhancedKey, phase}` |
| Container | Heavy (needs ffmpeg for video processing) |
| Memory | 4 GB |
| Timeout | 15 minutes |
| AWS Clients | S3, DynamoDB |
| Gemini | Yes (video analysis for ffmpeg parameter recommendations) |

**Processing**: Downloads one video from S3. Compresses a preview and sends to Gemini for analysis. Gemini returns structured enhancement recommendations (brightness, contrast, stabilization, denoising parameters). Applies ffmpeg filters based on recommendations. Uploads the enhanced video to `{sessionId}/enhanced/{filename}`. Updates the corresponding item in the `EnhancementJob` DynamoDB record.

**Error handling**: Returns an error to Step Functions. The Map state has a retry policy (1 attempt). Lambda's 15-minute timeout is the hard limit — videos exceeding this duration should be flagged as too large.

### Common Initialization Pattern

All four Lambdas share the same `init()` pattern:

```go
func init() {
    logging.Init()
    cfg, _ := awsconfig.LoadDefaultConfig(context.Background())
    s3Client = s3.NewFromConfig(cfg)
    mediaBucket = os.Getenv("MEDIA_BUCKET_NAME")
}
```

Lambdas requiring Gemini also load the API key from SSM at cold start. Lambdas requiring DynamoDB initialize the `store.DynamoStore` with the table name from `DYNAMO_TABLE_NAME`.

### Why Direct Invocation (Not HTTP)

The processing Lambdas are invoked by Step Functions, not API Gateway. Step Functions uses `lambda:InvokeFunction` to call them directly with a JSON payload. Using the HTTP adapter (`aws-lambda-go-api-proxy`) would add unnecessary overhead: JSON → HTTP request → router → handler → HTTP response → JSON. Direct invocation is simpler (JSON → handler → JSON) and more idiomatic for Step Functions.

## Rationale

- **Thin entrypoints**: Each Lambda's `main.go` is a thin wrapper (~100-200 lines) that delegates to shared `internal/` packages. Business logic stays in `internal/chat`, `internal/filehandler`, and `internal/store`.
- **Typed events**: Go structs for input/output enable compile-time checking and clear documentation of the Step Functions contract.
- **Single-item processing**: Thumbnail and Enhancement Lambdas process exactly one file per invocation. This enables Step Functions Map state to fan out across items with per-item retry, concurrency throttling, and partial failure tolerance.
- **Batch processing**: Selection Lambda processes all thumbnails in one invocation because Gemini needs to see all media together for comparative selection decisions.
- **DynamoDB for inter-Lambda state**: Processing Lambdas write results to DynamoDB. The API Lambda reads DynamoDB for status polling. This replaces in-memory job maps that don't survive across Lambda containers.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| HTTP-based processing Lambdas (behind API Gateway) | API Gateway has a 30-second timeout; processing can take 15 minutes; unnecessary HTTP overhead for Step Functions |
| Single processing Lambda with event routing | Defeats the purpose of per-Lambda memory/timeout tuning; a 256 MB API request doesn't need 4 GB |
| Processing logic inline in `cmd/` entrypoints | Duplicates code between CLI tools, API Lambda, and processing Lambdas; shared `internal/` packages solve this |
| Lambda Layers for shared code | Go compiles to a single static binary; layers don't reduce binary size; each Lambda already has exactly one binary |

## Consequences

**Positive:**

- Processing logic is shared via `internal/` packages — no code duplication between API Lambda and processing Lambdas
- Each Lambda has a clear, typed input/output contract documented in the event structs
- Step Functions handles retry, concurrency throttling, and partial failure automatically
- Adding a new processing step requires only a new `cmd/` directory + Step Functions state
- Processing Lambdas are testable in isolation: pass an event struct, assert on the result

**Trade-offs:**

- Four new `cmd/` directories to maintain (though each is ~100-200 lines)
- Processing Lambdas cannot be tested via HTTP (no API Gateway); requires direct invocation or Step Functions execution
- Cold start latency for processing Lambdas (~1-3 seconds for 512 MB-4 GB containers); mitigated by Step Functions retry and the inherent latency tolerance of background processing

## Implementation

| File | Purpose |
|------|---------|
| `cmd/thumbnail-lambda/main.go` | Per-file thumbnail generation handler |
| `cmd/selection-lambda/main.go` | Gemini AI selection handler (batch) |
| `cmd/enhance-lambda/main.go` | Per-photo Gemini image editing handler |
| `cmd/video-lambda/main.go` | Per-video ffmpeg enhancement handler |

Each Lambda follows the pattern:
1. `init()` — AWS client setup, API key loading
2. `main()` — `lambda.Start(handler)`
3. `handler(ctx, event) (result, error)` — download, process, upload, return

## Related Decisions

- [DDR-035](./DDR-035-multi-lambda-deployment.md): Multi-Lambda Deployment Architecture — defines the 5-Lambda split, container images, and Step Functions state machines
- [DDR-039](./DDR-039-dynamodb-session-store.md): DynamoDB SessionStore — shared state between API Lambda and processing Lambdas
- [DDR-030](./DDR-030-cloud-selection-backend.md): Cloud Selection Backend — selection processing logic being extracted
- [DDR-031](./DDR-031-multi-step-photo-enhancement.md): Photo Enhancement Pipeline — enhancement logic being extracted
- [DDR-032](./DDR-032-multi-step-video-enhancement.md): Video Enhancement Pipeline — video processing logic being extracted
