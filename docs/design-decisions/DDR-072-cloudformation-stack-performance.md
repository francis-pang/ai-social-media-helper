# DDR-072: CloudFormation Stack Performance Analysis

**Date**: 2026-02-28  
**Status**: Accepted  
**Iteration**: Cloud — infrastructure performance

## Context

With 11 CDK stacks and 95 CloudFormation resources in the largest stack (`AiSocialMediaOperationsMonitoring`), a full `cdk deploy --all` was perceived as slow. Recent deploys of `AiSocialMediaFrontend` took ~17 minutes wall time, raising concerns about stack sizes approaching CloudFormation limits and whether the architecture needed further splitting.

A systematic analysis was performed using AWS CLI (`list-stack-resources`, `describe-stack-events`) and cross-referenced against AWS CloudFormation best practices to determine:

1. Whether any stack is approaching the 500-resource CloudFormation limit
2. What actually causes slow deploys
3. Whether stack splitting or consolidation is warranted

## Findings

### Resource counts and limits

| Stack | Resources | % of 500 limit | Template size |
|-------|-----------|-----------------|---------------|
| AiSocialMediaOperationsMonitoring | 95 | 19% | 124 KB |
| AiSocialMediaBackend | 63 | 12.6% | 176 KB |
| AiSocialMediaRag | 42 | 8.4% | — |
| AiSocialMediaStorage | 28 | 5.6% | — |
| AiSocialMediaWebhook | 23 | 4.6% | — |
| AiSocialMediaBackendPipeline | 16 | 3.2% | — |
| AiSocialMediaFrontendPipeline | 16 | 3.2% | — |
| AiSocialMediaFrontend | 8 | 1.6% | 18 KB |
| AiSocialMediaRegistry | 7 | 1.4% | — |
| AiSocialMediaOperationsAlert | 3 | 0.6% | — |
| AiSocialMediaOperationsDashboard | 2 | 0.4% | — |

No stack is near the 500-resource or 1 MB template limit. The heaviest stack uses only 19% of the resource limit and 12% of the template size limit.

### Root cause: slow deploys are resource-type driven, not size driven

The Frontend stack's ~17-minute deploy on 2026-03-01 was **not** caused by stack size (8 resources). Event log analysis revealed 4 deployment attempts:

1. **03:12:11** — FAILED (5s): `Secrets Manager can't find the specified secret` — BackendStack had already deleted `OriginVerifySecret`
2. **03:16:14** — FAILED (5s): Same Secrets Manager error
3. **03:18:08** — FAILED (7s): `You can't remove or replace the web ACL` — WAF pricing plan constraint during the migration
4. **03:21:01** — SUCCEEDED (7m 52s): CloudFront distribution propagated to all edge locations

The 3 failed attempts each triggered automatic rollbacks (~2 min each), adding ~9 minutes of overhead. The successful deploy's 7m 52s was entirely one resource — `Distribution830FAC52` — propagating to 600+ CloudFront edge locations. Every other resource updated in under 2 seconds.

### Per-stack update speeds (when healthy)

| Stack | Typical update time | Bottleneck |
|-------|---------------------|------------|
| OperationsMonitoring | 8–14s | MetricFilter/SubscriptionFilter are near-instant |
| Backend | 18–44s | IAM policy propagation (~16s per batch) |
| Rag | ~2 min | Aurora Serverless v2 + VPC resources |
| Storage | ~33s | S3 configuration checks |
| Frontend | 7–8 min | CloudFront global propagation (AWS SLA) |
| All others | 6–8s | Lightweight resources |

### Growth rates per new Lambda

| Stack | Resources per new Lambda | Hits 500 limit at |
|-------|--------------------------|-------------------|
| OperationsMonitoring | +8 (6 MetricFilters + 2 SubscriptionFilters) | ~60 Lambdas |
| Backend | +5–6 (Function + LogGroup + Role + 2–3 Policies) | ~80 Lambdas |

Neither is a concern at the current scale of 10 Lambdas.

### OperationsMonitoring resource composition

- 60 `AWS::Logs::MetricFilter` — 6 per Lambda (Error, Fatal, RateLimit, Timeout, Auth, ColdStart)
- 20 `AWS::Logs::SubscriptionFilter` — 2 per Lambda (INFO+ and DEBUG Firehose streams)
- 15 base resources (Firehose streams, IAM, Glue, MetricStream)

