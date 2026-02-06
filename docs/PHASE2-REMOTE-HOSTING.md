# Phase 2: Remote Hosting and Lambda Migration

**Status**: Planning (not yet implemented)  
**Prerequisite**: Phase 1 web UI (DDR-022) must be complete and stable

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

Phase 2 (target):
  Browser <-> Static Hosting Platform (e.g., CloudFront)
                |-> Preact SPA (same build output as Phase 1)
  Browser <-> API Gateway <-> Go Lambda
                                |-> S3
                                |-> Gemini API
```

The Preact SPA is identical in both phases. The only change is the API base URL:
- Phase 1: `http://localhost:8080/api/`
- Phase 2: `https://api.yourdomain.com/`

This is configured via an environment variable or build-time constant in the frontend.

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

## Recommendation

**Primary: Option 1 (S3 + CloudFront)** for production deployment.

- Gold-standard security with full header control
- Native AWS integration with Lambda backend (same IAM, Cognito, WAF)
- The setup complexity is a one-time cost, and the project is already invested in AWS

**Development/staging: Option 2 (Cloudflare Pages)** for preview deployments.

- Free, zero-config SPA routing, excellent DDoS protection
- Use for PR preview deployments and testing
- Simpler setup than CloudFront for non-production environments

---

## Backend Migration Notes (Lambda)

The Go HTTP handlers in `cmd/media-web/main.go` can be adapted to Lambda handlers with minimal changes:

1. **Handler signature change**: `http.HandlerFunc` -> `func(ctx, events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error)`
2. **File system access**: Replace `os.Open`/local paths with S3 SDK calls
3. **Thumbnail serving**: Generate thumbnails on-the-fly or cache in S3
4. **Media upload**: Frontend uploads directly to S3 via presigned URLs (not through Lambda)

The JSON API contracts (request/response shapes) remain identical. The frontend does not change.

### New AWS Resources Required

| Resource | Purpose |
|----------|---------|
| Lambda function | Go binary handling API requests |
| API Gateway (HTTP API) | Routes HTTP requests to Lambda |
| S3 bucket (media) | Stores uploaded media files |
| S3 bucket (frontend) | Stores SPA static files |
| CloudFront distribution | Serves frontend with security headers |
| Cognito User Pool | User authentication (if multi-user) |
| IAM roles | Lambda execution role, S3 access |
| ACM certificate | HTTPS for custom domain |

---

## Timeline Considerations

Phase 2 should not begin until:

1. Phase 1 web UI is feature-complete and stable
2. The JSON API contracts are finalized through real usage
3. There is a concrete need for remote access (not just local use)

Premature migration to Lambda adds complexity without benefit if the tool is only used locally.

---

**Last Updated**: 2026-02-06
