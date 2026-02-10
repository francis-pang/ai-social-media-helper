# CDK Rollback Recovery Runbook

Operator procedures for recovering from CloudFormation deployment failures.

**Related:** [Deployment Strategy](../DEPLOYMENT_STRATEGY.md) | [DDR-055](../design-decisions/DDR-055-deployment-automation.md)

---

## 1. UPDATE_ROLLBACK_FAILED

**Symptom:** Stack status shows `UPDATE_ROLLBACK_FAILED` in CloudFormation console or `cdk deploy` fails with "stack is in UPDATE_ROLLBACK_FAILED state".

**Cause:** CloudFormation attempted to roll back a failed update but couldn't restore one or more resources to their previous state.

### Recovery Steps

```bash
# Step 1: Attempt standard rollback recovery
aws cloudformation continue-update-rollback \
  --stack-name <STACK_NAME> \
  --region us-east-1

# Step 2: If Step 1 fails (resource cannot be restored), skip the problem resource
aws cloudformation continue-update-rollback \
  --stack-name <STACK_NAME> \
  --resources-to-skip <LOGICAL_RESOURCE_ID> \
  --region us-east-1

# Step 3: Verify stack is back to UPDATE_ROLLBACK_COMPLETE
aws cloudformation describe-stacks \
  --stack-name <STACK_NAME> \
  --query 'Stacks[0].StackStatus' \
  --output text
```

**Finding the logical resource ID:**

```bash
# List failed events to identify which resource blocked rollback
aws cloudformation describe-stack-events \
  --stack-name <STACK_NAME> \
  --query 'StackEvents[?ResourceStatus==`UPDATE_FAILED`].[LogicalResourceId,ResourceStatusReason]' \
  --output table
```

**After recovery:** Fix the root cause in CDK code before attempting the next deploy. The skipped resource may be in an inconsistent state.

---

## 2. Resource Already Exists

**Symptom:** `CREATE_FAILED` with "Resource already exists" error.

**Common causes:**
- Orphaned S3 bucket from a previous partial deploy
- Manually created resource with the same name
- Stack was deleted but resource had `DeletionPolicy: Retain`

### Recovery Steps

```bash
# Step 1: Identify the conflicting resource
aws cloudformation describe-stack-events \
  --stack-name <STACK_NAME> \
  --query 'StackEvents[?ResourceStatus==`CREATE_FAILED`].[LogicalResourceId,ResourceType,ResourceStatusReason]' \
  --output table

# Step 2a: If it's an S3 bucket — check if it's empty and can be deleted
aws s3 ls s3://<BUCKET_NAME> --summarize
# If empty:
aws s3 rb s3://<BUCKET_NAME>
# If not empty and you're sure it's orphaned:
aws s3 rm s3://<BUCKET_NAME> --recursive && aws s3 rb s3://<BUCKET_NAME>

# Step 2b: If it's another resource type — delete or rename the existing resource
# Then retry the deploy

# Step 3: Redeploy
cd ai-social-media-helper-deploy/cdk
npx cdk deploy <STACK_NAME> --method=direct
```

**Prevention (DDR-045):** All S3 buckets live in `StorageStack` (stateful). Stateless stacks never create buckets, so destroying/redeploying them cannot orphan storage.

---

## 3. Permission Denied

**Symptom:** `AccessDenied`, `UnauthorizedAccess`, or `is not authorized to perform` errors during deploy.

### Check IAM Permissions

```bash
# Verify which IAM identity is being used
aws sts get-caller-identity

# Expected: user/boyshawn in account 681565534940
```

**Required IAM permission groups** (DDR-023):

| Group | Services |
|-------|----------|
| `AiSocialMedia-Infra-Core` | CloudFormation, S3, DynamoDB, Lambda, API Gateway, SSM, Cognito |
| `AiSocialMedia-Compute` | ECR, CodeBuild, Step Functions |
| `AiSocialMedia-IAM` | IAM roles and policies (scoped to `AiSocialMedia*`) |
| `AiSocialMedia-CICD-CDN` | CodePipeline, CloudFront, CodeStar Connections |

```bash
# List groups for the current user
aws iam list-groups-for-user --user-name boyshawn \
  --query 'Groups[].GroupName' --output table
```

**For GitHub Actions:** Check that `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` secrets are set and not expired.

```bash
gh secret list --repo francis-pang/ai-social-media-helper
gh secret list --repo francis-pang/ai-social-media-helper-deploy
```

---

## 4. Pipeline Source Failure

**Symptom:** CodePipeline fails at Source stage with "Could not access the GitHub repository" or CodeStar connection error.

### Recovery Steps

```bash
# Step 1: Check CodeStar connection status
aws codeconnections list-connections \
  --query 'Connections[?ProviderType==`GitHub`].[ConnectionName,ConnectionStatus,ConnectionArn]' \
  --output table \
  --region us-east-1

# Step 2: If status is PENDING or ERROR, re-authorize:
# Go to AWS Console > Developer Tools > Settings > Connections
# Click on the connection > Update pending connection > Authorize with GitHub
```

**Check the connection ARN matches the pipeline:**

```bash
# The CDK code reads CODESTAR_CONNECTION_ARN or falls back to a default
# Verify the ARN in the pipeline source action:
aws codepipeline get-pipeline \
  --name AiSocialMediaBackendPipeline \
  --query 'pipeline.stages[0].actions[0].configuration' \
  --output json
```

---

