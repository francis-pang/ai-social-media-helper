# DDR-023: AWS IAM User and Scoped Policies for CDK Deployment

**Date**: 2026-02-06  
**Status**: Accepted  
**Iteration**: 14

## Context

Phase 2 migrates the application to AWS (Lambda + API Gateway + S3 + CloudFront + CodePipeline), managed via AWS CDK. CDK deploys infrastructure through CloudFormation and requires AWS credentials with permissions to create and manage multiple service resources.

A dedicated IAM user is needed to run CDK commands (`cdk bootstrap`, `cdk deploy`, `cdk destroy`) without using the root account or an overly privileged administrator account.

### Constraints

1. AWS IAM inline policies have a 2,048-character limit per policy
2. AWS IAM managed policies have a 6,144 non-whitespace character limit per policy
3. `iam:PassRole` with wildcard (`*`) resources is flagged as overly permissive by AWS IAM policy validation
4. CDK generates resource names dynamically, so IAM policies must use prefix-based ARN patterns rather than exact ARNs
5. The principle of least privilege should be followed while remaining practical for a single-developer personal project

## Decision

### 1. Dedicated IAM User

Created a dedicated IAM user **`social-media-app-dev`** (`arn:aws:iam::123456789012:user/social-media-app-dev`) solely for CDK deployment of this project. This user has no console access — only programmatic access via access keys.

### 2. Four Scoped Managed Policies

Because the full policy exceeds the 2,048-character inline limit, the permissions are split into four customer-managed policies, each under the 6,144-character managed policy limit:

| Policy Name | Scope |
|---|---|
| `AiSocialMedia-Infra-Core` | CloudFormation, S3 (project + CDK asset buckets), CDK bootstrap (STS, SSM, ECR) |
| `AiSocialMedia-Compute` | Lambda functions, API Gateway HTTP APIs, CloudWatch Logs |
| `AiSocialMedia-IAM` | IAM role/policy CRUD (scoped to `AiSocialMedia*` and `cdk-*` prefixes), `iam:PassRole` with `iam:PassedToService` condition |
| `AiSocialMedia-CICD-CDN` | CloudFront distributions, CodePipeline, CodeBuild, CodeStar Connections |

### 3. Resource Naming Convention

All CDK-created resources must use the **`AiSocialMedia`** prefix (for IAM roles, Lambda functions, CloudFormation stacks, CodeBuild projects, CodePipeline pipelines) so they match the ARN patterns in the IAM policies. S3 buckets use the **`ai-social-media-`** prefix (lowercase with hyphens, per S3 naming rules).

### 4. PassRole Scoping

The `iam:PassRole` permission is restricted with two controls:

- **Resource ARNs**: Only roles matching `AiSocialMedia*` or `cdk-*` can be passed
- **Condition key**: `iam:PassedToService` limits which services the role can be passed to: `lambda.amazonaws.com`, `codepipeline.amazonaws.com`, `codebuild.amazonaws.com`, `cloudformation.amazonaws.com`

### 5. Policy Details

#### AiSocialMedia-Infra-Core

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "CloudFormation",
      "Effect": "Allow",
      "Action": "cloudformation:*",
      "Resource": [
        "arn:aws:cloudformation:us-east-1:123456789012:stack/CDKToolkit/*",
        "arn:aws:cloudformation:us-east-1:123456789012:stack/AiSocialMedia*/*"
      ]
    },
    {
      "Sid": "S3ProjectBuckets",
      "Effect": "Allow",
      "Action": "s3:*",
      "Resource": [
        "arn:aws:s3:::ai-social-media-*",
        "arn:aws:s3:::ai-social-media-*/*",
        "arn:aws:s3:::cdk-*-assets-123456789012-us-east-1",
        "arn:aws:s3:::cdk-*-assets-123456789012-us-east-1/*"
      ]
    },
    {
      "Sid": "CDKBootstrap",
      "Effect": "Allow",
      "Action": [
        "sts:GetCallerIdentity", "sts:AssumeRole",
        "ssm:GetParameter", "ssm:PutParameter", "ssm:DeleteParameter",
        "ecr:CreateRepository", "ecr:DescribeRepositories",
        "ecr:SetRepositoryPolicy", "ecr:PutLifecyclePolicy",
        "ecr:GetAuthorizationToken"
      ],
      "Resource": "*"
    }
  ]
}
```

#### AiSocialMedia-Compute

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "Lambda",
      "Effect": "Allow",
      "Action": "lambda:*",
      "Resource": "arn:aws:lambda:us-east-1:123456789012:function:AiSocialMedia*"
    },
    {
      "Sid": "APIGateway",
      "Effect": "Allow",
      "Action": "apigateway:*",
      "Resource": "arn:aws:apigateway:us-east-1::/apis*"
    },
    {
      "Sid": "Logs",
      "Effect": "Allow",
      "Action": "logs:*",
      "Resource": "arn:aws:logs:us-east-1:123456789012:log-group:/aws/*/AiSocialMedia*"
    }
  ]
}
```

