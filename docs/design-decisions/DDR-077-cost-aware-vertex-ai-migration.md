# DDR-077: Cost-Aware Vertex AI Migration

**Date**: 2026-03-01  
**Status**: Accepted  
**Iteration**: Cloud — cost optimization and GCP integration

## Context

The app currently uses a GCP-linked Gemini API key with no free tier — paying for every call. A new GCP project (`gen-lang-client-0436578028`) with service account (`aws-app@gen-lang-client-0436578028.iam.gserviceaccount.com`) was created for AWS-to-GCP interaction. Google AI Studio is being deprecated in favor of Vertex AI Studio. Need to minimize costs for a personal-use app (~$4.31/month at current usage).

## Decision

### 1. Dual-Backend Architecture

Vertex AI as primary (using GCP service account with $300 credits), standalone Gemini Developer API as cost-free fallback.

### 2. Economy Mode (Batch API)

- **Default ON** for all workflows except enhancement feedback
- Uses Gemini Batch API at 50% cost reduction
- Server-side polling via Gemini Batch Poll Step Function: Wait 15s → GeminiBatchPollLambda → Choice loop
- 5–15 min turnaround for small batches

### 3. NewAIClient()

Replaces `NewGeminiClient()`. Tries Vertex AI first (`BackendVertexAI` with `VERTEX_AI_PROJECT` + `VERTEX_AI_REGION`), falls back to standalone Gemini API key (`BackendGeminiAPI` with `GEMINI_API_KEY`).

### 4. Service Account Loading

`LoadGCPServiceAccount()` reads JSON from `GCP_SERVICE_ACCOUNT_JSON` env var (from SSM), writes to `/tmp/gcp-sa-key.json`, sets `GOOGLE_APPLICATION_CREDENTIALS` for ADC.

### 5. SSM Parameters

- **New**: `/ai-social-media/prod/vertex-ai-service-account` (SecureString)
- **Replaced**: `/ai-social-media/prod/gemini-api-key` — now uses standalone (not GCP-linked) key

### 6. CDK Changes

- All AI Lambdas get `VERTEX_AI_PROJECT`, `VERTEX_AI_REGION`, `GCP_SERVICE_ACCOUNT_JSON` env vars
- New `GeminiBatchPollLambda` (128MB, 10s)
- New Gemini Batch Poll SFN (Standard Workflow)

## Consequences

**Positive:**

- $300 GCP credits last ~98 months at economy rate (~$3.05/month)
- Standalone Gemini API fallback provides effectively free usage within 1,000–1,500 RPD
- Economy Mode saves 29% on API costs (50% off everything except enhancement)
- Unified auth via service account simplifies GCP interaction
- Google Maps grounding available for FB Prep workflow

**Negative:**

- Two auth mechanisms to maintain (Vertex AI + standalone API key)
- Batch API adds 5–15 min latency for economy mode workflows
- Standalone free tier long-term survival uncertain (AI Studio deprecation)
- Additional infrastructure: GeminiBatchPollLambda + Gemini Batch Poll SFN

## Cost Analysis

| Workflow   | Calls/month | Real-time cost | Economy (50% off) |
| ---------- | ----------- | -------------- | ----------------- |
| Triage     | 36          | ~$0.73         | ~$0.37            |
| Selection  | 12          | ~$0.43         | ~$0.22            |
| Description| 120         | ~$0.66         | ~$0.33            |
| FB Prep    | 12          | ~$0.66         | ~$0.33            |
| Enhancement| 60+36       | ~$1.77         | ~$1.77 (always RT)|
| RAG        | ~960        | ~$0.06         | ~$0.03            |
| **Total**  |             | **~$4.31**     | **~$3.05**        |

## Risks

- Standalone Gemini API free tier may be deprecated alongside AI Studio
- Batch API latency (5–15 min) may be unacceptable for interactive workflows; enhancement remains real-time by design

## Alternatives Considered

| Approach                          | Rejected Because                                                                 |
| --------------------------------- | -------------------------------------------------------------------------------- |
| Vertex AI only                    | No fallback if GCP credits expire or project issues                              |
| Standalone API only               | No free tier for GCP-linked keys; paying for every call                          |
| Batch API for all workflows       | Enhancement feedback requires real-time response for UX                          |
| Single auth (Vertex only)         | Loses cost-free fallback within 1,000–1,500 RPD                                  |

## Implementation

### Files Modified

| File | Change |
| ---- | ------ |
| `internal/chat/` | `NewAIClient()` with dual-backend; `LoadGCPServiceAccount()` |
| `ai-social-media-helper-deploy/cdk/` | AI Lambda env vars; `GeminiBatchPollLambda`; Gemini Batch Poll SFN |
| SSM | New `vertex-ai-service-account`; replace `gemini-api-key` with standalone |

## Related Documents

- DDR-065 (Gemini Context Caching and Batch API — Batch API introduction)
- DDR-064 (Gemini 3.1 Pro Model Upgrade — model selection)
- DDR-025 (SSM Parameter Store Secrets — secret management)
