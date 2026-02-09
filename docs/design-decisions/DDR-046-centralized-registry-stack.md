# DDR-046: Centralized RegistryStack for ECR Repositories

**Date**: 2026-02-09  
**Status**: Accepted  
**Iteration**: Phase 2 Cloud Deployment

## Context

Any CDK stack that creates both an ECR repository and a `DockerImageFunction` referencing that repository will fail on first deploy. CloudFormation creates the ECR repo, then tries to create the Lambda with a tag that does not exist yet (e.g., `webhook-latest`). The Lambda creation fails, and CloudFormation rolls back — deleting the ECR repo along with it.

This chicken-and-egg problem affected WebhookStack (DDR-044) on its first deployment: the `ai-social-media-webhook` repo was created, but the Lambda referencing `webhook-latest` failed because no image had been pushed yet.

BackendStack avoided this in practice because its 5 Lambdas share only 2 ECR repos (`light` and `heavy`), and placeholder tags were already present from earlier pipeline runs. But this is an implicit ordering that does not generalize — any new Lambda with a new ECR repo hits the same wall.

### Previous ECR repo ownership

| ECR Repository | Owning Stack | Type |
|----------------|-------------|------|
| `ai-social-media-lambda-light` | BackendStack | Private |
| `ai-social-media-lambda-heavy` | BackendStack | Private |
| `ai-social-media-lambda-light` | BackendStack | Public |
| `ai-social-media-lambda-heavy` | BackendStack | Public |
| `ai-social-media-webhook` | WebhookStack (via `fromRepositoryName`) | Private |

## Decision

Create a dedicated **RegistryStack** that owns all ECR repositories (private and public). RegistryStack deploys before any application stack and contains no Lambda functions, so ECR repos are always created cleanly. Application stacks (BackendStack, WebhookStack, future stacks) receive repos as cross-stack props.

### After

```
RegistryStack (deploys first, no Lambdas)
  → ECR Private: lambda-light, lambda-heavy, webhook
  → ECR Public: lambda-light, lambda-heavy

StorageStack (stateful, termination-protected — DDR-045)
  → S3 buckets, DynamoDB table

BackendStack (receives ECR repos as props)
  → 5 Lambdas, API Gateway, Cognito, Step Functions

WebhookStack (receives ECR repo as prop)
  → 1 Lambda, API Gateway, CloudFront

BackendPipelineStack (receives ECR repos and Lambdas as props)
  → CodePipeline, CodeBuild
```

### Bootstrap procedure for new Lambdas

1. Add `new ecr.Repository(...)` in RegistryStack, expose as public property
2. `cdk deploy AiSocialMediaRegistry` (creates the empty repo)
3. Build and push one seed image locally:
   ```bash
   aws ecr get-login-password | docker login --username AWS --password-stdin <account>.dkr.ecr.<region>.amazonaws.com
   docker build --build-arg CMD_TARGET=<lambda-name> -t <repo-uri>:<tag> -f cmd/media-lambda/Dockerfile.light .
   docker push <repo-uri>:<tag>
   ```
4. Add the Lambda in its application stack, referencing the repo as a prop
5. `cdk deploy <ApplicationStack>`
6. Pipeline takes over image management after first run

## Rationale

- **Breaks the circular dependency** — ECR repos are created in a stack with no Lambdas, so `cdk deploy RegistryStack` always succeeds, even with empty repos.
- **Repos survive application stack rollbacks** — If WebhookStack or BackendStack fails during deployment, the ECR repos and their images are unaffected.
- **Generic pattern** — Every future Lambda follows the same 5-step bootstrap procedure. No ad-hoc workarounds.
- **Single source of truth** — All ECR repos in one stack, easy to audit lifecycle rules and access policies.
- **Consistent with DDR-045** — Follows the same principle (durable resources in dedicated stacks), applied to container registries.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Keep ECR repos in application stacks | Causes chicken-and-egg failure on first deploy of any new Lambda + repo |
| Move ECR repos to StorageStack | Mixes data stores with container registries; StorageStack already has 7 S3 buckets + DynamoDB |
| Manual `fromRepositoryName()` | ECR repo not managed by CDK (no lifecycle rules, no IaC); manual process per new Lambda |
| Two-phase CDK deploy with context flag | Fragile, unusual, risk of accidental bootstrap mode |
| `DockerImageCode.fromImageAsset()` | Rebuilds Docker images on every `cdk deploy`; dual ECR repos per Lambda; requires Docker locally |

## Consequences

**Positive:**

- First-deploy failures caused by missing ECR images are eliminated
- All ECR lifecycle rules and access policies are centrally managed
- Application stacks can be freely destroyed and redeployed without losing container images
- Future Lambdas follow a documented, repeatable bootstrap pattern

**Trade-offs:**

- One additional stack in the deployment chain (8 stacks total)
- Cross-stack references from BackendStack and WebhookStack to RegistryStack
- One-time manual image push per new ECR repo (scripted but not zero-touch)
- Cannot delete RegistryStack without first deleting all stacks that reference it

## Implementation

| File | Change |
|------|--------|
| `cdk/lib/registry-stack.ts` | **New** — 3 private ECR repos + 2 public ECR repos |
| `cdk/lib/backend-stack.ts` | Remove 4 ECR repo declarations; accept repos as props |
| `cdk/lib/webhook-stack.ts` | Remove `fromRepositoryName()`; accept `webhookEcrRepo` as prop |
| `cdk/bin/cdk.ts` | Add RegistryStack; wire repos to BackendStack and WebhookStack |
| `cdk/lib/backend-pipeline-stack.ts` | No change (already receives repos as props via `IRepository`) |

## Related Documents

- [DDR-035: Multi-Lambda Deployment](./DDR-035-multi-lambda-deployment.md) — Original multi-Lambda architecture
- [DDR-041: Container Registry Strategy](./DDR-041-container-registry-strategy.md) — Private/public ECR split
- [DDR-044: Instagram Webhook Integration](./DDR-044-instagram-webhook-integration.md) — WebhookStack design
- [DDR-045: Stateful/Stateless Stack Split](./DDR-045-stateful-stateless-stack-split.md) — Same pattern for S3/DynamoDB
