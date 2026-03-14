# DDR-086: Vertex AI Batch JSONL — generationConfig Schema Fix

**Date**: 2026-03-14  
**Status**: Accepted  
**Iteration**: Cloud — FB Prep economy mode batch prediction fix

## Context

### Problem: Vertex AI Batch Jobs Fail with JSONL Parse Error

All economy-mode batch workflows (FB Prep, Triage, Description, Selection) submit JSONL to Vertex AI via GCS (DDR-084). Production FB Prep batch jobs were failing with:

```
collect-batch: batch request failed: {"code":3,"space":"generic","message":"Failed to parse JSON into proto: google.cloud.aiplatform.master.GenerateContentRequest with status: invalid JSON in google.cloud.aiplatform.master.GenerateContentRequest @ generationConfig: message google.cloud.aiplatform.master.GenerationConfig, near 1:23528961 (offset 23528960): no such field: 'systemInstruction'"}
```

The batch JSONL was built by marshaling `genai.GenerateContentConfig` into the `generationConfig` field. The genai SDK's `GenerateContentConfig` struct includes `SystemInstruction`, which was being serialized (as a sibling or nested field) into the `generationConfig` object. Vertex AI's `GenerationConfig` proto does **not** have a `systemInstruction` field — it is a top-level sibling of `generationConfig` in the `GenerateContentRequest`. Vertex AI rejects any unknown field in `generationConfig` with "no such field".

The code attempted to nil out `cfgCopy.SystemInstruction` before marshaling, but the `genai.GenerateContentConfig` struct could still emit `systemInstruction` in the JSON output (e.g. via `omitempty` behavior or struct embedding), causing Vertex AI to reject the batch input during import.

## Decision

### 1. Vertex AI–Compatible generationConfig Struct

Introduce a `vertexGenConfig` struct in `internal/ai/batch.go` that contains **only** fields Vertex AI accepts in `generationConfig`:

- `temperature`, `topP`, `topK`
- `maxOutputTokens`, `stopSequences`
- `presencePenalty`, `frequencyPenalty`
- `responseMimeType`, `responseSchema`, `responseJsonSchema`
- `seed`, `candidateCount`

**Explicitly excluded**: `systemInstruction`, `tools`, `toolConfig`, `httpOptions`, and any other genai fields not in Vertex AI's `GenerationConfig` proto.

### 2. toVertexGenConfig Helper

Add `toVertexGenConfig(cfg *genai.GenerateContentConfig) *vertexGenConfig` that copies only the allowed fields. `systemInstruction` remains at the request level as `batchJSONLRequest.SystemInstruction`, a sibling of `generationConfig` — matching the Vertex AI schema.

### 3. batchJSONLRequest Uses vertexGenConfig

Change `batchJSONLRequest.GenerationConfig` from `*genai.GenerateContentConfig` to `*vertexGenConfig`. This guarantees the marshaled JSON never contains `systemInstruction` inside `generationConfig`.

### 4. Unit Test

Add `TestBatchJSONLExcludesSystemInstructionFromGenerationConfig` in `internal/ai/batch_test.go` to assert that marshaled batch JSONL never places `systemInstruction` inside `generationConfig`.

## Files Changed

### Backend (`ai-social-media-helper`)
- `internal/ai/batch.go` — `vertexGenConfig` struct, `toVertexGenConfig()`, `batchJSONLRequest` uses `*vertexGenConfig`
- `internal/ai/batch_test.go` — new unit test

## Consequences

- Economy-mode batch jobs (FB Prep, Triage, Description, Selection) succeed on Vertex AI
- No change to callers — the fix is encapsulated in `batch.go`
- `vertexGenConfig` is a strict subset; new genai config fields must be explicitly added if Vertex AI supports them
- When config has only `SystemInstruction` (e.g. FB Prep), `toVertexGenConfig` returns `nil` and no `generationConfig` is emitted — valid per Vertex AI schema

## Rejected Alternatives

- **Continue nil-ing SystemInstruction on genai struct**: Unreliable — struct marshaling can still emit the field; Vertex AI rejects unknown fields
- **Custom JSON marshaler for genai.GenerateContentConfig**: Would require wrapping or forking the genai type; `vertexGenConfig` is simpler and self-contained
- **Drop systemInstruction for batch**: FB Prep and other workflows require system instructions; Vertex AI supports them at the request level