#### AiSocialMedia-IAM

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "IAMRolesAndPolicies",
      "Effect": "Allow",
      "Action": [
        "iam:CreateRole", "iam:DeleteRole", "iam:GetRole",
        "iam:AttachRolePolicy", "iam:DetachRolePolicy",
        "iam:PutRolePolicy", "iam:DeleteRolePolicy", "iam:GetRolePolicy",
        "iam:ListRolePolicies", "iam:ListAttachedRolePolicies",
        "iam:TagRole", "iam:UntagRole",
        "iam:CreatePolicy", "iam:DeletePolicy", "iam:GetPolicy",
        "iam:GetPolicyVersion", "iam:ListPolicyVersions",
        "iam:CreatePolicyVersion", "iam:DeletePolicyVersion"
      ],
      "Resource": [
        "arn:aws:iam::123456789012:role/AiSocialMedia*",
        "arn:aws:iam::123456789012:role/cdk-*",
        "arn:aws:iam::123456789012:policy/AiSocialMedia*"
      ]
    },
    {
      "Sid": "IAMPassRoleScoped",
      "Effect": "Allow",
      "Action": "iam:PassRole",
      "Resource": [
        "arn:aws:iam::123456789012:role/AiSocialMedia*",
        "arn:aws:iam::123456789012:role/cdk-*"
      ],
      "Condition": {
        "StringEquals": {
          "iam:PassedToService": [
            "lambda.amazonaws.com",
            "codepipeline.amazonaws.com",
            "codebuild.amazonaws.com",
            "cloudformation.amazonaws.com"
          ]
        }
      }
    }
  ]
}
```

#### AiSocialMedia-CICD-CDN

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "CloudFront",
      "Effect": "Allow",
      "Action": "cloudfront:*",
      "Resource": "*"
    },
    {
      "Sid": "CICD",
      "Effect": "Allow",
      "Action": [
        "codepipeline:*", "codebuild:*",
        "codestar-connections:*"
      ],
      "Resource": [
        "arn:aws:codepipeline:us-east-1:123456789012:AiSocialMedia*",
        "arn:aws:codebuild:us-east-1:123456789012:project/AiSocialMedia*",
        "arn:aws:codestar-connections:us-east-1:123456789012:connection/*"
      ]
    }
  ]
}
```

## Rationale

- **Dedicated user over shared credentials**: Isolates deployment permissions from day-to-day AWS usage; access keys can be rotated or revoked independently
- **Managed policies over inline**: Inline policies are capped at 2,048 characters, too small for the required permissions; managed policies allow up to 6,144 characters each and are reusable
- **Four policies by service domain**: Logical grouping (infra, compute, IAM, CI/CD) makes it easy to audit, update, or temporarily detach specific permission sets
- **Prefix-based ARN scoping**: CDK generates resource names with stack prefixes; using `AiSocialMedia*` patterns ensures the policies work with CDK's naming while preventing access to unrelated resources in the account
- **PassRole condition key**: AWS IAM best practice — prevents the user from passing roles to arbitrary services, limiting the blast radius of a credential compromise

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| `AdministratorAccess` managed policy | Violates least privilege; grants full access to all AWS services and resources |
| Single inline policy | Exceeds the 2,048-character inline policy limit |
| Single managed policy | The combined policy exceeds 6,144 characters with properly scoped PassRole |
| IAM Role with AssumeRole (instead of IAM User) | Adds complexity for a single-developer workflow; user + access keys is simpler for local CLI usage |
| AWS SSO / Identity Center | Overkill for a single-user personal project; adds organizational overhead |

## Consequences

**Positive:**
- Deployment credentials follow least privilege — only the specific AWS services needed for this project are accessible
- `iam:PassRole` is properly scoped with both resource ARNs and service conditions, passing AWS IAM policy validation without warnings
- Resource naming convention (`AiSocialMedia*` prefix) is established early, ensuring consistency across all CDK stacks
- Policies are modular — individual permission sets can be updated without touching others

**Trade-offs:**
- If CDK introduces new resource types or naming patterns, the IAM policies may need updating (permission denied errors during `cdk deploy`)
- CloudFront actions use `Resource: "*"` because CloudFront does not support resource-level ARN restrictions for most actions
- The `CDKBootstrap` statement uses `Resource: "*"` for STS, SSM, and ECR actions required by CDK's bootstrap process

## AWS CLI Configuration

The user's credentials are configured locally as a named profile:

```bash
aws configure --profile social-media-dev
# Access Key ID: <from IAM console>
# Secret Access Key: <from IAM console>
# Default region: us-east-1
# Output format: json
```

Verify with:

```bash
aws sts get-caller-identity --profile social-media-dev
```

## Related Documents

- [PHASE2-REMOTE-HOSTING.md](../PHASE2-REMOTE-HOSTING.md) — Phase 2 architecture and hosting evaluation
- [Phase 2 Deployment Plan](../../.cursor/plans/phase_2_deployment_plan_cc93b95b.plan.md) — Implementation plan with CDK stacks and pipeline
