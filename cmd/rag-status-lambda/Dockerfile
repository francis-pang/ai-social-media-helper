# Multi-stage Dockerfile for RAG status Lambda (no ffmpeg).
# Stage 1: Build the Go binary.
# Stage 2: Bundle the Go binary with the AWS Lambda base image.

FROM public.ecr.aws/docker/library/golang:1.26 AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
    go build -ldflags="-s -w" -o /handler ./cmd/rag-status-lambda

FROM public.ecr.aws/lambda/provided:al2023
COPY --from=builder /handler /var/runtime/bootstrap
ENTRYPOINT ["/var/runtime/bootstrap"]
