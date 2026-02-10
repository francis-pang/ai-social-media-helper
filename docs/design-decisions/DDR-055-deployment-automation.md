# DDR-055: Deployment Automation — Hybrid Pipeline Triggers and Full Validation

**Date**: 2026-02-10  
**Status**: Accepted  
**Iteration**: Phase 2 — Cloud Deployment

## Context

The project has two repositories:

| Repository | Purpose | Contents |
|-----------|---------|----------|
| `ai-social-media-helper` | Application code | Go backend (11 Lambda container images), Preact frontend (SPA) |
| `ai-social-media-helper-deploy` | Infrastructure | CDK (10 CloudFormation stacks), Makefile-driven deployment |

Both CodePipelines (`AiSocialMediaBackendPipeline`, `AiSocialMediaFrontendPipeline`) exist in AWS but are configured with `triggerOnPush: false`, requiring manual execution. The CDK deploy repo has no CI/CD — deploys run locally via `make deploy`. No pre-push validation exists, so known failure patterns from the error catalog (DDR-047 §6.2) can reach production unchecked.

Four decisions are needed:
1. **How to trigger app pipelines** on push to `main`
2. **How to trigger CDK deploys** on push to `main`
3. **What local validation** to run before push
4. **How GitHub Actions authenticates** with AWS

## Decision

### A4: Hybrid — CodeStar + GitHub Actions (App Pipeline Trigger)

Enable `triggerOnPush: true` on both CodePipeline source actions so CodeStar natively triggers both pipelines on every push to `main`. Deploy a GitHub Actions workflow (`.github/workflows/deploy-on-main.yml`) that acts as an intelligent filter: it analyzes the commit's changed files and stops whichever pipeline is unnecessary.

**Change detection logic:**

| Changed directories | Backend Pipeline | Frontend Pipeline |
|--------------------|-----------------|-------------------|
| Only `web/` | STOPPED | Runs |
| Only `cmd/`, `internal/`, `go.*`, `Dockerfile*` | Runs | STOPPED |
| Both frontend + backend paths | Runs | Runs |
| Other files only (docs, scripts, etc.) | STOPPED | STOPPED |

The workflow computes `git diff --name-only HEAD~1 HEAD` to classify the change scope, then calls `aws codepipeline stop-pipeline-execution` on unnecessary pipeline(s). It also supports `workflow_dispatch` for manual selective triggering (choices: `auto-detect`, `backend-only`, `frontend-only`, `both`).

### B1: GitHub Actions Only (CDK Deploy Trigger)

A `.github/workflows/deploy-cdk.yml` workflow runs `cdk synth`, `cdk diff`, and `cdk deploy` on push to `main` in the deploy repo. Path-filtered to `cdk/**` to avoid deploys on non-CDK changes. Supports `workflow_dispatch` with targets: `core`, `full`, `pipelines`, `edge`, `observability`.

### C3: Full Validation (Local Git Hooks)

Pre-push git hooks in both repos run comprehensive validation before allowing pushes to `main`.

**App repo (`.githooks/pre-push`):**
- `go vet ./...` — static analysis
- `go build ./cmd/...` — compilation check for all 14 commands
- `cd web/frontend && npm run build` — frontend build verification
- Secret detection — grep for AWS keys, API keys, credentials

**Deploy repo (`.githooks/pre-push`):**
- `tsc --noEmit` — TypeScript strict compilation
- `cdk synth --all` — CloudFormation template synthesis
- `cdk diff --all` — preview infrastructure changes (informational)
- `cdk/scripts/validate-cdk.sh` — custom CDK lint checking for:
  - Banned `functionName` on DockerImageFunction (DDR commit `460fbef`)
  - Missing `--provenance=false` in Docker builds (DDR commit `037b837`)
  - `ECR_ACCOUNT_ID` validation in heavy image builds (DDR commit `0b79423`)
  - Bash-specific syntax in buildspec commands (DDR commit `57086d7`)
  - Duplicate log group names across stacks (DDR commit `04ad39a`)
  - Synthesized CloudFormation template JSON validity
- Secret detection

### D1: Access Keys (AWS Authentication)

Store `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` as GitHub repository secrets on both repos. GitHub Actions workflows use `aws-actions/configure-aws-credentials@v4` with these secrets.

## Rationale

