# DDR-028: Security Hardening for Cloud Deployment

**Date**: 2026-02-07  
**Status**: Implemented  
**Iteration**: 16

## Context

DDR-026 and DDR-027 deployed the application to AWS (Lambda + API Gateway + S3 + CloudFront). A security assessment of the deployed infrastructure revealed 19 vulnerabilities across both repos (`ai-social-media-helper` and `ai-social-media-helper-deploy`). The most critical finding: every API endpoint is publicly accessible with zero authentication, and the API Gateway URL can be called directly to bypass CloudFront.

The full assessment is documented in the [Consolidated Security Plan](../../.cursor/plans/consolidated_security_plan.plan.md).

### Current State

**What's already secure:**
- S3 buckets are private with public access blocked and AES256 encryption
- Media files auto-expire via 24-hour S3 lifecycle rule
- Gemini API key is in SSM Parameter Store as `SecureString` (DDR-025)
- IAM policies are scoped to specific resources (DDR-023)
- CloudFront sets security response headers (CSP, HSTS, X-Frame-Options)

**What's not secure:**
- **Zero authentication** on all API endpoints — anyone can upload files, start Gemini-powered triage jobs (real cost), read other sessions' media, and delete files
- **API Gateway is directly accessible** — its public URL (`https://xxx.execute-api.us-east-1.amazonaws.com`) bypasses CloudFront entirely, so any future CloudFront-level protection can be circumvented
- **No input validation** — `sessionId` is used directly in S3 key construction without UUID validation, enabling path traversal (`../../`); media endpoints accept any S3 key with no validation
- **CORS allows all origins** (`*`) on both API Gateway and S3, enabling cross-site request abuse
- **Sequential job IDs** (`triage-1`, `triage-2`, ...) allow enumeration of all triage jobs
- **No rate limiting** anywhere — unlimited requests to all endpoints
- **No upload validation** — any content type, any file size
- **No ownership checks** — anyone who knows a job ID can view results or confirm deletions

### Threat Model

This is a single-user personal tool. The primary threats are:
1. **Cost abuse** — unauthorized Gemini API calls and S3 storage consumption
2. **Automated scanning/bots** — the API Gateway URL is discoverable and callable
3. **Opportunistic attackers** — path traversal, file enumeration, upload abuse

The goal is to block 95%+ of these attacks with proportionate effort and cost.

## Decision

Implement security hardening in three phases. Each phase is a self-contained deployment that makes the app meaningfully more secure.

### Phase 1: Close the Open Doors (Critical)

#### 1a. Block direct API Gateway access with origin-verify header

CloudFront injects a secret header (`x-origin-verify`) into every request it forwards to API Gateway. Lambda middleware checks for this header and rejects requests that don't have it. The shared secret is stored in SSM Parameter Store.

This is the standard AWS pattern for restricting API Gateway access to CloudFront-only traffic. It ensures that any future CloudFront-level security (auth, WAF, rate limits) cannot be bypassed.

#### 1b. Add authentication via Amazon Cognito

Add a Cognito User Pool with `selfSignUpEnabled: false` (no signup page needed for a single-user app). The user account is provisioned via AWS CLI:

```bash
aws cognito-idp admin-create-user \
  --user-pool-id $POOL_ID \
  --username your@email.com \
  --user-attributes Name=email,Value=your@email.com Name=email_verified,Value=true \
  --message-action SUPPRESS

aws cognito-idp admin-set-user-password \
  --user-pool-id $POOL_ID \
  --username your@email.com \
  --password 'YourSecurePassword1!' \
  --permanent
```

API Gateway gets a JWT authorizer that validates Cognito tokens on every `/api/*` route (except `/api/health`). The Preact SPA uses the Cognito hosted UI for login and attaches the JWT to all API requests.

#### 1c. Validate all user-provided parameters

