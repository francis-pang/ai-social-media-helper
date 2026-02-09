# DDR-048: Instagram OAuth Lambda — Automated Token Exchange

**Date**: 2026-02-09  
**Status**: Accepted  
**Iteration**: Phase 2 Cloud Deployment

## Context

The application publishes content to Instagram via the Graph API (DDR-040) using a long-lived access token stored in SSM Parameter Store. Currently, the token lifecycle is fully manual:

1. Generate a short-lived token via the Instagram Login OAuth flow (browser)
2. Exchange it for a long-lived token via `curl` at the command line
3. Store the long-lived token in SSM via `aws ssm put-parameter`

This manual process is error-prone and must be repeated every 60 days when the token expires. The Meta developer dashboard for the app (**Francis Media Uploader-IG**, App ID `YOUR_INSTAGRAM_APP_ID`) requires a **Redirect URL** to be configured for the Instagram Business Login product, but no endpoint exists to handle the redirect.

## Decision

Create a dedicated **oauth-lambda** (`cmd/oauth-lambda/`) deployed within the existing WebhookStack (DDR-044). The Lambda handles the Instagram OAuth 2.0 authorization code exchange, converts the short-lived token to a long-lived token, and stores both the token and user ID in SSM Parameter Store.

### Architecture

```
User Browser
    → Instagram OAuth (/oauth/authorize)
    → Instagram Login Screen
    → Redirect to callback with ?code=AUTH_CODE
    ↓
CloudFront (Webhook Distribution — DDR-044)
    ↓
API Gateway HTTP API (no auth, existing webhook API)
    ↓ GET /oauth/callback
OAuth Lambda (128 MB, 10s) — Go binary
    → POST https://api.instagram.com/oauth/access_token (code → short-lived token)
    → GET https://graph.instagram.com/access_token (short → long-lived token)
    → SSM PutParameter (token + user_id)
    → HTML success page to browser
```

### Route

| Method | Path | Lambda | Auth |
|--------|------|--------|------|
| `GET` | `/oauth/callback` | oauth-lambda | None (browser redirect from Meta) |

This route is added to the existing webhook API Gateway and CloudFront distribution, alongside the `/webhook` route used by the webhook Lambda (DDR-044).

### SSM Parameters

| Parameter | Type | Access | Purpose |
|-----------|------|--------|---------|
| `/ai-social-media/prod/instagram-app-id` | String | Read | Meta App ID |
| `/ai-social-media/prod/instagram-app-secret` | SecureString | Read | Meta App Secret (shared with webhook Lambda) |
| `/ai-social-media/prod/instagram-oauth-redirect-uri` | String | Read | Full callback URL (set after first deploy) |
| `/ai-social-media/prod/instagram-access-token` | SecureString | Write | Long-lived access token (read by API Lambda for publishing) |
| `/ai-social-media/prod/instagram-user-id` | String | Write | Instagram user ID (read by API Lambda for publishing) |

### Token Exchange Flow

```
Authorization Code (from Meta redirect)
    → POST /oauth/access_token
Short-Lived Token (1 hour) + User ID
    → GET /access_token?grant_type=ig_exchange_token
Long-Lived Token (60 days)
    → SSM PutParameter (overwrite existing)
```

### Build and Deploy

Uses the existing `Dockerfile.light` (DDR-035) with `CMD_TARGET=oauth-lambda`:

```bash
docker build --build-arg CMD_TARGET=oauth-lambda \
  -f cmd/media-lambda/Dockerfile.light .
```

ECR repository (`ai-social-media-oauth`) is owned by RegistryStack (DDR-046). The BackendPipelineStack (DDR-035) builds and deploys the image alongside the other 6 Lambdas.

## Rationale

### Why a separate Lambda instead of adding to the existing API Lambda?

The API Lambda uses Cognito JWT auth, origin-verify middleware, and CORS lockdown. The OAuth callback is a browser redirect from Meta — it has no JWT token, no origin-verify header, and arrives at a different CloudFront domain. Adding exception paths to the API Lambda would weaken its security model (same reasoning as DDR-044 for webhooks).

### Why in the WebhookStack instead of a new stack?

