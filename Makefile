.PHONY: all build-frontend build-frontend-local build-web build-select build-triage clean deploy-frontend
.PHONY: build-lambda-api build-lambda-thumbnail build-lambda-selection build-lambda-enhance build-lambda-video build-lambdas
.PHONY: build-lambda-triage build-lambda-description build-lambda-download build-lambda-publish
.PHONY: ecr-login push-api push-triage push-description push-download push-publish push-thumbnail push-selection push-enhance push-video push-webhook push-oauth push-all

# Build all binaries
all: build-select build-triage build-web

# Build the Preact frontend for cloud deployment (uses .env.production → cloud mode)
build-frontend:
	cd web && npm install && npm run build
	rm -rf cmd/web-server/frontend_dist
	cp -r web/dist cmd/web-server/frontend_dist

# Build the Preact frontend for local mode (overrides VITE_CLOUD_MODE from .env.production)
build-frontend-local:
	cd web && npm install && VITE_CLOUD_MODE= npm run build
	rm -rf cmd/web-server/frontend_dist
	cp -r web/dist cmd/web-server/frontend_dist

# Build the web server with local-mode frontend
build-web: build-frontend-local
	go build -o bin/web-server ./cmd/web-server

# Build CLI tools
build-select:
	go build -o bin/media-select ./cmd/media-select

build-triage:
	go build -o bin/media-triage ./cmd/media-triage

# Build Lambda binaries (for local testing — Docker builds use Dockerfiles)
build-lambda-api:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o bin/bootstrap-api ./cmd/api

build-lambda-thumbnail:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o bin/bootstrap-thumbnail ./cmd/thumbnail-worker

build-lambda-selection:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o bin/bootstrap-selection ./cmd/selection-worker

build-lambda-enhance:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o bin/bootstrap-enhance ./cmd/enhance-worker

build-lambda-video:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o bin/bootstrap-video ./cmd/video-worker

build-lambda-triage:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o bin/bootstrap-triage ./cmd/triage-worker

build-lambda-description:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o bin/bootstrap-description ./cmd/description-worker

build-lambda-download:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o bin/bootstrap-download ./cmd/download-worker

build-lambda-publish:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o bin/bootstrap-publish ./cmd/publish-worker

build-lambdas: build-lambda-api build-lambda-thumbnail build-lambda-selection build-lambda-enhance build-lambda-video build-lambda-triage build-lambda-description build-lambda-download build-lambda-publish

# Deploy frontend to S3 + CloudFront (manual deploy bypassing FrontendPipeline)
# Usage: make deploy-frontend
# Optional: FRONTEND_BUCKET=... CLOUDFRONT_DIST_ID=... make deploy-frontend
FRONTEND_BUCKET ?= $(shell aws cloudformation describe-stacks --stack-name AiSocialMediaStorage --region us-east-1 --query 'Stacks[0].Outputs[?OutputKey==`FrontendBucketName`].OutputValue' --output text 2>/dev/null || echo "ai-social-media-frontend-681565534940")
CLOUDFRONT_DIST_ID ?= $(shell aws cloudformation describe-stacks --stack-name AiSocialMediaFrontend --region us-east-1 --query 'Stacks[0].Outputs[?OutputKey==`DistributionId`].OutputValue' --output text 2>/dev/null || echo "EWSMFGGJI6TSW")
deploy-frontend: build-frontend
	aws s3 sync web/dist s3://$(FRONTEND_BUCKET) --delete
	aws cloudfront create-invalidation --distribution-id $(CLOUDFRONT_DIST_ID) --paths "/*"

# Development: run Go server with API only (frontend uses Vite dev server)
dev-api:
	go run ./cmd/web-server

# Development: run Vite dev server (in separate terminal)
dev-frontend:
	cd web && npm run dev

# =========================================================================
# Local Lambda Quick-Push (DDR-047: bypass CodePipeline for dev iteration)
# Usage: make push-api  (auto-detects ACCOUNT and REGION)
#        make push-all  (rebuild and deploy all 8 Lambdas)
#
# Function names are CDK-generated (stable unless construct tree changes).
# To find current names: aws lambda list-functions --region us-east-1 \
#   --query 'Functions[?starts_with(FunctionName,`AiSocialMedia`)].FunctionName'
# =========================================================================
ACCOUNT ?= $(shell aws sts get-caller-identity --query Account --output text)
REGION  ?= us-east-1
PRIVATE_LIGHT   = $(ACCOUNT).dkr.ecr.$(REGION).amazonaws.com/ai-social-media-lambda-light
PRIVATE_HEAVY   = $(ACCOUNT).dkr.ecr.$(REGION).amazonaws.com/ai-social-media-lambda-heavy
PRIVATE_WEBHOOK = $(ACCOUNT).dkr.ecr.$(REGION).amazonaws.com/ai-social-media-webhook
PRIVATE_OAUTH   = $(ACCOUNT).dkr.ecr.$(REGION).amazonaws.com/ai-social-media-oauth

