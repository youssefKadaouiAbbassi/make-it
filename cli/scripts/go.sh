#!/usr/bin/env bash
# Run a command in the golang image with this repository mounted, as the calling user.
# Nothing is installed on the host: the toolchain, module cache and build cache live in
# the image and the claude-stack-gocache volume.
#   scripts/go.sh go test ./...
set -euo pipefail
repo=$(cd "$(dirname "$0")/../.." && pwd)
image=${GO_IMAGE:-golang:latest}
flags=(--rm -i)
if [[ -t 0 && -t 1 ]]; then flags+=(-t); fi
exec docker run "${flags[@]}" \
  --user "$(id -u):$(id -g)" \
  -e HOME=/tmp \
  -e GOMODCACHE=/cache/mod -e GOCACHE=/cache/build -e GOBIN=/cache/bin -e GOPATH=/cache \
  -e GOTOOLCHAIN=local -e GOFLAGS="${GOFLAGS:-}" -e CGO_ENABLED=0 \
  -v claude-stack-gocache:/cache \
  -v "$repo:/src" -w /src/cli \
  "$image" "$@"