MetricFilters cannot be consolidated: each is 1:1 with a log group + filter pattern. SubscriptionFilters are already at the CloudWatch Logs limit of 2 per log group. Lambda Insights can partially replace ColdStart detection but not application-level log patterns.

### Backend resource composition

- 13 IAM Roles (9 Lambda + 4 Step Functions)
- 13 IAM Policies (merged via `minimizePolicies: true`)
- 10 LogGroups (9 Lambda + 1 API Gateway)
- 9 Lambda Functions
- 4 Step Functions StateMachines
- API Gateway + Cognito + SSM

CDK already optimizes policies via `minimizePolicies: true` and `createNewPoliciesWithAddToRolePolicy: false`. Shared IAM roles could save ~12–14 resources but violate least-privilege.

## Decision

### 1. No stack splitting

All stacks are well within CloudFormation limits. The largest (95 resources) is at 19% of the 500-resource limit. Splitting would add complexity without solving the actual performance bottlenecks (CloudFront propagation, IAM policy propagation, Aurora updates).

Revisit only if projecting 40+ Lambdas, which would put OperationsMonitoring near 335 resources.

### 2. Deploy selectively and in parallel

The Makefile (DDR-047) already provides optimized deployment targets:

- `make deploy` (core stacks only, `--concurrency 3`) for daily work — avoids CloudFront
- `make deploy-edge` for Frontend-only changes (accepts the 8-min CloudFront wait)
- `make deploy-full` (`--concurrency 5`, all stacks) for infrastructure changes
- Per-stack targets with `--exclusively` for single-stack iteration

### 3. Cross-stack migration ordering

When migrating shared configuration between stacks (e.g., OriginVerifySecret from Secrets Manager to SSM), the deployment order must account for the `addDependency` graph — BackendStack deploys before FrontendStack. Migrating in a single deploy will fail if BackendStack removes a resource that FrontendStack still references.

Safe migration order for shared configuration:

1. Create the new configuration source (e.g., SSM parameter) manually or via a separate stack
2. Update **consumers** (e.g., FrontendStack) to read from the new source
3. Deploy consumers: `make deploy-frontend` (or include in `deploy-full`)
4. Update **producers** (e.g., BackendStack) to stop creating the old source
5. Deploy producers: `make deploy-backend`

For multi-stack migrations within a single `cdk deploy --all`, consider a two-phase approach: first deploy adds the new source alongside the old one, second deploy removes the old one.

### 4. Accept inherent resource-type latencies

Some update times are AWS constraints, not design flaws:

- **CloudFront distributions**: 5–15 min per update (edge propagation to 600+ PoPs)
- **IAM policy propagation**: ~16s per policy batch (global consistency)
- **Aurora Serverless v2**: 1–3 min per update (cluster configuration)

## Rationale

- AWS CloudFormation best practices recommend organizing stacks by **lifecycle and ownership**, not by resource count — which this architecture already does (DDR-045, DDR-047)
- SSM parameters for cross-stack communication instead of CF exports avoids hard coupling — already in place
- The 95-resource stack deploys in 14 seconds when adding resources, proving resource count does not determine deploy speed
- CloudFront is the single largest contributor to deploy time, and it cannot be optimized — only avoided via selective deploys

## Consequences

**Positive:**

- Confirms no infrastructure restructuring needed — focus remains on feature work
- Documents growth ceilings so future Lambda additions can be planned against known limits
- Cross-stack migration ordering guidance prevents the class of failures seen on 2026-03-01

**Trade-offs:**

- Frontend deploys remain slow (~8 min) by nature — no fix exists
- OperationsMonitoring will need splitting if Lambda count grows past ~50 (unlikely near-term)
- Cross-stack migrations require manual multi-phase deployments

## Related Documents

- [DDR-045: Stateful/Stateless Stack Split](./DDR-045-stateful-stateless-stack-split.md) — Stack separation pattern
- [DDR-046: Centralized RegistryStack](./DDR-046-centralized-registry-stack.md) — ECR repo ownership
- [DDR-047: CDK Deploy Optimization](./DDR-047-cdk-deploy-optimization.md) — Makefile, concurrency, Operations split
- [DDR-062: Observability and Version Tracking](./DDR-062-observability-and-version-tracking.md) — Logging infrastructure