- `sessionId`: must match UUID regex `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`
- `filename`: must match `^[a-zA-Z0-9][a-zA-Z0-9._-]{0,254}$`, no `..`, `/`, or `\`
- S3 `key` (on media endpoints): must match `<uuid>/<safe-filename>` pattern
- Applied to `handleUploadURL`, `handleTriageStart`, `handleThumbnail`, `handleFullImage`

#### 1d. Restrict CORS to CloudFront domain only

Replace `allowOrigins: ['*']` with the CloudFront distribution domain in both `backend-stack.ts` (API Gateway CORS) and `storage-stack.ts` (S3 bucket CORS).

### Phase 2: Prevent Abuse (High)

#### 2a. Add API Gateway throttling

Stage-level throttling at 10 requests/second steady state with bursts up to 20. Returns HTTP 429 automatically. Zero Lambda code changes.

#### 2b. Validate file uploads

Content-type allowlist covering common photo formats (JPEG, PNG, GIF, WebP, HEIC, TIFF, BMP), RAW camera formats (DNG, CR2, CR3, NEF, ARW, RAF, ORF, RW2, SRW), and video formats (MP4, MOV, WebM, AVI, MKV, 3GP, AVCHD).

Size limits: **50 MB for photos**, **5 GB for videos**. The photo limit covers phone JPEGs (4-25 MB), phone RAW/DNG (~6 MB), and standard DSLR RAW (30-50 MB). The video limit covers ~5 minutes of 5.3K 60fps GoPro footage (~1 GB/min).

#### 2c. Randomize job IDs

Replace `jobSeq++` / `fmt.Sprintf("triage-%d", jobSeq)` with 128-bit cryptographically random IDs via `crypto/rand`. Applied in both `cmd/media-lambda/main.go` and `cmd/media-web/main.go`.

#### 2d. Add ownership checks

Store `sessionId` (and Cognito user ID when available) with each triage job. Validate ownership on `/api/triage/{id}/results` and `/api/triage/{id}/confirm`.

### Phase 3: Defense in Depth (Medium/Low)

- Enable API Gateway access logging to CloudWatch (30-day retention)
- Add CloudWatch alarms for Lambda errors and throttles
- Enforce path containment in local web server (`cmd/media-web/main.go`)
- Use descriptive-but-safe error messages (strip S3 bucket paths, ARNs, account IDs; keep validation context)
- Parameterize CodeStar connection ARN in `pipeline-stack.ts`
- Check `.gpg-passphrase` file permissions (0600) before reading in `internal/auth/auth.go`
- Tighten CSP (remove `unsafe-inline` for styles if feasible)
- Add `govulncheck` and `npm audit` to CI pipeline

### WAF: Deferred

AWS WAF is not included in the initial implementation. API Gateway throttling (Phase 2) provides rate limiting at no cost. WAF options for future consideration:

| Option | Monthly cost | What it adds |
|---|---|---|
| **Option C: WAF rate-only** | ~$6/mo ($5 Web ACL + $1 rule) | Per-IP rate limiting at CloudFront edge |
| **Option D: WAF full rules** | ~$8/mo ($5 Web ACL + $3 rules) + $0.60/M requests | Rate limiting + `AWSManagedRulesCommonRuleSet` + `AWSManagedRulesKnownBadInputsRuleSet` (auto-blocks SQL injection probes, bad bots, known attack patterns) |

Trigger for upgrading: evidence of bot traffic or sustained abuse in API Gateway access logs.

## Rationale

### Why Cognito over Basic Auth?

| Criteria | Cognito | CloudFront Basic Auth |
|----------|:-:|:-:|
| Token-based (no password on every request) | Yes | No (base64 password in every request) |
| Brute-force protection | Built-in lockout | None |
| Token expiry and refresh | Automatic | No tokens — session is permanent |
| Per-user accounts | Yes | Shared credentials |
| Credential revocation | Disable user in console/CLI | Redeploy CloudFront Function |
| Cost | Free (up to 50K MAU) | Free |
| Signup page needed | No (`admin-create-user` via CLI) | N/A |
| Hosted login page | Provided by Cognito | Browser native prompt |

For a single-user app, both work. Cognito is chosen because it's the proper long-term solution: if the app is ever shared, no rearchitecting is needed. The hosted UI means no custom login page to build.

### Why 50 MB photo limit?

| Source | File size |
|---|---|
| Phone JPEG (12MP default) | 4-8 MB |
| Phone JPEG (200MP full-res) | ~25 MB |
| Phone RAW (DNG, JPEG XL compressed) | ~6 MB |
| Professional DSLR RAW (45MP) | 30-50 MB |
| High-res DSLR RAW (61MP, uncompressed) | 60-120 MB |

50 MB covers all typical photography workflows. The outlier is uncompressed 61MP RAW files (Sony A7R V), which would need to be exported as compressed RAW or JPEG. This is an acceptable trade-off for keeping S3 costs reasonable.

### Why 5 GB video limit?

| Source | File size |
|---|---|
| 4K 60fps (GoPro, phones) | ~800 MB/min → ~4 GB for 5 min |
| 5.3K 60fps (GoPro Hero 12/13) | ~1 GB/min → ~5 GB for 5 min |

5 GB covers a 5-minute clip at the highest consumer resolution. Longer recordings can be split. With 24-hour S3 expiry, even a 5 GB upload costs only a few cents in storage.

### Why defer WAF?

The combination of origin-verify header (blocks direct API Gateway access), Cognito auth (blocks unauthenticated requests), and API Gateway throttling (rate limits at 10 req/sec) already blocks the vast majority of attacks. WAF adds $6-8/month for automated pattern-based blocking that is unlikely to trigger against a single-user app behind Cognito auth. Adding it later requires only a CDK change — no application code changes.

### Why not skip Cognito and rely on origin-verify alone?

The origin-verify header ensures requests came through CloudFront, but CloudFront is publicly accessible. Without auth, anyone who visits the CloudFront URL can use the API. The origin-verify header protects CloudFront's security layers from being bypassed — it's a gate behind the gate, not a replacement for user auth.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| CloudFront Basic Auth (instead of Cognito) | Shared credentials, no token expiry, no brute-force protection, password in every request, requires rearchitecting if ever shared |
| API key in Lambda middleware (instead of Cognito) | Must manage key distribution and rotation manually; no login UI; keys don't expire |
| Cognito with self-signup enabled | Unnecessary for single-user app; opens the door to unauthorized account creation |
| AWS WAF immediately | $6-8/month for protection that's mostly redundant behind Cognito auth; can be added with a CDK-only change later |
| 250 MB photo limit | Unnecessarily generous; 50 MB covers all typical workflows; larger limit increases potential for storage cost abuse |
| No file type validation | Allows upload of `.exe`, `.html`, `.php` files; potential for stored XSS if served back |
| Keep sequential job IDs with auth | Auth prevents unauthorized access, but sequential IDs still leak information about system usage (how many jobs have been created) |

## Consequences

**Positive:**

- All API endpoints require valid Cognito JWT — unauthorized access is blocked at the API Gateway level before Lambda is invoked
- Direct API Gateway access is blocked — CloudFront-level protections (auth, future WAF) cannot be bypassed
- Path traversal eliminated — `sessionId` and `key` parameters are validated against strict patterns
- CORS restricted to CloudFront domain — cross-site request abuse blocked
- Rate limiting prevents cost attacks — 10 req/sec steady state via API Gateway throttling
- Upload validation prevents storage abuse — 50 MB photo / 5 GB video limits with content-type allowlist
- Job ID enumeration eliminated — 128-bit random IDs are unguessable
- Ownership enforced — only the creator of a triage job can view results or confirm deletions
- Single-user account provisioned via CLI — no signup page, no email verification, no temporary password flow
- Descriptive error messages aid debugging — validation errors include what failed and what was expected, without leaking infrastructure details
- Zero ongoing cost — Cognito free tier, API Gateway throttling is free, no WAF

**Trade-offs:**

- Cognito adds a login step — user must authenticate via hosted UI before using the app (one-time per browser session, token auto-refreshes)
- Frontend SPA needs Cognito integration — `amazon-cognito-identity-js` or Amplify Auth library, OAuth callback handling, token attachment to API requests
- CDK stack changes span both repos — origin-verify header in `frontend-stack.ts`, Cognito + authorizer in `backend-stack.ts`, CORS in `storage-stack.ts`
- 50 MB photo limit excludes uncompressed 61MP RAW files — acceptable for a media triage tool (users can export as compressed RAW)
- 5 GB video limit excludes recordings longer than ~5 minutes at 5.3K — users can split longer recordings
- WAF is deferred — automated pattern-based blocking (SQL injection probes, known bad bots) is not active until manually upgraded

## Implementation

### Changes to Application Repo (`ai-social-media-helper`)

| File | Changes |
|------|---------|
| `cmd/media-lambda/main.go` | Add origin-verify middleware; add `validateSessionID`, `validateFilename`, `validateS3Key` functions; call validators in all handlers; replace `jobSeq++` with `newJobID()` using `crypto/rand`; add content-type allowlist and size limit check in `handleUploadURL`; store sessionId with triage jobs; validate ownership on results/confirm; update `httpError` to support descriptive-but-safe messages |
| `cmd/media-web/main.go` | Replace `jobSeq++` with `newJobID()`; add path containment check in `handleBrowse`, `handleThumbnail`, `handleFullImage` |
| `internal/auth/auth.go` | Add file permission check (0600) before reading `.gpg-passphrase` |
| `web/frontend/src/api/client.ts` | Add Cognito auth: attach JWT `Authorization: Bearer <token>` header to all API requests |
| `web/frontend/src/app.tsx` | Add Cognito login flow: redirect to hosted UI if no valid token, handle OAuth callback |

### Changes to Deploy Repo (`ai-social-media-helper-deploy`)

| File | Changes |
|------|---------|
| `cdk/lib/backend-stack.ts` | Add Cognito User Pool (selfSignUpEnabled: false) + User Pool Client; add JWT authorizer to HTTP API; add `ORIGIN_VERIFY_SECRET` to Lambda env vars; restrict CORS `allowOrigins` to CloudFront domain; add API Gateway stage-level throttling |
| `cdk/lib/frontend-stack.ts` | Add `x-origin-verify` custom header to API Gateway origin in CloudFront |
| `cdk/lib/storage-stack.ts` | Restrict S3 CORS `allowedOrigins` to CloudFront domain |
| `cdk/lib/pipeline-stack.ts` | Parameterize CodeStar connection ARN; add `govulncheck` and `npm audit` to build specs |
| `cdk/bin/cdk.ts` | Read CodeStar ARN from environment variable |

### Post-Deploy (One-Time)

```bash
# 1. Store origin-verify secret in SSM
aws ssm put-parameter --name /ai-social-media/prod/origin-verify-secret \
  --value "$(openssl rand -hex 32)" --type SecureString

# 2. Create Cognito user (after CDK deploy creates the User Pool)
aws cognito-idp admin-create-user \
  --user-pool-id <POOL_ID> \
  --username your@email.com \
  --user-attributes Name=email,Value=your@email.com Name=email_verified,Value=true \
  --message-action SUPPRESS

aws cognito-idp admin-set-user-password \
  --user-pool-id <POOL_ID> \
  --username your@email.com \
  --password 'YourSecurePassword1!' \
  --permanent
```

## Related Decisions

- [DDR-023](./DDR-023-aws-iam-deployment-user.md): AWS IAM User and Scoped Policies — existing IAM permissions for CDK deployment
- [DDR-025](./DDR-025-ssm-parameter-store-secrets.md): SSM Parameter Store — pattern reused for origin-verify secret
- [DDR-026](./DDR-026-phase2-lambda-s3-deployment.md): Phase 2 Lambda + S3 — initial cloud deployment being secured
- [DDR-027](./DDR-027-container-image-lambda-local-commands.md): Container Image Lambda — current Lambda deployment model
- [Consolidated Security Plan](../../.cursor/plans/consolidated_security_plan.plan.md): Full vulnerability assessment with code-level fixes
