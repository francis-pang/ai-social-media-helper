# DDR-025: SSM Parameter Store for Runtime Secrets

**Date**: 2026-02-06  
**Status**: Accepted  
**Iteration**: 14

## Context

Phase 2 deploys a Lambda function that needs the Gemini API key at runtime. The original plan ([Phase 2 Deployment Plan](../../.cursor/plans/phase_2_deployment_plan_cc93b95b.plan.md)) specified passing the key as a CDK environment variable (`GEMINI_API_KEY=... cdk deploy`), which sets it as a plaintext Lambda environment variable. While Lambda encrypts environment variables at rest, this approach has drawbacks:

1. The plaintext value appears in the CloudFormation template and stack parameters
2. Anyone with `cloudformation:GetTemplate` or `lambda:GetFunctionConfiguration` can read the key
3. The value is visible in the AWS Console Lambda configuration page
4. No audit trail for secret access

A more secure approach is needed for storing the API key.

## Decision

Store the Gemini API key as an **SSM Parameter Store `SecureString`** parameter at the path `/ai-social-media/prod/gemini-api-key`. The Lambda function reads it at startup via the SSM `GetParameter` API with decryption.

### Setup (one-time)

```bash
aws ssm put-parameter \
  --name "/ai-social-media/prod/gemini-api-key" \
  --type SecureString \
  --value "<gemini-api-key>" \
  --profile social-media-dev
```

### Lambda IAM Permissions

The Lambda execution role requires:

- `ssm:GetParameter` on `arn:aws:ssm:us-east-1:123456789012:parameter/ai-social-media/*`
- `kms:Decrypt` on the default AWS-managed SSM KMS key (`alias/aws/ssm`)

### Lambda Code Pattern

```go
func getGeminiAPIKey(ctx context.Context) (string, error) {
    cfg, _ := config.LoadDefaultConfig(ctx)
    client := ssm.NewFromConfig(cfg)
    out, err := client.GetParameter(ctx, &ssm.GetParameterInput{
        Name:           aws.String("/ai-social-media/prod/gemini-api-key"),
        WithDecryption: aws.Bool(true),
    })
    if err != nil {
        return "", fmt.Errorf("failed to get Gemini API key from SSM: %w", err)
    }
    return *out.Parameter.Value, nil
}
```

The key is fetched once during Lambda cold start and cached for the lifetime of the execution environment.

## Rationale

- **SSM Parameter Store is free** for standard parameters (up to 10,000 parameters, 40 TPS) — no additional cost
- **Encryption at rest** via KMS `SecureString` type, same encryption guarantee as Secrets Manager
- **No plaintext in CloudFormation** — the Lambda reads the secret at runtime, not from environment variables
- **Audit trail** — CloudTrail logs every `GetParameter` call
- **CDK has first-class support** — `ssm.StringParameter.valueForStringParameter()` for infra, SDK for runtime
- **Hierarchical naming** — `/ai-social-media/prod/gemini-api-key` naturally scopes to this project and environment

## Alternatives Considered

| Approach | Rejected Because |
|----------|------------------|
| Lambda environment variable (original plan) | Plaintext in CloudFormation template, visible in console, no access audit trail |
| AWS Secrets Manager | $0.40/secret/month + API call costs; automatic rotation is unnecessary for a static Gemini API key |
| CDK context parameter / `.env` file | Same problem as env vars — ends up as plaintext in CloudFormation |
| Hardcoded in code | Obviously insecure; secret in source control |

## Consequences

**Positive:**
- Gemini API key is encrypted at rest and never appears in plaintext in CloudFormation or the Lambda console
- Zero additional cost (SSM standard tier is free)
- CloudTrail provides an audit trail for secret access
- The hierarchical path (`/ai-social-media/...`) is extensible for future secrets
- IAM deployment user already has `ssm:PutParameter` (via `AiSocialMedia-Infra-Core` policy in [DDR-023](./DDR-023-aws-iam-deployment-user.md))

**Trade-offs:**
- Lambda cold start adds ~50-100ms for the SSM `GetParameter` call (mitigated by caching the value for the lifetime of the execution environment)
- One additional manual prerequisite step (`aws ssm put-parameter`) before the first deployment
- If the KMS key is deleted or the IAM policy is misconfigured, the Lambda will fail to start

## Related Documents

- [DDR-023: AWS IAM User and Scoped Policies](./DDR-023-aws-iam-deployment-user.md) — IAM user policies include SSM permissions
- [Phase 2 Deployment Plan](../../.cursor/plans/phase_2_deployment_plan_cc93b95b.plan.md) — Updated to reference SSM instead of env vars