Both the webhook Lambda and the OAuth Lambda handle Meta/Instagram callbacks. They share the same CloudFront distribution and API Gateway, with different routes (`/webhook` and `/oauth/callback`). Grouping them avoids creating another CloudFront distribution (~$0.085/month) and API Gateway for a single endpoint.

### Why SSM for the redirect URI instead of a CDK environment variable?

The OAuth redirect URI includes the CloudFront distribution domain name, which is only known after the first deployment. Referencing `distribution.distributionDomainName` in the Lambda's environment variables creates a circular dependency (Lambda → CloudFront → API Gateway → Lambda). Storing the redirect URI in SSM breaks this cycle and is consistent with how other credentials are managed.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Add OAuth route to API Lambda | Requires bypassing JWT auth and origin-verify — same reasoning as DDR-044 |
| Use GitHub Pages as redirect URL | Static site cannot exchange auth code server-side without exposing app secret |
| Separate CDK stack for OAuth | Overkill for a single endpoint; creates an extra CloudFront distribution |
| Manual token exchange forever | Error-prone; must be repeated every 60 days; no redirect URL to register in Meta dashboard |
| CDK context parameter for redirect URI | Must be provided on every `cdk deploy`; easy to forget |

## Consequences

**Positive:**

- Automates the Instagram token acquisition — no more manual `curl` commands
- Provides a valid Redirect URL for the Meta developer dashboard
- Reuses existing WebhookStack infrastructure (CloudFront, API Gateway) — zero marginal infrastructure cost
- SSM PutParameter immediately updates the token for the API Lambda (next cold start picks it up)
- Follows established patterns: same Dockerfile, ECR strategy, SSM credential management

**Trade-offs:**

- One additional ECR repository (negligible cost — storage only)
- Pipeline builds 7 images instead of 6 (~30s additional with Docker layer caching)
- Three new SSM parameters must be created before first use (app ID, redirect URI, app secret already exists)
- After first deploy, user must set the redirect URI SSM parameter (one-time manual step)
- Token refresh still requires the user to re-trigger the OAuth flow every 60 days (automated refresh can be added later via CloudWatch scheduled Lambda)

## Pre-deployment Setup

1. Create SSM parameters (app secret already exists from DDR-044):
   ```bash
   aws ssm put-parameter \
     --name /ai-social-media/prod/instagram-app-id \
     --type String \
     --value "YOUR_INSTAGRAM_APP_ID"

   # Set after first deploy — use the WebhookDistributionDomain output
   aws ssm put-parameter \
     --name /ai-social-media/prod/instagram-oauth-redirect-uri \
     --type String \
     --value "https://<webhook-cf-domain>/oauth/callback"
   ```

2. In Meta Developer Dashboard → Instagram Business Login → API Setup:
   - **Redirect URL**: `https://<webhook-cf-domain>/oauth/callback`

## Implementation

| File | Purpose |
|------|---------|
| `internal/instagram/oauth.go` | Token exchange functions (ExchangeCode, ExchangeLongLivedToken) |
| `cmd/oauth-lambda/main.go` | Lambda entry point and HTTP handler |
| `cdk/lib/registry-stack.ts` | Add `oauthEcrRepo` ECR repository |
| `cdk/lib/webhook-stack.ts` | Add OAuth Lambda, route, SSM permissions |
| `cdk/lib/backend-pipeline-stack.ts` | Add OAuth build + deploy step |
| `cdk/bin/cdk.ts` | Wire OAuth ECR repo through stacks |

## Related Documents

- [DDR-040: Instagram Publishing Client](./DDR-040-instagram-publishing-client.md) — Token usage for publishing
- [DDR-044: Instagram Webhook Integration](./DDR-044-instagram-webhook-integration.md) — WebhookStack architecture
- [DDR-035: Multi-Lambda Deployment](./DDR-035-multi-lambda-deployment.md) — Lambda architecture and Dockerfile strategy
- [DDR-046: Centralized RegistryStack](./DDR-046-centralized-registry-stack.md) — ECR repository ownership
- [DDR-028: Security Hardening](./DDR-028-security-hardening.md) — Origin-verify and JWT auth model
