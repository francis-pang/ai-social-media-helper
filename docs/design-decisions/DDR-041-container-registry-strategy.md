# DDR-041: Container Registry Strategy — ECR Private + ECR Public

**Date**: 2026-02-09  
**Status**: Accepted  
**Iteration**: Phase 2 Cloud Deployment

## Context

The multi-Lambda architecture (DDR-035) deploys 5 Lambda functions as container images, currently stored in **two private ECR repositories** (`ai-social-media-lambda-light` and `ai-social-media-lambda-heavy`). All images — including non-sensitive base layers — are stored privately, incurring ECR private storage costs ($0.10/GB/month).

The project contains a mix of:

1. **Non-sensitive code** — standard Go HTTP handlers, Preact frontend, generic media processing utilities. These are safe to publish publicly and could benefit the community.
2. **Sensitive/critical code** — proprietary prompt templates, AI orchestration logic, business-specific selection algorithms, and authentication/session handling. These should remain private.

AWS ECR offers two tiers:

- **ECR Private**: $0.10/GB/month storage, data transfer charges. Suitable for proprietary images.
- **ECR Public** (public.ecr.aws): 50 GB free storage, 500 GB/month free bandwidth for authenticated users (5 TB/month for AWS-authenticated). Suitable for open-source or non-sensitive images.

Docker Hub's free tier has pull rate limits (100 pulls/6hr anonymous, 200/6hr authenticated) that can disrupt CI/CD pipelines and Lambda cold starts. ECR Public has no per-pull rate limits for authenticated AWS users, making it a more reliable choice for AWS-hosted workloads.

Reference: [Amazon ECR service quotas](https://docs.aws.amazon.com/AmazonECR/latest/userguide/service-quotas.html)

## Decision

Adopt a **hybrid registry strategy** using both ECR Private and ECR Public:

### ECR Private (paid) — Critical/proprietary images

Store Lambda function images that contain sensitive business logic:

| Image | Repository | Reason |
|---|---|---|
| API handler (`media-lambda`) | `ai-social-media-lambda-light` (private) | Contains authentication, session management, prompt orchestration |
| Selection processor (`selection-lambda`) | `ai-social-media-lambda-heavy` (private) | Contains proprietary AI selection algorithms and prompt templates |

### ECR Public (free) — Non-sensitive images

Publish images that contain generic, non-proprietary code:

| Image | Repository | Reason |
|---|---|---|
| Thumbnail processor (`thumbnail-lambda`) | `public.ecr.aws/<alias>/lambda-heavy` | Generic ffmpeg thumbnail extraction — no business logic |
| Enhancement processor (`enhance-lambda`) | `public.ecr.aws/<alias>/lambda-light` | Generic Gemini API passthrough — no proprietary prompts |
| Video processor (`video-lambda`) | `public.ecr.aws/<alias>/lambda-heavy` | Generic ffmpeg video processing — no business logic |

### Registry layout summary

```
ECR Private ($0.10/GB/month):
  ai-social-media-lambda-light   →  api-{commit}, api-latest
  ai-social-media-lambda-heavy   →  select-{commit}, select-latest

ECR Public (free, 50GB):
  public.ecr.aws/<alias>/lambda-light   →  enhance-{commit}, enhance-latest
  public.ecr.aws/<alias>/lambda-heavy   →  thumb-{commit}, thumb-latest, video-{commit}, video-latest
```

## Rationale

- **Cost reduction** — Moving 3 of 5 images to ECR Public reduces private storage from ~590 MB to ~250 MB (~$0.025/month → effectively the same, but the principle scales as the project grows).
- **No pull rate limits** — ECR Public has no per-pull rate limits for AWS-authenticated users (unlike Docker Hub), so Lambda cold starts and CI/CD builds are never throttled.
- **Security by separation** — Critical business logic (prompt templates, selection algorithms, auth handling) stays private. Generic media processing utilities are public.
- **Layer sharing preserved** — Within each ECR Public repository, Docker layer deduplication still works. The AL2023 base and ffmpeg layers are stored once.
- **Lambda compatibility** — Lambda supports pulling images from both ECR Private and ECR Public repositories. No infrastructure changes needed beyond updating image URIs in CDK.
- **Free storage headroom** — ECR Public provides 50 GB free. Current total image storage is ~590 MB, well within limits even with full history retention.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| All images in ECR Private | Pays for storage of non-sensitive images unnecessarily; missed opportunity to share generic tooling publicly |
| All images in ECR Public | Exposes proprietary prompt templates and business logic; no access control on public repos |
| Docker Hub for public images | Pull rate limits (100/6hr anonymous) can throttle Lambda cold starts and CI/CD; ECR Public is rate-limit-free for AWS users |
| GitHub Container Registry (ghcr.io) | Cross-cloud pull adds latency for Lambda; no native IAM integration; Docker Hub rate limits apply to GitHub Actions |
| All images in a single ECR Public repo | Cannot selectively keep some images private — a repository is either fully public or fully private |

## Consequences

**Positive:**

- Non-sensitive images are free to store and pull (50 GB ECR Public free tier)
- No Docker Hub pull rate limits disrupting builds or cold starts
- Clear security boundary: private = proprietary, public = generic
- Community can inspect and reuse generic media processing images
- Lambda image URIs change but CDK handles this declaratively

**Trade-offs:**

- Two authentication flows in CI/CD: `aws ecr get-login-password` for private, `aws ecr-public get-login-password` for public (ECR Public login uses `us-east-1` regardless of deployment region)
- ECR Public repos require a public alias and catalog metadata (one-time setup)
- Must audit each new Lambda before deciding private vs. public to avoid accidentally publishing sensitive code
- ECR Public images are visible to anyone — ensure no secrets, API keys, or proprietary algorithms are baked into public images

## Related Documents

- [DDR-035: Multi-Lambda Deployment Architecture](./DDR-035-multi-lambda-deployment.md) — defines the 5 Lambda functions and 2 ECR repos
- [DDR-027: Container Image Lambda](./DDR-027-container-image-lambda-local-commands.md) — original container image deployment
- [docs/DOCKER-IMAGES.md](../DOCKER-IMAGES.md) — Docker image strategy, layer structure, and build pipeline
