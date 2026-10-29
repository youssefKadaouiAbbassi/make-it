#!/usr/bin/env bash
# Runs `makeit setup --yes` for real in fresh ubuntu, debian, fedora and alpine containers, as a
# user with passwordless sudo: prerequisites from the distribution, Claude Code from Anthropic's
# signed repository, then the stack. A second run must change nothing. Then update, uninstall
# (no makeit plugin, settings value or global file left) and setup again. One more ubuntu pass,
# with the prerequisites preinstalled and no sudo, must fall back to the native installer.
# Only the binary is mounted; no credentials or tokens enter a container (--rm cleans up).
#   bash cli/scripts/test-setup-distros.sh [image...]
set -euo pipefail
cli=$(cd "$(dirname "$0")/.." && pwd)
"$cli/scripts/go.sh" go build -o dist/makeit ./cmd/makeit
images=("$@")
[ ${#images[@]} -gt 0 ] || images=(ubuntu:24.04 debian:trixie-slim fedora:latest alpine:3.23)

# As the user, after setup: update, uninstall, check what is left, setup again.
upkeep=$(cat <<'UPKEEP'
cat > /tmp/upkeep.sh <<"EOS"
set -e
PATH="$HOME/.local/bin:$PATH"
makeit update --yes --json > /tmp/update.json || { cat /tmp/update.json; exit 1; }
cat /tmp/update.json
! grep -q "\"status\": \"failed\"" /tmp/update.json
! grep -q "\"status\": \"done\"" /tmp/update.json
makeit uninstall --yes --json > /tmp/uninstall.json || { cat /tmp/uninstall.json; exit 1; }
cat /tmp/uninstall.json
! claude plugin list --json | grep -q "@makeit\""
! grep -q -e advisorModel -e cleanupPeriodDays "$HOME/.claude/settings.json"
test ! -e "$HOME/.claude/CLAUDE.md"
! claude plugin marketplace list --json | grep -q "\"name\": \"makeit\""
test ! -e "$HOME/.makeit/catalog"
makeit setup --yes
EOS
chmod 755 /tmp/upkeep.sh
UPKEEP
)

# As root: sudo and a user who may use it without a password; then setup twice as that user
# (sudo -iu, because fedora's image has no su).
sudo_user='set -e
if command -v apt-get >/dev/null; then apt-get update -qq && apt-get install -y -qq sudo >/dev/null; useradd -m u
elif command -v dnf >/dev/null; then dnf install -y -q sudo >/dev/null; useradd -m u
else apk add -q sudo; adduser -D u; fi
echo "u ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/u
sudo -iu u makeit setup --yes
sudo -iu u claude --version
sudo -iu u makeit setup --yes --json > /tmp/second.json
cat /tmp/second.json
! grep -q "\"status\": \"done\"" /tmp/second.json
'"$upkeep"'
sudo -iu u sh /tmp/upkeep.sh'

# As root: the prerequisites only, no sudo; setup must install Claude Code natively.
no_sudo='set -e
apt-get update -qq && apt-get install -y -qq git nodejs jq ca-certificates curl >/dev/null
useradd -m u
su - u -c "makeit setup --yes"
su - u -c "~/.local/bin/claude --version"
'"$upkeep"'
su - u -c "sh /tmp/upkeep.sh"'

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
