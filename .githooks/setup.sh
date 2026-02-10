#!/usr/bin/env bash
# Install git hooks for the app repo (ai-social-media-helper)
# Usage: .githooks/setup.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

echo "Installing git hooks for ai-social-media-helper..."

# Configure git to use our hooks directory
git -C "$REPO_DIR" config core.hooksPath .githooks

# Ensure hooks are executable
chmod +x "$SCRIPT_DIR/pre-push"

echo "Done. Git hooks installed from .githooks/"
echo "  - pre-push: Full validation (C3) — go vet, go build, frontend build, secret scan"