## 5. CDK Bootstrap Missing or Outdated

**Symptom:** "This stack uses assets, so the toolkit stack must be deployed" or bootstrap version mismatch.

### Recovery Steps

```bash
# Check current bootstrap version
aws cloudformation describe-stacks \
  --stack-name CDKToolkit \
  --query 'Stacks[0].Outputs[?OutputKey==`BootstrapVersion`].OutputValue' \
  --output text \
  --region us-east-1

# Re-bootstrap if needed (safe to run — idempotent)
cd ai-social-media-helper-deploy/cdk
npx cdk bootstrap aws://681565534940/us-east-1
```

---

## 6. Lambda Update Failure (ECR Image Missing)

**Symptom:** Backend pipeline Deploy stage fails with `InvalidParameterValueException: Source image ... does not exist`.

**Cause:** The pipeline tried to update a Lambda with a Docker image tag that hasn't been pushed to ECR yet. This can happen if the Build stage partially failed or the conditional build logic skipped an image it shouldn't have.

### Recovery Steps

```bash
# Step 1: Check what tags exist in the ECR repo
aws ecr list-images \
  --repository-name ai-social-media-lambda-light \
  --query 'imageIds[*].imageTag' \
  --output table

# Step 2: Verify the specific tag the pipeline is looking for
# Check the imageDetail.json artifact in the pipeline's build output

# Step 3: If the tag is missing, manually trigger a full rebuild
# Option A: GitHub Actions
# Go to Actions > Intelligent Pipeline Trigger > Run workflow > both

# Option B: AWS Console
aws codepipeline start-pipeline-execution \
  --pipeline-name AiSocialMediaBackendPipeline

# Step 3 alt: Reset the conditional build tracker to force full rebuild
aws ssm delete-parameter --name /ai-social-media/last-build-commit
# Then trigger the pipeline — all 11 images will rebuild
```

---

## 7. CloudFront Invalidation Failure

**Symptom:** Frontend pipeline Deploy stage succeeds at S3 sync but fails at CloudFront invalidation. Users see stale content.

### Manual Invalidation

```bash
# Find the distribution ID
aws cloudfront list-distributions \
  --query 'DistributionList.Items[?Comment!=``].[Id,Comment,DomainName]' \
  --output table

# Create invalidation
aws cloudfront create-invalidation \
  --distribution-id <DISTRIBUTION_ID> \
  --paths "/*"

# Check invalidation status
aws cloudfront get-invalidation \
  --distribution-id <DISTRIBUTION_ID> \
  --id <INVALIDATION_ID> \
  --query 'Invalidation.Status'
```

---

## 8. Deploy Order Violations

**Symptom:** Stack deploy fails because a dependency stack hasn't been deployed yet (e.g., Lambda references an ECR repo that doesn't exist).

### Correct Deploy Order

```
1. StorageStack        (S3, DynamoDB — must be first)
2. RegistryStack       (ECR repos — must be before Backend/Webhook)
3. BackendStack        (depends on Storage + Registry)
4. FrontendStack       (depends on Storage)
5. WebhookStack        (depends on Registry)
6. FrontendPipeline    (depends on Frontend)
7. BackendPipeline     (depends on Backend + Webhook)
8-10. Operations stacks (depend on Backend + Storage)
```

**Full ordered deploy:**

```bash
cd ai-social-media-helper-deploy/cdk
make deploy-full
# Or manually:
npx cdk deploy --all --method=direct --concurrency 5 --require-approval never
```

CDK's `addDependency()` ensures correct ordering when using `--all`.

---

## 9. Stack Stuck in IN_PROGRESS

**Symptom:** Stack shows `CREATE_IN_PROGRESS` or `UPDATE_IN_PROGRESS` for more than 30 minutes.

**Common causes:**
- Lambda is waiting for a VPC ENI (if VPC-attached)
- CloudFront distribution creation (can take 15-25 minutes — this is normal)
- Resource is waiting for a custom resource Lambda to respond

### Recovery Steps

```bash
# Check what resource is still in progress
aws cloudformation describe-stack-events \
  --stack-name <STACK_NAME> \
  --query 'StackEvents[?ResourceStatus==`CREATE_IN_PROGRESS` || ResourceStatus==`UPDATE_IN_PROGRESS`].[LogicalResourceId,ResourceType,Timestamp]' \
  --output table

# If it's a CloudFront distribution — wait (up to 25 min is normal)
# If it's genuinely stuck (>45 min) — cancel the update:
aws cloudformation cancel-update-stack --stack-name <STACK_NAME>
```

---

## Quick Reference

| Stack | Command |
|-------|---------|
| All stacks | `make deploy-full` |
| Core (daily) | `make deploy` or `make deploy-core` |
| Single stack | `make deploy-backend` / `make deploy-frontend` / etc. |
| Preview changes | `make diff` |
| Synthesize templates | `make synth` |
| Dev mode (fast) | `make deploy-dev` |

| Emergency | Command |
|-----------|---------|
| Force pipeline run | `aws codepipeline start-pipeline-execution --pipeline-name <NAME>` |
| Force full image rebuild | `aws ssm delete-parameter --name /ai-social-media/last-build-commit` |
| CloudFront invalidation | `aws cloudfront create-invalidation --distribution-id <ID> --paths "/*"` |
| Resume failed rollback | `aws cloudformation continue-update-rollback --stack-name <NAME>` |
| Push without hooks | `git push --no-verify` |
