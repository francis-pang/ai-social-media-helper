# DDR-006: Model Selection: gemini-3-flash-preview

**Date**: 2025-12-30  
**Status**: Accepted  
**Iteration**: 5-6

## Context

Gemini offers multiple models with different capabilities, latencies, and pricing. We needed to select a default model that works well for the CLI's use cases while remaining accessible on the free tier.

## Decision

Use `gemini-3-flash-preview` as the default model for validation and content generation.

## Rationale

- **Free tier compatible**: Explicitly free of charge per the [pricing documentation](https://ai.google.dev/gemini-api/docs/pricing)
- **Low latency**: Flash models are optimized for speed
- **Multimodal support**: Handles text, images, videos, and audio
- **Latest model**: Most intelligent Flash model with superior search and grounding
- **Consistent**: Same model used for validation and chat operations

## Alternatives Evaluated

| Model | Issue |
|-------|-------|
| `gemini-2.0-flash` | Rate limited to 0 requests on free tier |
| `gemini-2.0-flash-lite` | Rate limited to 0 requests on free tier |
| `gemini-2.5-flash` | Works, but `gemini-3-flash-preview` is newer |
| `gemini-pro` | Higher latency, unnecessary for this use case |
| List models API | Doesn't verify generation permissions |

## Free Tier Limits (as of 2025-12-30)

| Limit | Value |
|-------|-------|
| Requests per minute | 15 |
| Requests per day | 1500 |
| Tokens per minute | 1,000,000 |

## Consequences

- **Positive**: No cost for typical usage patterns
- **Positive**: Fast responses for interactive use
- **Trade-off**: Preview model may have API changes
- **Trade-off**: Free tier has rate limits (acceptable for CLI use)

## Future Consideration

If rate limits become restrictive or a better free-tier model becomes available, this decision may be superseded.