- **A4 over A1 (CodeStar only):** CodeStar alone triggers both pipelines on every push regardless of what changed. A frontend-only CSS tweak would rebuild all 11 Docker images (~10 min wasted). The GitHub Actions layer adds selective stopping with zero extra latency on the trigger side — CodeStar fires immediately, and unnecessary pipelines are stopped within seconds.
- **A4 over A2 (GitHub Actions only):** Pure GitHub Actions as trigger means pipeline start depends on GitHub Actions availability. With A4, if GitHub Actions is down, both pipelines still trigger via CodeStar (safe fallback — extra builds, but no missed builds).
- **B1 over B2 (local hook):** Cloud-based CDK deploy is auditable, reproducible, and doesn't depend on a specific developer's machine having the right Node.js/CDK versions.
- **C3 over C1/C2:** The error catalog documents specific failures that have caused production issues. The extra ~30-60s pre-push time is justified by catching these patterns locally before they reach CodePipeline or CloudFormation.
- **D1 over D2 (OIDC):** The IAM user `boyshawn` already exists with the required permission groups (`AiSocialMedia-Infra-Core`, `AiSocialMedia-Compute`, `AiSocialMedia-IAM`, `AiSocialMedia-CICD-CDN`). No additional IAM OIDC provider configuration needed. Acceptable for a single-developer project.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| A1: CodeStar `triggerOnPush: true` only | No selective triggering — both pipelines run on every push regardless of what changed |
| A2: GitHub Actions as sole trigger | No redundancy — if GitHub Actions is unavailable, no pipelines trigger |
| A3: Local hook triggers pipelines | Needs AWS credentials locally; easy to bypass by pushing without hook |
| B2: Local hook CDK deploy | Not truly automated on merge; depends on developer's local environment |
| B3: Pre-push reminder only | No actual validation — just a print statement |
| C1: Minimal (reminder only) | No local validation; known failure patterns reach production |
| C2: Light validation | Misses CDK lint checks that catch the specific failures in the error catalog |
| D2: OIDC | Requires IAM OIDC provider setup; overkill for single-developer project |

## Consequences

**Positive:**
- Every push to `main` automatically triggers the correct pipeline(s) via CodeStar
- Unnecessary builds are stopped within seconds, saving ~5-10 min per selective push
- Known failure patterns (6 checks in `validate-cdk.sh`) are caught locally before deploy
- CDK infrastructure deploys are automated and auditable via GitHub Actions logs
- Manual `workflow_dispatch` provides escape hatch for re-running specific pipelines

**Trade-offs:**
- GitHub Actions adds a dependency (mitigated by CodeStar as fallback trigger)
- Long-lived AWS credentials in GitHub Secrets require rotation discipline
- Pre-push hooks add ~30-60s to push time (only on pushes to `main`; bypass with `--no-verify` for emergencies)
- Brief double-execution window: between CodeStar triggering and GitHub Actions stopping the unnecessary pipeline, both pipelines start their Source stage (harmless — Source is fast and idempotent)

## Implementation Artifacts

| File | Repository | Purpose |
|------|-----------|---------|
| `.github/workflows/deploy-on-main.yml` | app | A4: Intelligent pipeline trigger/filter |
| `.githooks/pre-push` | app | C3: Full pre-push validation |
| `.githooks/setup.sh` | app | Hook installation script |
| `cdk/lib/backend-pipeline-stack.ts` | deploy | A4: `triggerOnPush: true` |
| `cdk/lib/frontend-pipeline-stack.ts` | deploy | A4: `triggerOnPush: true` |
| `.github/workflows/deploy-cdk.yml` | deploy | B1: CDK deploy automation |
| `.githooks/pre-push` | deploy | C3: Full pre-push validation |
| `.githooks/setup.sh` | deploy | Hook installation script |
| `cdk/scripts/validate-cdk.sh` | deploy | C3: CDK lint and validation |
| GitHub Secrets (both repos) | GitHub | D1: `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` |

## Related Documents

- [DDR-023](./DDR-023-aws-iam-deployment-user.md) — AWS IAM User and Scoped Policies for CDK Deployment
- [DDR-028](./DDR-028-security-hardening.md) — Security Hardening (CodeStar connection ARN)
- [DDR-035](./DDR-035-multi-lambda-deployment.md) — Multi-Lambda Deployment Architecture
- [DDR-041](./DDR-041-container-registry-strategy.md) — Container Registry Strategy
- [DDR-044](./DDR-044-instagram-webhook-integration.md) — Instagram Webhook Integration
- [DDR-045](./DDR-045-stateful-stateless-stack-split.md) — Stateful/Stateless Stack Split
- [DDR-047](./DDR-047-cdk-deploy-optimization.md) — CDK Deploy Optimization
- [DDR-048](./DDR-048-instagram-oauth-lambda.md) — Instagram OAuth Lambda
- [DDR-053](./DDR-053-granular-lambda-split.md) — Granular Lambda Split
