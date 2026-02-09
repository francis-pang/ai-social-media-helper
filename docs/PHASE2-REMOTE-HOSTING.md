# Phase 2: Remote Hosting and Lambda Migration

**Status**: Implemented (2026-02-07)  
**Implementation Record**: [DDR-026](./design-decisions/DDR-026-phase2-lambda-s3-deployment.md)  
**Prerequisite**: Phase 1 web UI (DDR-022) — completed

## Overview

Phase 2 migrates the application from a local tool to a remotely hosted service:

1. **Backend**: Go HTTP server becomes an AWS Lambda function behind API Gateway
2. **Frontend**: Preact SPA moves from embedded-in-binary to hosted on a static hosting platform
3. **Media**: Files are uploaded to S3 instead of read from local filesystem

This document evaluates options for the frontend hosting platform. The backend migration (Go to Lambda) is a separate concern and is not covered here.

---

## Architecture Change

```
Phase 1 (current):
  Browser <-> Go HTTP Server (localhost:8080)
                |-> Embedded SPA (embed.FS)
                |-> JSON API
                |-> Local filesystem
                |-> Gemini API

Phase 2 (implemented):
  Browser <-> CloudFront (d10rlnv7vz8qt7.cloudfront.net)
                |-> /* : S3 origin (Preact SPA, OAC)
                |-> /api/* : API Gateway proxy (same-origin)
  Browser ---> S3 (presigned PUT URL, direct upload)
  CloudFront /api/* <-> API Gateway <-> Go Lambda
                                          |-> S3 (download to /tmp)
                                          |-> Gemini API
                                          |-> SSM Parameter Store
```

The Preact SPA is identical in both phases. The CloudFront `/api/*` proxy means the API base URL is `""` (same-origin) in both modes. The only difference is the build-time `VITE_CLOUD_MODE` flag, which switches between the native file picker (Phase 1) and the drag-and-drop S3 uploader (Phase 2).

---

## Frontend Hosting Options

All options serve the same artifact: the `dist/` folder produced by `npm run build` (index.html + JS/CSS bundles).

### Option 1: AWS S3 + CloudFront

**What it is:** S3 bucket stores static files. CloudFront CDN serves them globally with HTTPS, custom headers, and caching.

**Cost:** Near-free for personal use. S3: ~$0.023/GB storage. CloudFront: first 1TB/month free.

#### Pros
- Full control over HTTP security headers (CSP, HSTS, X-Frame-Options, X-Content-Type-Options, Referrer-Policy) via CloudFront response headers policy
- SPA routing: CloudFront custom error response routes 404s to `/index.html` with 200 status
- Native integration with API Gateway/Lambda — same AWS account, same IAM, same VPC
- Free SSL certificates via AWS Certificate Manager
- S3 bucket can be fully private (CloudFront Origin Access Control)
- WAF integration for DDoS protection, geo-blocking, rate limiting
- Cognito integration for authentication
- Detailed access logs via CloudWatch

#### Cons
- Most complex setup: S3 bucket policy, CloudFront distribution, OAC, SSL cert, DNS
- AWS console/CLI configuration overhead
- Metered billing (near-zero but not free)

#### Security Profile
- **CSP:** Fully configurable. Gold standard.
- **HSTS:** Configurable with preload.
- **CORS:** Configurable per CloudFront behavior.
- **Bucket security:** Private, CloudFront OAC only. No public access.
- **WAF:** Available. Rate limiting, IP filtering, managed rule sets.
- **Auth:** API Gateway + Cognito for JWT-based auth.
- **Verdict:** Gold standard for security. Full control over every header and access pattern.

---

### Option 2: Cloudflare Pages

**What it is:** Free static site hosting on Cloudflare's CDN (300+ edge locations). Git-based deploy or CLI upload.

**Cost:** Free tier: unlimited bandwidth, unlimited requests, 500 builds/month.

#### Pros
- Completely free with unlimited bandwidth
- Largest edge network (300+ cities) — excellent global performance
- Custom HTTP headers via `_headers` file in build output
- SPA routing is automatic (serves `index.html` for all routes, zero config)
- Git-based deployment or `wrangler pages deploy` CLI
- Automatic HTTPS on `*.pages.dev`
- Preview deployments for every PR
- World-class DDoS protection included free

