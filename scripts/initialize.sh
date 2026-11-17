#!/bin/sh
# Creates .local/secrets and .env through `household init` (Go, or Docker without Go).
# Usage: sh scripts/initialize.sh [--development] [--env-template .env.all.example]
set -eu
umask 077
cd "$(dirname "$0")/.."
if command -v go >/dev/null 2>&1; then exec go run ./cmd/household init "$@"; fi
docker build --quiet --target runtime -t household:local . >/dev/null
exec docker run --rm --read-only --user "$(id -u):$(id -g)" -v "$PWD:/work" -w /work household:local init "$@"
