# DDR-047: CDK Deploy Optimization

**Date**: 2026-02-09  
**Status**: Accepted  
**Iteration**: Phase 2 Cloud Deployment

## Context

With 8 CDK stacks (Storage, Registry, Backend, Frontend, FrontendPipeline, BackendPipeline, Webhook, Operations) and 6 Docker-image Lambda functions, deployment times were dominated by three bottlenecks:

1. **Sequential Docker builds in CodeBuild** — all 6 images built one after another with no layer caching (~10-15 min per pipeline run)
2. **No CDK deploy flags** — every deploy creates change sets, runs stacks sequentially, and deploys all stacks even when only one changed
3. **Monolithic OperationsStack** — ~70-75 CloudFormation resources in a single stack, making alarm threshold tweaks take as long as full observability reconfigurations

Additionally, `ts-node` was used for CDK synthesis (slower than alternatives), and there was no local workflow for pushing Lambda code changes without waiting for the full CodePipeline.

## Decision

Apply a four-tier optimization strategy:

### Tier 1: CDK Deploy Flags + Synth Acceleration

- Add a **Makefile** with per-stack targets using `--method=direct` (skip change sets), `--concurrency 3` (parallel stacks), `--hotswap` (dev mode), and `--exclusively` (skip unchanged dependencies)
- Replace `ts-node` with **`tsx`** (esbuild-based, 2-5x faster TypeScript transpilation)
- Enable **incremental TypeScript compilation** (`incremental: true` in tsconfig)

### Tier 2: CodeBuild Pipeline Optimization

- Enable **Docker BuildKit** (`DOCKER_BUILDKIT=1`) and **`--cache-from`** to reuse layers from previous `:latest` images
- Add **CodeBuild S3 cache** for Go module and build caches
- **Parallelize Docker builds** using shell background processes in 3 waves (light, heavy, webhook)
- **Parallelize ECR pushes** in the post_build phase

### Tier 3: OperationsStack Split

Split the monolithic OperationsStack (~940 lines, ~70-75 resources) into two stacks:

- **OperationsAlertStack** (~25 resources): alarms, SNS topic, X-Ray tracing — changes often
- **OperationsMonitoringStack** (~45 resources): metric filters, subscription filters, Firehose, dashboard, Glue — changes rarely

Both depend on BackendStack and StorageStack but not on each other, so they deploy in parallel with `--concurrency 3`.

### Tier 4: Dockerfile + Local Dev Workflow

- Add **BuildKit cache mounts** (`--mount=type=cache`) in Dockerfiles for Go module and build caches
- Add **local Lambda quick-push** Makefile targets that build, push to ECR, and update a single Lambda function in ~1-2 minutes (bypassing CodePipeline)

## Rationale

| Optimization | Expected Impact |
|---|---|
| `--method=direct` | ~30-60s saved per stack (no change set creation) |
| `--exclusively` + per-stack targets | ~80% faster for single-stack changes |
| `tsx` over `ts-node` | 2-5x faster synth |
| Parallel Docker builds (3 waves) | Build time from ~10-15 min to ~3-5 min |
| `--cache-from` + BuildKit | ~30-50% faster per image when layers cached |
| CodeBuild S3 cache | Go modules cached across pipeline runs |
| OperationsStack split | Alarm changes ~60% faster (2 min vs 5-8 min) |
| Local Lambda push | Code iteration from ~5+ min to ~1-2 min |

Combined: most common dev scenario (Lambda code change) goes from ~15-20 min to ~1-2 min.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| CDK Pipelines (self-mutating) | Overkill for single-account, single-region; adds CloudFormation churn per commit |
| Separate CodeBuild project per image | More infrastructure to manage; background shell processes achieve same parallelism |
| SAM local invoke for dev | Doesn't support DockerImageFunction well; local push to real Lambda is more representative |
| Nested stacks for OperationsStack | Cannot be independently deployed; must update parent stack |
| Skip CDK entirely (raw CloudFormation) | Loses type safety, construct abstractions, and cross-stack references |

## Consequences

**Positive:**

- Full deploy ~40-50% faster; single-stack changes ~80% faster
- CodeBuild pipeline time reduced ~60-70% (parallel builds + caching)
- Lambda code iteration reduced from ~5+ min to ~1-2 min with local push
- Alarm-only changes isolated to a fast-deploying stack

**Trade-offs:**

- One additional stack (9 total after OperationsStack split)
- `--method=direct` skips change set review (use `cdk diff` to preview)
- `--hotswap` should not be used in production (skips CloudFormation drift detection)
- Parallel Docker builds require CodeBuild MEDIUM compute or larger
- Local push uses `:dev` tags that differ from pipeline `:commit-hash` tags

## Implementation

| File | Change |
|------|--------|
| `cdk/Makefile` | **New** — deploy, deploy-{stack}, deploy-dev, watch, synth, diff targets |
| `cdk/cdk.json` | Replace `ts-node` with `tsx` |
| `cdk/package.json` | Add `tsx`, remove `ts-node` |
| `cdk/tsconfig.json` | Add `incremental: true` |
| `cdk/.gitignore` | Add `.tsbuildinfo` |
| `cdk/lib/backend-pipeline-stack.ts` | BuildKit, --cache-from, S3 cache, parallel builds/pushes |
| `cdk/lib/operations-alert-stack.ts` | **New** — alarms, SNS, X-Ray (split from operations-stack.ts) |
| `cdk/lib/operations-monitoring-stack.ts` | **New** — metric filters, Firehose, dashboard, Glue (split from operations-stack.ts) |
| `cdk/lib/operations-stack.ts` | **Deleted** — replaced by the two stacks above |
| `cdk/bin/cdk.ts` | Wire new OperationsAlert + OperationsMonitoring stacks |
| `cmd/media-lambda/Dockerfile.light` | BuildKit cache mounts for Go modules and build cache |
| `cmd/media-lambda/Dockerfile.heavy` | BuildKit cache mounts for Go modules and build cache |
| `Makefile` (app repo) | Add push-api, push-thumbnail, etc. local Lambda quick-push targets |

## Related Documents

- [DDR-035: Multi-Lambda Deployment](./DDR-035-multi-lambda-deployment.md) — Original multi-Lambda architecture
- [DDR-041: Container Registry Strategy](./DDR-041-container-registry-strategy.md) — ECR private/public split
- [DDR-045: Stateful/Stateless Stack Split](./DDR-045-stateful-stateless-stack-split.md) — Stack separation pattern
- [DDR-046: Centralized RegistryStack](./DDR-046-centralized-registry-stack.md) — ECR repo ownership
