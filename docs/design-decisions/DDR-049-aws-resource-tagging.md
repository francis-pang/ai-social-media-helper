# DDR-049: AWS Resource Tagging for Cost Tracking

**Date**: 2026-02-09  
**Status**: Accepted  
**Iteration**: Phase 2 Cloud Deployment

## Context

The system spans 9 CDK stacks with 80+ AWS resources (Lambdas, S3 buckets, DynamoDB, CloudFront, API Gateway, Step Functions, ECR repositories, CodePipeline, CloudWatch, IAM roles, etc.). Without resource tags, there is no way to isolate this system's costs in AWS Cost Explorer — its spend is mixed into the account-wide total.

AWS Cost Allocation Tags allow filtering billing data by tag. Once a tag is activated in the Billing console, Cost Explorer can group and filter costs by that tag, providing a clear picture of how much this system costs per month.

## Decision

Apply a single tag to every resource across all 9 stacks:

```
Project = ai-social-media-helper
```

Use CDK's `Tags.of(app).add()` at the app construct level in `cdk/bin/cdk.ts`. This propagates the tag to every resource in every stack via CDK's tag inheritance mechanism. The existing `cdk.json` feature flag `@aws-cdk/core:explicitStackTags: true` ensures tags are explicitly written to every CloudFormation resource.

```typescript
const app = new cdk.App();
cdk.Tags.of(app).add('Project', 'ai-social-media-helper');
```

## Rationale

| Factor | Details |
|---|---|
| Single point of change | One line in `cdk.ts` tags all 80+ resources across 9 stacks |
| CDK tag inheritance | `Tags.of(app)` cascades to every child construct automatically |
| Cost Explorer integration | `Project` tag can be activated as a Cost Allocation Tag in AWS Billing |
| No per-resource changes | No need to modify individual stack files or resource definitions |
| CloudFormation support | Tags propagate through CloudFormation natively; most AWS resources support tags |

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Tag per stack (`Tags.of(stack)`) | More code, same result — app-level is simpler and ensures nothing is missed |
| Multiple tags (Project + Environment + Owner) | Over-engineering for a single-account, single-environment system; one tag is sufficient for cost isolation |
| AWS Organizations tag policies | Requires AWS Organizations setup; overkill for personal account |
| Manual tagging via AWS Console | Does not persist across CDK deployments; CloudFormation overwrites manual tags |

## Consequences

**Positive:**

- Total system cost visible in AWS Cost Explorer by filtering `Project = ai-social-media-helper`
- All future resources added to any stack automatically inherit the tag
- Zero runtime impact — tags are metadata only

**Trade-offs:**

- The `Project` tag must be activated as a Cost Allocation Tag in the AWS Billing console (one-time manual step)
- Cost data for the new tag only appears from the activation date forward — it is not retroactive
- A small number of AWS resource types do not support tags (e.g., some CloudWatch resources); these will appear untagged in Cost Explorer

## Implementation

| File | Change |
|------|--------|
| `cdk/bin/cdk.ts` | Add `cdk.Tags.of(app).add('Project', 'ai-social-media-helper')` after app creation |

### Post-Deploy Steps

1. Go to **AWS Billing** > **Cost Allocation Tags**
2. Find the `Project` tag under **User-defined cost allocation tags**
3. Select it and click **Activate**
4. Cost Explorer will show data for this tag within 24 hours

## Related Documents

- [DDR-045: Stateful/Stateless Stack Split](./DDR-045-stateful-stateless-stack-split.md) — Stack separation pattern
- [DDR-047: CDK Deploy Optimization](./DDR-047-cdk-deploy-optimization.md) — Deploy flags and stack organization
