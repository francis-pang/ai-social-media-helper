package main

// Build-time version identity, injected via -ldflags (DDR-062).
//
// These variables are overridden during Docker build:
//
//	go build -ldflags="-X main.commitHash=${COMMIT_HASH} -X main.buildTime=$(date -u +%Y%m%dT%H%M%SZ)"
//
// In development (go run), the defaults "dev" and "unknown" are used.
var (
	commitHash = "dev"     // 7-char git commit hash, overridden by -ldflags at build
	buildTime  = "unknown" // UTC timestamp (YYYYMMDDTHHMMSSz), overridden by -ldflags at build
)