# Lambda function names (from CDK stacks: AiSocialMediaBackend, AiSocialMediaWebhook)
FN_API       ?= $(shell aws cloudformation describe-stacks --stack-name AiSocialMediaBackend --region $(REGION) --query 'Stacks[0].Outputs[?OutputKey==`ApiLambdaName`].OutputValue' --output text 2>/dev/null)
FN_TRIAGE    ?= $(shell aws cloudformation describe-stacks --stack-name AiSocialMediaBackend --region $(REGION) --query 'Stacks[0].Outputs[?OutputKey==`TriageLambdaName`].OutputValue' --output text 2>/dev/null)
FN_DESC      ?= $(shell aws cloudformation describe-stacks --stack-name AiSocialMediaBackend --region $(REGION) --query 'Stacks[0].Outputs[?OutputKey==`DescriptionLambdaName`].OutputValue' --output text 2>/dev/null)
FN_DOWNLOAD  ?= $(shell aws cloudformation describe-stacks --stack-name AiSocialMediaBackend --region $(REGION) --query 'Stacks[0].Outputs[?OutputKey==`DownloadLambdaName`].OutputValue' --output text 2>/dev/null)
FN_PUBLISH   ?= $(shell aws cloudformation describe-stacks --stack-name AiSocialMediaBackend --region $(REGION) --query 'Stacks[0].Outputs[?OutputKey==`PublishLambdaName`].OutputValue' --output text 2>/dev/null)
FN_ENHANCE   ?= $(shell aws lambda list-functions --region $(REGION) --query 'Functions[?contains(FunctionName,`EnhancementProcessor`)].FunctionName|[0]' --output text 2>/dev/null)
FN_THUMBNAIL ?= $(shell aws lambda list-functions --region $(REGION) --query 'Functions[?contains(FunctionName,`ThumbnailProcessor`)].FunctionName|[0]' --output text 2>/dev/null)
FN_SELECTION ?= $(shell aws lambda list-functions --region $(REGION) --query 'Functions[?contains(FunctionName,`SelectionProcessor`)].FunctionName|[0]' --output text 2>/dev/null)
FN_VIDEO     ?= $(shell aws lambda list-functions --region $(REGION) --query 'Functions[?contains(FunctionName,`VideoProcessor`)].FunctionName|[0]' --output text 2>/dev/null)
FN_WEBHOOK   ?= $(shell aws cloudformation describe-stacks --stack-name AiSocialMediaWebhook --region $(REGION) --query 'Stacks[0].Outputs[?OutputKey==`WebhookLambdaName`].OutputValue' --output text 2>/dev/null)
FN_OAUTH     ?= $(shell aws cloudformation describe-stacks --stack-name AiSocialMediaWebhook --region $(REGION) --query 'Stacks[0].Outputs[?OutputKey==`OAuthLambdaName`].OutputValue' --output text 2>/dev/null)

# --provenance=false: required for Lambda-compatible Docker image manifest format
DOCKER_BUILD = DOCKER_BUILDKIT=1 docker build --provenance=false

ecr-login:
	aws ecr get-login-password --region $(REGION) | \
	  docker login --username AWS --password-stdin $(ACCOUNT).dkr.ecr.$(REGION).amazonaws.com

push-api: ecr-login
	$(DOCKER_BUILD) --build-arg CMD_TARGET=api \
	  -f build/Dockerfile.light -t $(PRIVATE_LIGHT):api-dev .
	docker push $(PRIVATE_LIGHT):api-dev
	aws lambda update-function-code --function-name $(FN_API) \
	  --image-uri $(PRIVATE_LIGHT):api-dev --region $(REGION)
	aws lambda wait function-updated --function-name $(FN_API) --region $(REGION)

push-triage: ecr-login
	$(DOCKER_BUILD) --build-arg CMD_TARGET=triage-worker \
	  -f build/Dockerfile.light -t $(PRIVATE_LIGHT):triage-dev .
	docker push $(PRIVATE_LIGHT):triage-dev
	aws lambda update-function-code --function-name $(FN_TRIAGE) \
	  --image-uri $(PRIVATE_LIGHT):triage-dev --region $(REGION)
	aws lambda wait function-updated --function-name $(FN_TRIAGE) --region $(REGION)

push-description: ecr-login
	$(DOCKER_BUILD) --build-arg CMD_TARGET=description-worker \
	  -f build/Dockerfile.light -t $(PRIVATE_LIGHT):desc-dev .
	docker push $(PRIVATE_LIGHT):desc-dev
	aws lambda update-function-code --function-name $(FN_DESC) \
	  --image-uri $(PRIVATE_LIGHT):desc-dev --region $(REGION)
	aws lambda wait function-updated --function-name $(FN_DESC) --region $(REGION)

