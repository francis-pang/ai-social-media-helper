# DDR-064: Model Upgrade: gemini-3-pro-preview to gemini-3.1-pro-preview

**Date**: 2026-02-19  
**Status**: Accepted  
**Iteration**: N/A

## Context

Google announced Gemini 3.1 Pro on February 19, 2026, as a significant upgrade to the Gemini 3 Pro preview model. The new model features an upgraded core reasoning engine (previously introduced with Gemini 3 Deep Think) and substantially improved benchmark scores:

- **ARC-AGI-2**: 77.1% (more than double Gemini 3 Pro)
- **Humanity's Last Exam**: 44.4%
- **GPQA Diamond**: 94.3% (scientific knowledge)
- **SWE-Bench Verified**: 80.6% (agentic coding)

The model is available in preview via Google AI Studio, Vertex AI, and the Gemini API.

No corresponding 3.1 Flash or 3.1 Pro Image models were announced. The default model (`gemini-3-flash-preview`) and image editing model (`gemini-3-pro-image-preview`) remain unchanged.

## Decision

Upgrade the Pro model constant from `gemini-3-pro-preview` to `gemini-3.1-pro-preview` for all Pro-tier usage (image analysis in Phase 2, CLI `--model` examples).

Keep `gemini-3-flash-preview` as the default and `gemini-3-pro-image-preview` for image editing — no 3.1 variants exist for these.

## Rationale

- **Improved reasoning**: 2x+ improvement on ARC-AGI-2 directly benefits the Phase 2 professional quality analysis, where the model must reason about what further enhancements a photo needs.
- **Better coding/agentic performance**: SWE-Bench Verified 80.6% indicates stronger structured output compliance, reducing JSON parse failures in analysis responses.
- **Drop-in replacement**: Same API surface and context window (1M tokens). No code changes beyond the model string.
- **No impact on default model**: The default `gemini-3-flash-preview` is unchanged, so triage, selection, and validation are unaffected.

## Scope of Change

| Component | Old Value | New Value |
|-----------|-----------|-----------|
| `ModelGemini3ProPreview` constant | `gemini-3-pro-preview` | `gemini-3.1-pro-preview` |
| Constant name | `ModelGemini3ProPreview` | `ModelGemini31ProPreview` |
| Phase 2 analysis model | `gemini-3-pro-preview` | `gemini-3.1-pro-preview` |
| CLI `--model` examples | `gemini-3-pro-preview` | `gemini-3.1-pro-preview` |

**Unchanged:**
- Default model: `gemini-3-flash-preview`
- Image editing model: `gemini-3-pro-image-preview`
- Imagen 3 model: `imagen-3.0-capability-001`

## Alternatives Considered

| Alternative | Issue |
|-------------|-------|
| Wait for stable (non-preview) release | No timeline announced; preview is production-ready for our use case |
| Update Flash to 3.1 as well | No Gemini 3.1 Flash model exists yet |
| Update Pro Image to 3.1 | No Gemini 3.1 Pro Image model exists yet |

## Consequences

- **Positive**: Better quality analysis in the enhancement pipeline (Phase 2)
- **Positive**: Better structured output compliance reduces parse failures
- **Trade-off**: Preview model may have API changes (same risk as current `gemini-3-pro-preview`)

## Related Decisions

- [DDR-006](./DDR-006-model-selection.md) — Default model selection (unchanged)
- [DDR-031](./DDR-031-multi-step-photo-enhancement.md) — Photo enhancement pipeline (Phase 2 analysis uses Pro)
- [DDR-032](./DDR-032-multi-step-video-enhancement.md) — Video enhancement pipeline (Phase 4 analysis uses Pro)
