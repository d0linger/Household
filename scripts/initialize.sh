#!/bin/sh
# Creates the test-database secrets in .local/secrets (household init --files),
# with Go or, without Go, inside the locally built image. The app itself uses
# .env from `household init > .env`; see docs/installation.md.
# Usage: sh scripts/initialize.sh [--development]
set -eu
umask 077
cd "$(dirname "$0")/.."
if command -v go >/dev/null 2>&1; then exec go run ./cmd/household init --files "$@"; fi
docker build --quiet --target ocr -t household:local . >/dev/null
exec docker run --rm --read-only --user "$(id -u):$(id -g)" -v "$PWD:/work" -w /work household:local init --files "$@"
