# DDR-044: Instagram Webhook Integration — Dedicated Stack

**Date**: 2026-02-09  
**Status**: Accepted  
**Iteration**: Phase 2 Cloud Deployment

## Context

The application publishes content to Instagram via the Graph API (DDR-040). Currently, all interaction is outbound — the app pushes content to Instagram but has no way to receive real-time notifications from Meta. Instagram's [Webhooks product](https://developers.facebook.com/docs/instagram-platform/webhooks) allows apps to receive push notifications for events like comments, mentions, story insights, messages, and media status updates.

Setting up webhooks requires two capabilities:

1. **Verification endpoint (GET)** — When configuring webhooks in the Meta App Dashboard, Meta sends a `GET` request with `hub.mode=subscribe`, `hub.verify_token`, and `hub.challenge` query parameters. The server must validate the verify token and respond with the challenge value.
2. **Event receiver (POST)** — Meta sends a `POST` request with a JSON payload for each event notification, signed with `X-Hub-Signature-256` (HMAC-SHA256 using the App Secret). The server must validate the signature and respond with `200 OK`.

The existing API Lambda and CloudFront distribution are tightly coupled: Cognito JWT auth, origin-verify headers, CORS lockdown, and SPA routing. Webhook requests from Meta are server-to-server with none of these concerns — they need a publicly accessible HTTPS endpoint with no authentication other than HMAC signature verification.

