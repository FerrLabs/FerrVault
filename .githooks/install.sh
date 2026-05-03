#!/usr/bin/env bash
set -euo pipefail
git config core.hooksPath .githooks
chmod +x .githooks/pre-commit .githooks/pre-push 2>/dev/null || true
echo "Git hooks installed (core.hooksPath=.githooks)."
echo "pre-commit: gofmt + go vet"
echo "pre-push:   go test ./..."
