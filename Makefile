.PHONY: all build-frontend build-web build-select build-triage clean
.PHONY: build-lambda-api build-lambda-thumbnail build-lambda-selection build-lambda-enhance build-lambda-video build-lambdas

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

clean:
	rm -f media-select media-triage media-web
	rm -f bootstrap-api bootstrap-thumbnail bootstrap-selection bootstrap-enhance bootstrap-video
	rm -rf web/frontend/dist
	rm -rf web/frontend/node_modules
