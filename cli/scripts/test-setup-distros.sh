#!/usr/bin/env bash
# Runs `makeit setup --yes` for real in fresh ubuntu, debian, fedora and alpine containers, as a
# user with passwordless sudo: prerequisites from the distribution, Claude Code from Anthropic's
# signed repository, then the stack. A second run must change nothing. One more ubuntu pass,
# with the prerequisites preinstalled and no sudo, must fall back to the native installer.
# Only the binary is mounted; no credentials or tokens enter a container (--rm cleans up).
#   bash cli/scripts/test-setup-distros.sh [image...]
set -euo pipefail
cli=$(cd "$(dirname "$0")/.." && pwd)
"$cli/scripts/go.sh" go build -o dist/makeit ./cmd/makeit
images=("$@")
[ ${#images[@]} -gt 0 ] || images=(ubuntu:24.04 debian:trixie-slim fedora:latest alpine:3.23)

# As root: sudo and a user who may use it without a password; then setup twice as that user.
sudo_user='set -e
if command -v apt-get >/dev/null; then apt-get update -qq && apt-get install -y -qq sudo >/dev/null; useradd -m u
elif command -v dnf >/dev/null; then dnf install -y -q sudo >/dev/null; useradd -m u
else apk add -q sudo; adduser -D u; fi
echo "u ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/u
su - u -c "makeit setup --yes"
su - u -c "claude --version"
su - u -c "makeit setup --yes --json" > /tmp/second.json
cat /tmp/second.json
! grep -q "\"status\": \"done\"" /tmp/second.json'

# As root: the prerequisites only, no sudo; setup must install Claude Code natively.
no_sudo='set -e
apt-get update -qq && apt-get install -y -qq git nodejs jq ca-certificates curl >/dev/null
useradd -m u
su - u -c "makeit setup --yes"
su - u -c "~/.local/bin/claude --version"'

run() { # name image script
  local log="$cli/dist/setup-$1.log"
  if docker run --rm -v "$cli/dist/makeit:/usr/local/bin/makeit:ro" "$2" sh -c "$3" >"$log" 2>&1; then
    echo "PASS $1"
  else
    echo "FAIL $1 (log: $log)"
    status=1
  fi
}

status=0
for img in "${images[@]}"; do
  run "$(echo "$img" | tr ':/' '__')" "$img" "$sudo_user"
done
run ubuntu-no-sudo ubuntu:24.04 "$no_sudo"
exit $status