#### Cons
- Different cloud provider than Lambda backend — cross-cloud CORS required
- Workers (Cloudflare's serverless) uses V8 isolates, not Go — different runtime than Lambda
- 25 MiB max file size (fine for SPA, but limits direct media serving)
- Less integrated with AWS services (IAM, Cognito, WAF)

#### Security Profile
- **CSP:** Configurable via `_headers` file.
- **HSTS:** Configurable via `_headers` file.
- **DDoS:** Best-in-class, included free.
- **WAF:** Available on paid plans. Free tier has basic bot protection.
- **Auth:** No built-in auth — use JWT from API Gateway + Cognito.
- **Verdict:** Excellent security. Custom headers solve CSP. DDoS is best-in-class. Slightly less integrated with AWS backend.

---

### Option 3: Netlify

**What it is:** Static hosting with CI/CD, serverless functions, and form handling. Git-based deploy.

**Cost:** Free tier: 100GB bandwidth/month, 300 build minutes/month.

#### Pros
- Easy Git-based deployment with CI/CD
- Custom HTTP headers via `_headers` file or `netlify.toml`
- SPA routing via `_redirects` file (`/* /index.html 200`)
- Preview deployments for PRs
- Automatic HTTPS
- Netlify Functions for serverless (AWS Lambda under the hood)

#### Cons
- 100GB/month bandwidth on free tier (vs Cloudflare's unlimited)
- 300 build minutes/month (vs Cloudflare's 500)
- Less global edge coverage than Cloudflare
- Another vendor to manage alongside AWS

#### Security Profile
- **CSP:** Configurable via `_headers` file.
- **HSTS:** Configurable.
- **DDoS:** Basic protection. Not as robust as Cloudflare.
- **Verdict:** Good. Similar header configurability to Cloudflare. Less DDoS protection.

---

### Option 4: GitHub Pages

**What it is:** Free static site hosting from GitHub. Serves files from a repo branch.

**Cost:** Free (unlimited bandwidth for public repos).

#### Pros
- Completely free
- Deploys via `git push`
- Automatic HTTPS on `*.github.io`
- Already integrated with GitHub repo

#### Cons
- **Cannot set custom HTTP headers** — no CSP, no HSTS, no CORS headers. Fundamental limitation.
- SPA routing requires hash-based URLs (`/#/triage`) or `404.html` hack
- Public repos only for free tier
- 1GB storage limit, 100GB/month bandwidth soft limit

#### Security Profile
- **CSP:** Cannot set. Dealbreaker for security-conscious deployment.
- **CORS:** Cannot configure from hosting side.
- **Verdict:** Not viable for gold-standard security due to inability to set HTTP security headers.

---

## Comparison Matrix

| Criteria | S3 + CloudFront | Cloudflare Pages | Netlify | GitHub Pages |
|----------|-----------------|------------------|---------|--------------|
| **Security headers** | Full control | Via `_headers` | Via `_headers` | None |
| **SPA routing** | Custom error response | Automatic | `_redirects` file | Hash URLs only |
| **AWS integration** | Native (same account) | Cross-cloud CORS | Cross-cloud CORS | Cross-cloud CORS |
| **DDoS protection** | WAF (additional) | Best-in-class (free) | Basic | None |
| **Cost** | ~$0.50-1/mo | Free | Free (100GB limit) | Free |
| **Setup complexity** | High | Low | Low | Low |
| **Custom domain** | ACM + Route 53 | Built-in | Built-in | DNS config |
| **Build CI/CD** | Manual or CodePipeline | Git-integrated | Git-integrated | Git-integrated |

---

## Recommendation (Implemented)

**Selected: Option 1 (S3 + CloudFront)** — deployed as of 2026-02-07.

- Gold-standard security with full header control (CSP, HSTS, X-Frame-Options)
- Native AWS integration with Lambda backend (same account, IAM, CloudFront proxy)
- CloudFront proxies `/api/*` to API Gateway, eliminating CORS and tightening CSP to `connect-src 'self'`
- S3 bucket is fully private with CloudFront OAC
- SPA routing via CloudFront custom error responses (403/404 -> `/index.html`)

---

## Backend Migration (Implemented)

The Go backend was implemented as a separate binary (`cmd/media-lambda/main.go`) rather than adapting `cmd/media-web` handlers. Key implementation decisions:

1. **`httpadapter.NewV2`** wraps a standard `http.ServeMux` for API Gateway HTTP API v2 payload format — handlers are testable with `httptest`
2. **Presigned URL uploads** — browser uploads directly to S3 (bypasses Lambda 6MB limit); Lambda generates 15-minute presigned PUT URLs
3. **Download-to-tmp** — Lambda downloads S3 objects to `/tmp/{sessionId}/` so existing `filehandler.LoadMediaFile()` and `chat.AskMediaTriage()` work unchanged
4. **SSM Parameter Store** — Gemini API key loaded from SSM SecureString at cold start (see [DDR-025](./design-decisions/DDR-025-ssm-parameter-store-secrets.md))

### Deployed AWS Resources

| Resource | Identifier |
|----------|-----------|
| Lambda function | `AiSocialMediaApiHandler` (`provided.al2023`, 1GB, 5min timeout) |
| API Gateway (HTTP API) | `obopiy55xg.execute-api.us-east-1.amazonaws.com` |
| S3 bucket (media) | `ai-social-media-uploads-123456789012` (24h lifecycle) |
| S3 bucket (frontend) | `ai-social-media-frontend-123456789012` |
| S3 bucket (artifacts) | `ai-social-media-artifacts-123456789012` |
| CloudFront distribution | `d10rlnv7vz8qt7.cloudfront.net` (ID: EFVHUDLKPXL4H) |
| SSM parameter | `/ai-social-media/prod/gemini-api-key` |
| CodePipeline | `AiSocialMediaPipeline` |

Infrastructure is defined in the `ai-social-media-helper-deploy` repository using CDK (TypeScript), with four stacks: Storage, Backend, Frontend, Pipeline.

---

## Current Limitations

1. **Video triage not supported in Lambda** — no FFmpeg/FFprobe in the Lambda runtime; only images are processed. Videos uploaded to S3 are skipped during triage. Can be addressed by adding an FFmpeg Lambda layer.
2. **No custom domain** — served from `d10rlnv7vz8qt7.cloudfront.net`. Add ACM certificate + Route 53 for a friendly URL.

## Landing Page (DDR-042)

As of 2026-02-09, the cloud deployment shows a **landing page** that lets users choose between:

- **Media Triage** — upload files, AI identifies unsaveable media, review and delete
- **Media Selection** — full pipeline: upload, select, enhance, group, caption, publish

This replaced the previous build-time `VITE_CLOUD_MODE` switching that only exposed the selection workflow in cloud mode. Both workflows are now accessible from a single deployment.

---

**Last Updated**: 2026-02-09