Reference: [Meta Webhooks — Instagram Platform](https://developers.facebook.com/docs/instagram-platform/webhooks)

## Decision

Create a **fully isolated WebhookStack** with its own CloudFront distribution, API Gateway, Lambda function, and ECR repository. The webhook Lambda is lightweight (128 MB, 10s timeout) and handles only verification and event logging.

### Architecture

```
Meta Platform
    ↓ GET/POST /webhook
CloudFront (Webhook) — dedicated distribution
    ↓
API Gateway HTTP API — no JWT, no CORS, throttled (10 burst / 5 steady)
    ↓
Webhook Lambda (128 MB, 10s) — Go binary
    ↓ reads at cold start
SSM Parameter Store
    ├── /ai-social-media/prod/instagram-webhook-verify-token
    └── /ai-social-media/prod/instagram-app-secret
```

### Components

#### 1. Webhook Lambda (`cmd/webhook-lambda/main.go`)

Lightweight Go Lambda — no S3, no DynamoDB, no Gemini. Dependencies:

- AWS SDK v2 SSM client (credential loading)
- `aws-lambda-go` + `aws-lambda-go-api-proxy` (HTTP adapter)
- `zerolog` (structured logging)
- Standard library only for crypto (`crypto/hmac`, `crypto/sha256`)

Built using the existing `Dockerfile.light` with `--build-arg CMD_TARGET=webhook-lambda`.

#### 2. Webhook Handler (`internal/webhook/handler.go`)

Reusable `http.Handler` implementing:

- **GET verification**: validates `hub.mode == "subscribe"` and `hub.verify_token`, responds with `hub.challenge`
- **POST event handling**: reads body, verifies `X-Hub-Signature-256` signature using HMAC-SHA256 with App Secret (constant-time comparison via `hmac.Equal`), logs the structured event payload, responds with `200 OK`

#### 3. WebhookStack (CDK)

Self-contained stack with no dependencies on BackendStack or StorageStack:

| Resource | Configuration |
|----------|---------------|
| ECR Private repo | `ai-social-media-webhook`, lifecycle: keep only latest |
| Lambda Function | 128 MB, 10s timeout, container image from ECR |
| API Gateway HTTP API | No authorizer, throttle: 10 burst / 5 steady |
| CloudFront Distribution | HTTPS only, caching disabled, forwards query strings |

#### 4. Pipeline Integration

The existing BackendPipelineStack adds a 6th Docker build:

```
Build 6: Webhook Lambda (private light)
  docker build --build-arg CMD_TARGET=webhook-lambda
    -t $PRIVATE_WEBHOOK_URI:webhook-$COMMIT
    -t $PRIVATE_WEBHOOK_URI:webhook-latest
    -f cmd/media-lambda/Dockerfile.light .
```

### Why a Separate Stack?

| Concern | Main App | Webhook |
|---------|----------|---------|
| Authentication | Cognito JWT | HMAC-SHA256 signature |
| Origin verification | CloudFront x-origin-verify header | Not needed |
| CORS | Locked to CloudFront domain | Not needed (server-to-server) |
| Traffic source | Browser (SPA) | Meta servers |
| Security model | User session tokens | App Secret |
| Failure blast radius | Affects all user-facing features | Only affects webhook delivery |

Mixing webhook routes into the main API would require bypassing JWT auth, origin-verify, and CORS — creating exception paths that weaken the security model. A separate stack maintains clean security boundaries.

## Rationale

- **Security isolation** — Webhook Lambda has no access to S3, DynamoDB, or Gemini. If compromised, the blast radius is limited to reading SSM parameters (verify token + app secret).
- **Independent scaling** — Meta can send bursts of up to 1000 batched events. The webhook Lambda scales independently from the main API without affecting user-facing latency.
- **Minimal cold start** — 128 MB Go binary with no heavy dependencies. Cold start is ~500ms vs ~2-3s for the 256 MB API Lambda with its dependency chain.
- **Reuses existing Dockerfile** — `Dockerfile.light` with a different `CMD_TARGET` avoids maintaining another build configuration.
- **CloudFront benefits** — AWS Shield Standard DDoS protection, edge termination, and the ability to add WAF rules later without infrastructure changes.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Add webhook route to existing API Lambda | Requires bypassing JWT auth, origin-verify, and CORS — creates exception paths in the security model |
| Lambda Function URL (no API Gateway) | No built-in throttling; CloudFront → Function URL loses query string forwarding without extra configuration |
| API Gateway directly (no CloudFront) | Loses DDoS protection (AWS Shield Standard) and the ability to add WAF rules |
| Separate API Gateway, same Lambda | Webhook and API concerns mixed in one Lambda; different security models in one binary |

## Consequences

**Positive:**

- Clean security boundary between user-facing API and Meta webhook endpoint
- Webhook Lambda is lightweight and fast — minimal cost ($0.20/million requests at 128 MB)
- CloudFront provides HTTPS with a valid certificate (required by Meta) and DDoS protection
- Event payloads are logged via zerolog for future processing (comments, mentions, etc.)
- Pipeline integration is a single additional Docker build step

**Trade-offs:**

- One additional CloudFront distribution (~$0.085/month minimum)
- One additional API Gateway HTTP API (~$1/million requests)
- One additional ECR repository (negligible cost — single image)
- BackendPipelineStack builds 6 images instead of 5 (~30s additional build time with Docker layer caching)
- Two SSM parameters must be created manually before first deployment

## Configuration

### Pre-deployment Setup

1. Create SSM parameters:
   ```bash
   aws ssm put-parameter \
     --name /ai-social-media/prod/instagram-webhook-verify-token \
     --value "your_chosen_verify_token" \
     --type SecureString

   aws ssm put-parameter \
     --name /ai-social-media/prod/instagram-app-secret \
     --value "your_app_secret_from_meta_dashboard" \
     --type SecureString
   ```

### Post-deployment Setup

2. In Meta Developer Dashboard → Webhooks → Instagram:
   - **Callback URL**: `https://<webhook-cloudfront-domain>/webhook`
   - **Verify Token**: The value stored in SSM above

3. Enable webhook subscriptions for your Instagram account:
   ```bash
   curl -i -X POST \
     "https://graph.instagram.com/v24.0/<INSTAGRAM_ACCOUNT_ID>/subscribed_apps \
     ?subscribed_fields=comments,messages \
     &access_token=<YOUR_ACCESS_TOKEN>"
   ```

4. Ensure the app is set to **Live** mode in the Meta App Dashboard (required for webhook delivery).

**Note:** Meta retries failed deliveries with decreasing frequency over 36 hours. Unacknowledged responses are dropped after 36 hours. When event processing is added later, implement deduplication to handle retries.

## Implementation

| File | Purpose |
|------|---------|
| `internal/webhook/handler.go` | Webhook verification and event handler logic |
| `internal/webhook/handler_test.go` | Unit tests |
| `cmd/webhook-lambda/main.go` | Lambda entry point |
| `cdk/lib/webhook-stack.ts` | New CDK stack |
| `cdk/bin/cdk.ts` | Register WebhookStack |
| `cdk/lib/backend-pipeline-stack.ts` | Add webhook build + deploy |

## Related Documents

- [DDR-040: Instagram Publishing Client](./DDR-040-instagram-publishing-client.md) — Instagram Graph API integration
- [DDR-035: Multi-Lambda Deployment](./DDR-035-multi-lambda-deployment.md) — Lambda architecture and ECR strategy
- [DDR-041: Container Registry Strategy](./DDR-041-container-registry-strategy.md) — ECR Private vs Public decisions
- [DDR-043: Step Functions Lambda Entrypoints](./DDR-043-step-functions-lambda-entrypoints.md) — Pattern for new Lambda cmd targets
- [DDR-028: Security Hardening](./DDR-028-security-hardening.md) — Origin-verify and JWT auth model
- [Meta Webhooks — Instagram Platform](https://developers.facebook.com/docs/instagram-platform/webhooks) — Official documentation
