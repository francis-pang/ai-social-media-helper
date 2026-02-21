# Deployment Strategy

How code changes reach production in the AI Social Media Helper project.

**Design decision:** [DDR-055](./design-decisions/DDR-055-deployment-automation.md)

---

## Overview

```
Developer workstation                         AWS (us-east-1)
┌─────────────────────┐                      ┌─────────────────────────────────────┐
│  git push main      │                      │  CodePipeline                       │
│  (app repo)         │──CodeStar──────────► │  ├─ BackendPipeline (11 Docker→ECR) │
│                     │                      │  └─ FrontendPipeline (Preact→S3+CF) │
│                     │──GitHub Actions────► │                                     │
│                     │  (change detection   │  GitHub Actions stops unnecessary   │
│                     │   stops unneeded     │  pipeline within ~15s               │
│                     │   pipeline)          │                                     │
└─────────────────────┘                      └─────────────────────────────────────┘

┌─────────────────────┐                      ┌─────────────────────────────────────┐
│  git push main      │                      │  GitHub Actions Runner              │
│  (deploy repo)      │──GitHub Actions────► │  cdk synth → cdk diff → cdk deploy │
│                     │                      │  (core stacks by default)           │
└─────────────────────┘                      └─────────────────────────────────────┘
```

---

## Repositories

| Repository | What deploys | How it triggers | Workflow |
|-----------|-------------|----------------|----------|
| `ai-social-media-helper` | Backend (11 Lambda images) + Frontend (Preact SPA) | CodeStar `triggerOnPush: true` + GitHub Actions intelligent filter | `.github/workflows/deploy-on-main.yml` |
| `ai-social-media-helper-deploy` | CDK infrastructure (10 CloudFormation stacks) | GitHub Actions on push to `main` | `.github/workflows/deploy-cdk.yml` |

---

## App Repo: Pipeline Trigger Flow

### Automatic (on push to `main`)

1. **CodeStar** immediately triggers **both** `AiSocialMediaBackendPipeline` and `AiSocialMediaFrontendPipeline`.
2. **GitHub Actions** (`deploy-on-main.yml`) runs concurrently and classifies the push:

| Changed paths | Backend Pipeline | Frontend Pipeline |
|--------------|-----------------|-------------------|
| Only `web/` | **Stopped** | Runs |
| Only `cmd/`, `internal/`, `go.*`, `Dockerfile*` | Runs | **Stopped** |
| Both | Runs | Runs |
| Other (docs, scripts, config) | **Stopped** | **Stopped** |

3. The unnecessary pipeline is stopped via `aws codepipeline stop-pipeline-execution` within ~15 seconds.

### Manual (workflow_dispatch)

Go to **Actions > Intelligent Pipeline Trigger > Run workflow** and select:
- `auto-detect` — analyzes HEAD commit
- `backend-only` — triggers backend pipeline only
- `frontend-only` — triggers frontend pipeline only
- `both` — triggers both pipelines

### What each pipeline does

**Backend Pipeline** (`AiSocialMediaBackendPipeline`):
1. **Source** — pulls `main` from GitHub via CodeStar
2. **Build** — builds 11 Docker images in 3 parallel waves:
   - Wave 1: API, Triage, Description, Download, Publish (light images, ~30s each)
   - Wave 2: Enhancement, Webhook, OAuth (light images)
   - Wave 3: Thumbnail, Selection, Video (heavy images with ffmpeg, ~90s each)
   - Conditional rebuilds: only changed Lambdas are rebuilt (SSM tracks last build commit)
3. **Deploy** — updates all 11 Lambda functions with new image URIs, waits for completion

**Frontend Pipeline** (`AiSocialMediaFrontendPipeline`):
1. **Source** — pulls `main` from GitHub via CodeStar
2. **Build** — `npm ci && npm run build` (Preact SPA with Vite, Node 22)
3. **Deploy** — S3 sync + CloudFront invalidation

---

## Deploy Repo: CDK Deploy Flow

### Automatic (on push to `main`, path `cdk/**`)

1. **Preflight** job:
   - `tsc --noEmit` — TypeScript strict compilation
   - `cdk synth --all` — synthesize CloudFormation templates
   - `validate-cdk.sh` — custom lint for known failure patterns
2. **Deploy** job (only if preflight passes):
   - `cdk deploy` — core stacks by default (Storage, Registry, Backend, Webhook)

### Manual (workflow_dispatch)

Go to **Actions > CDK Deploy > Run workflow** and select a target:

| Target | Stacks | Use when |
|--------|--------|----------|
| `core` | Storage, Registry, Backend, Webhook | Default — daily changes |
| `full` | All 10 stacks | First deploy or major infra changes |
| `pipelines` | BackendPipeline, FrontendPipeline | Pipeline config changes |
| `edge` | Frontend | CloudFront/CDN changes |
| `observability` | OpsAlert, OpsMonitoring, OpsDashboard | Alarms/dashboard changes |

### Local deploy (Makefile)

For iteration or emergency deploys, use the Makefile in `cdk/`:

```bash
cd cdk
make deploy           # Core stacks (default)
make deploy-full      # All stacks
make deploy-backend   # Single stack
make deploy-dev       # Hotswap mode (fast, skips CloudFormation)
make diff             # Preview changes
make synth            # Synthesize templates
```

---

## Stack Deployment Order

Stacks must deploy in dependency order. CDK enforces this via `addDependency()`.

