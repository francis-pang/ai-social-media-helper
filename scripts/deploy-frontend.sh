#!/usr/bin/env bash
# Manual frontend deploy — bypasses the CodePipeline when it has bugs.
# Builds the Preact SPA locally, syncs to S3, invalidates CloudFront.
#
# Prerequisites: AWS CLI configured, npm in PATH
# Usage: ./scripts/deploy-frontend.sh [--profile PROFILE]

set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
FRONTEND_DIR="$REPO_ROOT/web/frontend"
AWS_PROFILE="${AWS_PROFILE:-}"
AWS_REGION="${AWS_REGION:-us-east-1}"
FRONTEND_STACK="AiSocialMediaFrontend"
BACKEND_STACK="AiSocialMediaBackend"

# Parse --profile if provided
while [[ $# -gt 0 ]]; do
  case $1 in
    --profile)
      AWS_PROFILE="$2"
      shift 2
      ;;
    *)
      break
      ;;
  esac
done

# Pass --profile only when AWS_PROFILE is set (avoids set -u issues with empty array)
aws_profile_opt() { [[ -n "$AWS_PROFILE" ]] && echo "--profile" "$AWS_PROFILE"; }
export AWS_REGION

echo "=== Manual Frontend Deploy ==="
echo "Repo root: $REPO_ROOT"
echo "AWS region: $AWS_REGION"
[[ -n "$AWS_PROFILE" ]] && echo "AWS profile: $AWS_PROFILE"
echo ""

# --- 1. Fetch CloudFormation outputs ---
echo ">>> Fetching CloudFormation outputs..."
DIST_ID=$(aws cloudformation describe-stacks $(aws_profile_opt) --stack-name "$FRONTEND_STACK" \
  --query "Stacks[0].Outputs[?OutputKey=='DistributionId'].OutputValue" --output text 2>/dev/null || true)
BUCKET=$(aws cloudformation describe-stacks $(aws_profile_opt) --stack-name "$FRONTEND_STACK" \
  --query "Stacks[0].Outputs[?OutputKey=='FrontendBucketName'].OutputValue" --output text 2>/dev/null || true)

# Fallback: StorageStack also exports FrontendBucketName
if [[ -z "$BUCKET" ]] || [[ "$BUCKET" == "None" ]]; then
  BUCKET=$(aws cloudformation describe-stacks $(aws_profile_opt) --stack-name AiSocialMediaStorage \
    --query "Stacks[0].Outputs[?OutputKey=='FrontendBucketName'].OutputValue" --output text 2>/dev/null || true)
fi

USER_POOL_ID=$(aws cloudformation describe-stacks $(aws_profile_opt) --stack-name "$BACKEND_STACK" \
  --query "Stacks[0].Outputs[?OutputKey=='UserPoolId'].OutputValue" --output text 2>/dev/null || true)
CLIENT_ID=$(aws cloudformation describe-stacks $(aws_profile_opt) --stack-name "$BACKEND_STACK" \
  --query "Stacks[0].Outputs[?OutputKey=='UserPoolClientId'].OutputValue" --output text 2>/dev/null || true)

if [[ -z "$DIST_ID" ]] || [[ "$DIST_ID" == "None" ]]; then
  echo "ERROR: Could not get CloudFront DistributionId from stack $FRONTEND_STACK"
  exit 1
fi
if [[ -z "$BUCKET" ]] || [[ "$BUCKET" == "None" ]]; then
  echo "ERROR: Could not get Frontend bucket name"
  exit 1
fi
if [[ -z "$USER_POOL_ID" ]] || [[ "$USER_POOL_ID" == "None" ]]; then
  echo "ERROR: Could not get Cognito UserPoolId from stack $BACKEND_STACK"
  exit 1
fi
if [[ -z "$CLIENT_ID" ]] || [[ "$CLIENT_ID" == "None" ]]; then
  echo "ERROR: Could not get Cognito UserPoolClientId from stack $BACKEND_STACK"
  exit 1
fi

echo "  Distribution ID: $DIST_ID"
echo "  Bucket: $BUCKET"
echo "  User Pool ID: $USER_POOL_ID"
echo ""

# --- 2. Build frontend ---
echo ">>> Building frontend (VITE_CLOUD_MODE=1, Cognito env vars set)..."
cd "$FRONTEND_DIR"
export VITE_CLOUD_MODE=1
export VITE_COGNITO_USER_POOL_ID="$USER_POOL_ID"
export VITE_COGNITO_CLIENT_ID="$CLIENT_ID"
npm ci
npm run build

if [[ ! -d dist ]]; then
  echo "ERROR: Build did not produce dist/ directory"
  exit 1
fi
echo "  Build completed. dist/ has $(find dist -type f | wc -l | tr -d ' ') files."
echo ""

# --- 3. Deploy to S3 ---
echo ">>> Syncing dist/ to s3://$BUCKET/ ..."
aws s3 sync dist/ "s3://$BUCKET/" --delete $(aws_profile_opt)
echo "  S3 sync complete."
echo ""

# --- 4. Invalidate CloudFront ---
echo ">>> Invalidating CloudFront cache..."
INV_ID=$(aws cloudfront create-invalidation $(aws_profile_opt) \
  --distribution-id "$DIST_ID" \
  --paths "/*" \
  --query 'Invalidation.Id' --output text)
echo "  Invalidation ID: $INV_ID"
echo ""

# --- 5. Output URL ---
DOMAIN=$(aws cloudfront get-distribution $(aws_profile_opt) \
  --id "$DIST_ID" \
  --query 'Distribution.DomainName' --output text)
echo "=== Deploy complete ==="
echo "URL: https://$DOMAIN"
echo ""
echo "CloudFront invalidation may take 1–2 minutes to propagate."
echo "Verify: curl -sI https://$DOMAIN | head -5"
