# Multi-stage Dockerfile for Lambda container image deployment (DDR-027).
#
# Stage 1: Build the Go binary using the project-required Go version.
# Stage 2: Bundle the Go binary with static ffmpeg/ffprobe into the AWS
#           Lambda base image (provided:al2023), which is proactively cached
#           on Lambda workers for faster cold starts.
#
# The Dockerfile preserves the existing exec.Command("ffmpeg"/ffprobe") calls
# unchanged — the binaries are on PATH inside the container.

# Stage 1: Build Go binary
FROM golang:1.24 AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /handler ./cmd/media-lambda

# Stage 2: Runtime with ffmpeg + ffprobe
# - AWS base image is proactively cached on Lambda workers (faster cold starts)
# - Static ffmpeg includes libsvtav1 (AV1) and libopus (Opus) codecs (DDR-018)
# - Stable layers (ffmpeg) are copied before the Go binary (changes every deploy)
#   so Lambda's chunk-level caching keeps ffmpeg cached across deployments
FROM public.ecr.aws/lambda/provided:al2023

# Copy static ffmpeg and ffprobe binaries (with AV1+Opus support)
COPY --from=mwader/static-ffmpeg:7.1 /ffmpeg /usr/local/bin/
COPY --from=mwader/static-ffmpeg:7.1 /ffprobe /usr/local/bin/

# Copy Go binary (changes most often — last layer for optimal caching)
COPY --from=builder /handler /var/runtime/bootstrap

ENTRYPOINT ["/var/runtime/bootstrap"]