```
1. StorageStack           (stateful: S3 buckets, DynamoDB — DDR-045)
2. RegistryStack          (ECR repos — DDR-046)
3. BackendStack           (9 Lambdas, API Gateway, Cognito, Step Functions)
4. RagStack               (Aurora Serverless v2, EventBridge, SQS, 5 RAG Lambdas — DDR-066)
5. FrontendStack          (CloudFront + OAC)
6. WebhookStack           (Meta webhook + OAuth Lambdas — DDR-044)
7. FrontendPipelineStack  (CodePipeline for Preact SPA)
8. BackendPipelineStack   (CodePipeline for 11 Docker images)
9. OperationsAlertStack   (CloudWatch alarms, SNS)
10. OperationsMonitoringStack (Metric filters, Firehose, Glue)
11. OperationsDashboardStack (CloudWatch dashboard)
```

**Rule:** StorageStack and RegistryStack must deploy before everything else. They hold stateful resources that other stacks reference.

**RAG stack (DDR-066):** The first-time RAG deployment and any change that adds or modifies the RAG stack (e.g. new Lambdas, Aurora, EventBridge rules) **must be deployed manually**. Use **Actions > CDK Deploy > Run workflow** and choose a target that includes the RAG stack (e.g. `full`), or run `cdk deploy RagStack` locally. Do not rely on the default automatic CDK deploy for introducing the RAG stack.

---

## Pre-Push Validation (C3)

Both repos have `.githooks/pre-push` hooks that run before pushes to `main`.

**Install hooks:**

```bash
# App repo
cd ai-social-media-helper && .githooks/setup.sh

# Deploy repo
cd ai-social-media-helper-deploy && .githooks/setup.sh
```

**App repo checks:**
- `go vet ./...` — static analysis
- `go build ./cmd/...` — compile all 14 commands
- Frontend `npm run build` — Preact SPA build
- Secret scan — AWS keys, API keys, credentials

**Deploy repo checks:**
- `tsc --noEmit` — TypeScript strict compilation
- `cdk synth --all` — CloudFormation synthesis
- `cdk diff --all` — preview changes (informational)
- `validate-cdk.sh` — known failure pattern checks
- Secret scan

**Bypass (emergency):** `git push --no-verify`

---

## AWS Authentication

| Context | Method | Details |
|---------|--------|---------|
| GitHub Actions | Access keys (D1) | `AWS_ACCESS_KEY_ID` + `AWS_SECRET_ACCESS_KEY` in GitHub Secrets |
| Local development | AWS CLI profile | IAM user `boyshawn` with scoped policies |
| CodePipeline | IAM roles | Service roles created by CDK |

**IAM permission groups** (DDR-023):
- `AiSocialMedia-Infra-Core` — CloudFormation, S3, DynamoDB, Lambda, API Gateway
- `AiSocialMedia-Compute` — ECR, CodeBuild, Step Functions
- `AiSocialMedia-IAM` — IAM role/policy management
- `AiSocialMedia-CICD-CDN` — CodePipeline, CloudFront, CodeStar

---

## Common Scenarios

### "I changed only a Go handler"

1. Push to `main`
2. CodeStar triggers both pipelines
3. GitHub Actions detects backend-only change, stops frontend pipeline
4. Backend pipeline rebuilds only the changed Lambda's Docker image (conditional build)
5. Lambda updated in ~3-5 minutes

### "I changed only CSS or a React component"

1. Push to `main`
2. CodeStar triggers both pipelines
3. GitHub Actions detects frontend-only change, stops backend pipeline
4. Frontend pipeline builds SPA, deploys to S3, invalidates CloudFront
5. Live in ~2-3 minutes

### "I changed a CDK stack definition"

1. Push to `main` in deploy repo
2. GitHub Actions runs preflight (tsc, synth, validate)
3. CDK deploys the affected stacks
4. If pipeline stacks changed, re-deploy pipeline stacks to pick up new configuration

### "I need to deploy everything from scratch"

```bash
# Option 1: GitHub Actions
# Go to Actions > CDK Deploy > Run workflow > target: full

# Option 2: Local
cd ai-social-media-helper-deploy/cdk
make deploy-full
```

---

## Troubleshooting

| Symptom | Likely cause | Fix |
|---------|-------------|-----|
| Both pipelines run on every push | GitHub Actions failed or timed out | Check Actions tab; CodeStar triggers are the safe fallback |
| Pipeline stuck in "Stopping" | Stop was called during Build stage | Wait for current CodeBuild phase to finish, or abandon execution |
| CDK deploy fails with "already exists" | Orphaned resource from partial deploy | See [CDK Rollback Recovery](./operations/cdk-rollback-recovery.md) |
| Pre-push hook too slow | Frontend `npm ci` on first run | Run `cd web/frontend && npm ci` once; subsequent runs use cache |
| `tsc --noEmit` fails in deploy workflow | TypeScript error in CDK code | Fix locally, push again |
| Pipeline source fails | CodeStar connection expired | Re-authorize in AWS Console > Developer Tools > Connections |

---

## Related Documents

- [DDR-055: Deployment Automation](./design-decisions/DDR-055-deployment-automation.md) — design decision
- [CDK Rollback Recovery](./operations/cdk-rollback-recovery.md) — operator runbook
- [DDR-023: IAM Deployment User](./design-decisions/DDR-023-aws-iam-deployment-user.md) — IAM setup
- [DDR-035: Multi-Lambda Deployment](./design-decisions/DDR-035-multi-lambda-deployment.md) — pipeline architecture
- [DDR-045: Stateful/Stateless Split](./design-decisions/DDR-045-stateful-stateless-stack-split.md) — stack strategy
- [DDR-047: CDK Deploy Optimization](./design-decisions/DDR-047-cdk-deploy-optimization.md) — Makefile targets and speed