push-download: ecr-login
	$(DOCKER_BUILD) --build-arg CMD_TARGET=download-worker \
	  -f build/Dockerfile.light -t $(PRIVATE_LIGHT):download-dev .
	docker push $(PRIVATE_LIGHT):download-dev
	aws lambda update-function-code --function-name $(FN_DOWNLOAD) \
	  --image-uri $(PRIVATE_LIGHT):download-dev --region $(REGION)
	aws lambda wait function-updated --function-name $(FN_DOWNLOAD) --region $(REGION)

push-publish: ecr-login
	$(DOCKER_BUILD) --build-arg CMD_TARGET=publish-worker \
	  -f build/Dockerfile.light -t $(PRIVATE_LIGHT):publish-dev .
	docker push $(PRIVATE_LIGHT):publish-dev
	aws lambda update-function-code --function-name $(FN_PUBLISH) \
	  --image-uri $(PRIVATE_LIGHT):publish-dev --region $(REGION)
	aws lambda wait function-updated --function-name $(FN_PUBLISH) --region $(REGION)

push-thumbnail: ecr-login
	$(DOCKER_BUILD) --build-arg CMD_TARGET=thumbnail-worker --build-arg ECR_ACCOUNT_ID=$(ACCOUNT) \
	  -f build/Dockerfile.heavy -t $(PRIVATE_HEAVY):thumb-dev .
	docker push $(PRIVATE_HEAVY):thumb-dev
	aws lambda update-function-code --function-name $(FN_THUMBNAIL) \
	  --image-uri $(PRIVATE_HEAVY):thumb-dev --region $(REGION)
	aws lambda wait function-updated --function-name $(FN_THUMBNAIL) --region $(REGION)

push-selection: ecr-login
	$(DOCKER_BUILD) --build-arg CMD_TARGET=selection-worker --build-arg ECR_ACCOUNT_ID=$(ACCOUNT) \
	  -f build/Dockerfile.heavy -t $(PRIVATE_HEAVY):select-dev .
	docker push $(PRIVATE_HEAVY):select-dev
	aws lambda update-function-code --function-name $(FN_SELECTION) \
	  --image-uri $(PRIVATE_HEAVY):select-dev --region $(REGION)
	aws lambda wait function-updated --function-name $(FN_SELECTION) --region $(REGION)

push-enhance: ecr-login
	$(DOCKER_BUILD) --build-arg CMD_TARGET=enhance-worker \
	  -f build/Dockerfile.light -t $(PRIVATE_LIGHT):enhance-dev .
	docker push $(PRIVATE_LIGHT):enhance-dev
	aws lambda update-function-code --function-name $(FN_ENHANCE) \
	  --image-uri $(PRIVATE_LIGHT):enhance-dev --region $(REGION)
	aws lambda wait function-updated --function-name $(FN_ENHANCE) --region $(REGION)

push-video: ecr-login
	$(DOCKER_BUILD) --build-arg CMD_TARGET=video-worker --build-arg ECR_ACCOUNT_ID=$(ACCOUNT) \
	  -f build/Dockerfile.heavy -t $(PRIVATE_HEAVY):video-dev .
	docker push $(PRIVATE_HEAVY):video-dev
	aws lambda update-function-code --function-name $(FN_VIDEO) \
	  --image-uri $(PRIVATE_HEAVY):video-dev --region $(REGION)
	aws lambda wait function-updated --function-name $(FN_VIDEO) --region $(REGION)

push-webhook: ecr-login
	$(DOCKER_BUILD) --build-arg CMD_TARGET=webhook \
	  -f build/Dockerfile.light -t $(PRIVATE_WEBHOOK):webhook-dev .
	docker push $(PRIVATE_WEBHOOK):webhook-dev
	aws lambda update-function-code --function-name $(FN_WEBHOOK) \
	  --image-uri $(PRIVATE_WEBHOOK):webhook-dev --region $(REGION)
	aws lambda wait function-updated --function-name $(FN_WEBHOOK) --region $(REGION)

push-oauth: ecr-login
	$(DOCKER_BUILD) --build-arg CMD_TARGET=oauth \
	  -f build/Dockerfile.light -t $(PRIVATE_OAUTH):oauth-dev .
	docker push $(PRIVATE_OAUTH):oauth-dev
	aws lambda update-function-code --function-name $(FN_OAUTH) \
	  --image-uri $(PRIVATE_OAUTH):oauth-dev --region $(REGION)
	aws lambda wait function-updated --function-name $(FN_OAUTH) --region $(REGION)

push-all: push-api push-triage push-description push-download push-publish push-enhance push-webhook push-oauth push-thumbnail push-selection push-video

clean:
	rm -rf bin/
	rm -rf web/dist
	rm -rf web/node_modules
