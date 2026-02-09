# DDR-045: Stateful/Stateless Stack Split

**Date**: 2026-02-09  
**Status**: Accepted  
**Iteration**: Phase 2 Cloud Deployment

## Context

All S3 buckets in the deploy project use explicit physical names (e.g., `ai-social-media-uploads-{account}`). The DynamoDB table also uses an explicit name (`media-selection-sessions`). When a `cdk deploy` fails partway through, CloudFormation rolls back but may leave the resource orphaned — it exists in AWS but is not tracked by any CloudFormation stack. The next deploy attempt fails because a resource with that name already exists.

Previously, S3 buckets were scattered across multiple stacks:

| Resource | Stack | Removal Policy |
|----------|-------|----------------|
| Media uploads bucket | StorageStack | DESTROY |
| Sessions table (DynamoDB) | StorageStack | DESTROY |
| Frontend assets bucket | FrontendStack | DESTROY |
| Log archive bucket | OperationsStack | RETAIN |
| Metrics archive bucket | OperationsStack | RETAIN |
| BE pipeline artifacts bucket | BackendPipelineStack | DESTROY |
| FE pipeline artifacts bucket | FrontendPipelineStack | DESTROY |

When a complex stateless stack (e.g., FrontendStack with CloudFront, OperationsStack with 17 alarms) failed during deployment, any S3 buckets it created could become orphaned. The RETAIN-policy buckets in OperationsStack were especially problematic — even a successful stack deletion leaves them behind, blocking redeployment.

## Decision

Consolidate all stateful resources (S3 buckets, DynamoDB tables) into StorageStack. All other stacks become purely stateless — they reference buckets/tables via CDK cross-stack props but never create them.

### Before

```
StorageStack           → media bucket, sessions table
FrontendStack          → frontend bucket, CloudFront
BackendPipelineStack   → BE artifacts bucket, CodePipeline
FrontendPipelineStack  → FE artifacts bucket, CodePipeline
OperationsStack        → log archive bucket, [metrics archive bucket], alarms, dashboard
```

### After

```
StorageStack (stateful, termination-protected)
  → media bucket, sessions table
  → frontend bucket
  → log archive bucket, [metrics archive bucket]
  → BE artifacts bucket, FE artifacts bucket

FrontendStack          → CloudFront (receives frontend bucket as prop)
BackendPipelineStack   → CodePipeline (receives BE artifacts bucket as prop)
FrontendPipelineStack  → CodePipeline (receives FE artifacts bucket as prop)
OperationsStack        → alarms, dashboard, Firehose (receives log/metrics buckets as props)
```

### Key Changes

1. **StorageStack** gains 5 new resources: frontend bucket, log archive bucket, metrics archive bucket (optional), BE artifacts bucket, FE artifacts bucket. Termination protection is enabled.

2. **FrontendStack** receives `frontendBucket` as a prop instead of creating it. CloudFront OAC setup uses the cross-stack bucket reference.

3. **BackendPipelineStack** and **FrontendPipelineStack** receive `artifactBucket` as a prop instead of creating it.

4. **OperationsStack** receives `logArchiveBucket` and optional `metricsArchiveBucket` as props. Firehose and MetricStream resources remain in OperationsStack (stateless compute that references the stateful bucket).

5. **bin/cdk.ts** wires the new cross-stack references and adds explicit dependency from FrontendStack on StorageStack.

## Rationale

- **Immunity to rollbacks** — If a stateless stack deployment fails (common for complex stacks like Operations or Frontend), no S3 buckets are affected because they live in StorageStack, which was already deployed and stable.
- **Faster stateless deploys** — Stateless stacks no longer check/update S3 bucket configurations on every deployment.
- **Data safety** — Termination protection on StorageStack prevents accidental deletion of production data via `cdk destroy`.
- **Single deployment target** — StorageStack is deployed once during initial setup and rarely changes. Day-to-day changes only affect stateless stacks.
- **Industry best practice** — Separating stateful and stateless resources is the standard enterprise pattern for CloudFormation/CDK projects.

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Remove explicit bucket names (Option A) | Bucket names become unpredictable; harder to identify in AWS console; requires full tear-down to adopt |
| Pre-deploy cleanup script (Option B) | Operational overhead — developers must remember to run the script; does not prevent the problem |
| Hybrid naming (Option C) | Mixed naming strategy is confusing; still requires a cleanup script |
| Self-healing custom resource (Option E) | Over-engineering — Lambda managing infrastructure deployment adds complexity and security risk |
| Dynamic hash naming (Option F) | Garbage accumulation of orphaned buckets; data fragmentation between failed and successful deploys |

## Consequences

**Positive:**

- Partial deployment failures in stateless stacks no longer orphan S3 buckets
- StorageStack changes are rare, minimizing the window for storage-layer failures
- Termination protection prevents accidental `cdk destroy` from deleting data
- Cross-stack references are type-safe via CDK props (no string-based lookups)
- Consistent pattern: all stateful resources in one place, all compute in others

**Trade-offs:**

- Initial migration requires deleting all existing stacks and redeploying (one-time cost)
- StorageStack becomes larger (7 S3 buckets + 1 DynamoDB table)
- Cross-stack references create CloudFormation exports, which are immutable once referenced — changing a bucket property in StorageStack requires updating all dependent stacks
- Cannot delete StorageStack without first deleting all stacks that reference it

## Implementation

| File | Change |
|------|--------|
| `cdk/lib/storage-stack.ts` | Add 5 S3 buckets, enable termination protection |
| `cdk/lib/frontend-stack.ts` | Receive `frontendBucket` as prop |
| `cdk/lib/backend-pipeline-stack.ts` | Receive `artifactBucket` as prop |
| `cdk/lib/frontend-pipeline-stack.ts` | Receive `artifactBucket` as prop |
| `cdk/lib/operations-stack.ts` | Receive `logArchiveBucket` and `metricsArchiveBucket` as props |
| `cdk/bin/cdk.ts` | Wire new cross-stack references, add storage dependency |
| `cdk/test/cdk.test.ts` | Update test to match new stack structure |

## Related Documents

- [DDR-035: Multi-Lambda Deployment](./DDR-035-multi-lambda-deployment.md) — Original stack structure
- [DDR-028: Security Hardening](./DDR-028-security-hardening.md) — S3 CORS and access controls
- [DDR-039: DynamoDB Session Store](./DDR-039-dynamodb-session-store.md) — Sessions table design
- [DDR-044: Instagram Webhook Integration](./DDR-044-instagram-webhook-integration.md) — WebhookStack (no S3, unaffected)
