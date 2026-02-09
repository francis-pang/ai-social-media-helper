.PHONY: all build-frontend build-web build-select build-triage clean
.PHONY: build-lambda-api build-lambda-thumbnail build-lambda-selection build-lambda-enhance build-lambda-video build-lambdas
.PHONY: ecr-login push-api push-thumbnail push-selection push-enhance push-video push-webhook

# Build all binaries
all: build-select build-triage build-web

# Build the Preact frontend and copy to embed directory
build-frontend:
	cd web/frontend && npm install && npm run build
	rm -rf cmd/media-web/frontend_dist
	cp -r web/frontend/dist cmd/media-web/frontend_dist

# Build the web server (requires frontend to be built first)
build-web: build-frontend
	go build -o media-web ./cmd/media-web

# Build CLI tools
build-select:
	go build -o media-select ./cmd/media-select

build-triage:
	go build -o media-triage ./cmd/media-triage

# Build Lambda binaries (for local testing — Docker builds use Dockerfiles)
build-lambda-api:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o bootstrap-api ./cmd/media-lambda

build-lambda-thumbnail:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o bootstrap-thumbnail ./cmd/thumbnail-lambda

build-lambda-selection:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o bootstrap-selection ./cmd/selection-lambda

build-lambda-enhance:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o bootstrap-enhance ./cmd/enhance-lambda

build-lambda-video:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o bootstrap-video ./cmd/video-lambda

build-lambdas: build-lambda-api build-lambda-thumbnail build-lambda-selection build-lambda-enhance build-lambda-video

# Development: run Go server with API only (frontend uses Vite dev server)
dev-api:
	go run ./cmd/media-web

# Development: run Vite dev server (in separate terminal)
dev-frontend:
	cd web/frontend && npm run dev

# =========================================================================
# Local Lambda Quick-Push (DDR-047: bypass CodePipeline for dev iteration)
# Usage: make push-api  (auto-detects ACCOUNT and REGION)
# =========================================================================
ACCOUNT ?= $(shell aws sts get-caller-identity --query Account --output text)
REGION  ?= us-east-1
PRIVATE_LIGHT = $(ACCOUNT).dkr.ecr.$(REGION).amazonaws.com/ai-social-media-lambda-light
PRIVATE_HEAVY = $(ACCOUNT).dkr.ecr.$(REGION).amazonaws.com/ai-social-media-lambda-heavy
PRIVATE_WEBHOOK = $(ACCOUNT).dkr.ecr.$(REGION).amazonaws.com/ai-social-media-webhook

ecr-login:
	aws ecr get-login-password --region $(REGION) | \
	  docker login --username AWS --password-stdin $(ACCOUNT).dkr.ecr.$(REGION).amazonaws.com

push-api: ecr-login
	DOCKER_BUILDKIT=1 docker build --build-arg CMD_TARGET=media-lambda \
	  -f cmd/media-lambda/Dockerfile.light -t $(PRIVATE_LIGHT):api-dev .
	docker push $(PRIVATE_LIGHT):api-dev
	aws lambda update-function-code --function-name AiSocialMediaApiHandler \
	  --image-uri $(PRIVATE_LIGHT):api-dev --region $(REGION)
	aws lambda wait function-updated --function-name AiSocialMediaApiHandler --region $(REGION)

push-thumbnail: ecr-login
	DOCKER_BUILDKIT=1 docker build --build-arg CMD_TARGET=thumbnail-lambda \
	  -f cmd/media-lambda/Dockerfile.heavy -t $(PRIVATE_HEAVY):thumb-dev .
	docker push $(PRIVATE_HEAVY):thumb-dev
	aws lambda update-function-code --function-name AiSocialMediaThumbnailProcessor \
	  --image-uri $(PRIVATE_HEAVY):thumb-dev --region $(REGION)
	aws lambda wait function-updated --function-name AiSocialMediaThumbnailProcessor --region $(REGION)

push-selection: ecr-login
	DOCKER_BUILDKIT=1 docker build --build-arg CMD_TARGET=selection-lambda \
	  -f cmd/media-lambda/Dockerfile.heavy -t $(PRIVATE_HEAVY):select-dev .
	docker push $(PRIVATE_HEAVY):select-dev
	aws lambda update-function-code --function-name AiSocialMediaSelectionProcessor \
	  --image-uri $(PRIVATE_HEAVY):select-dev --region $(REGION)
	aws lambda wait function-updated --function-name AiSocialMediaSelectionProcessor --region $(REGION)

push-enhance: ecr-login
	DOCKER_BUILDKIT=1 docker build --build-arg CMD_TARGET=enhance-lambda \
	  -f cmd/media-lambda/Dockerfile.light -t $(PRIVATE_LIGHT):enhance-dev .
	docker push $(PRIVATE_LIGHT):enhance-dev
	aws lambda update-function-code --function-name AiSocialMediaEnhancementProcessor \
	  --image-uri $(PRIVATE_LIGHT):enhance-dev --region $(REGION)
	aws lambda wait function-updated --function-name AiSocialMediaEnhancementProcessor --region $(REGION)

push-video: ecr-login
	DOCKER_BUILDKIT=1 docker build --build-arg CMD_TARGET=video-lambda \
	  -f cmd/media-lambda/Dockerfile.heavy -t $(PRIVATE_HEAVY):video-dev .
	docker push $(PRIVATE_HEAVY):video-dev
	aws lambda update-function-code --function-name AiSocialMediaVideoProcessor \
	  --image-uri $(PRIVATE_HEAVY):video-dev --region $(REGION)
	aws lambda wait function-updated --function-name AiSocialMediaVideoProcessor --region $(REGION)

push-webhook: ecr-login
	DOCKER_BUILDKIT=1 docker build --build-arg CMD_TARGET=webhook-lambda \
	  -f cmd/media-lambda/Dockerfile.light -t $(PRIVATE_WEBHOOK):webhook-dev .
	docker push $(PRIVATE_WEBHOOK):webhook-dev
	aws lambda update-function-code --function-name AiSocialMediaWebhookHandler \
	  --image-uri $(PRIVATE_WEBHOOK):webhook-dev --region $(REGION)
	aws lambda wait function-updated --function-name AiSocialMediaWebhookHandler --region $(REGION)

clean:
	rm -f media-select media-triage media-web
	rm -f bootstrap-api bootstrap-thumbnail bootstrap-selection bootstrap-enhance bootstrap-video
	rm -rf web/frontend/dist
	rm -rf web/frontend/node_modules
