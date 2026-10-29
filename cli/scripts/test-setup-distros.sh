#!/usr/bin/env bash
# Runs `makeit setup --yes` for real in fresh ubuntu, debian, fedora and alpine containers, as a
# user with passwordless sudo: prerequisites from the distribution, Claude Code from Anthropic's
# signed repository, then the stack. A second run must change nothing. Then update, uninstall
# (no makeit plugin, settings value or global file left) and setup again. One more ubuntu pass,
# with the prerequisites preinstalled and no sudo, must fall back to the native installer. Each pass
# ends with the self update chain against fake signed releases served in the container.
# Only the binary is mounted; no credentials or tokens enter a container (--rm cleans up).
# Checks that something is absent use `if ...; then exit 1; fi`: under set -e a failing `! cmd` line
# does not stop the script.
#   bash cli/scripts/test-setup-distros.sh [image...]
set -euo pipefail
cli=$(cd "$(dirname "$0")/.." && pwd)
"$cli/scripts/go.sh" go build -o dist/makeit ./cmd/makeit
# Fake releases for the self update chain (cli/SELF-UPDATE.md): a throwaway key, makeit 1.0.0, 1.1.0
# and 1.2.0 built with it, and the 1.1.0 and 1.2.0 releases signed by it. Only these test builds
# carry the key; dist/makeit has none.
"$cli/scripts/go.sh" go build -o dist/rel/fakerelease ./tools/fakerelease
seed=$(od -An -tx1 -N32 /dev/urandom | tr -d ' \n')
pub=$("$cli/dist/rel/fakerelease" pub -seed "$seed")
for v in 1.0.0 1.1.0 1.2.0; do
  "$cli/scripts/go.sh" go build -o "dist/rel/makeit-$v" -ldflags "-X makeit/internal/buildinfo.Version=$v \
-X makeit/internal/buildinfo.Repo=test/makeit -X makeit/internal/buildinfo.KeyCurrent=$pub" ./cmd/makeit
done
for v in 1.1.0 1.2.0; do
  "$cli/dist/rel/fakerelease" release -seed "$seed" -repo test/makeit -version "$v" -target linux-x64 \
    -binary "$cli/dist/rel/makeit-$v" -dir "$cli/dist/rel/site"
done
images=("$@")
[ ${#images[@]} -gt 0 ] || images=(ubuntu:24.04 debian:trixie-slim fedora:latest alpine:3.23)

# As the user, after setup: update, uninstall, check what is left, setup again.
upkeep=$(cat <<'UPKEEP'
cat > /tmp/upkeep.sh <<"EOS"
set -e
PATH="$HOME/.local/bin:$PATH"
makeit update --yes --json > /tmp/update.json || { cat /tmp/update.json; exit 1; }
cat /tmp/update.json
if grep -q "\"status\": \"failed\"" /tmp/update.json; then echo "update failed a step"; exit 1; fi
if grep -q "\"status\": \"done\"" /tmp/update.json; then echo "update right after setup changed something"; exit 1; fi
makeit uninstall --yes --json > /tmp/uninstall.json || { cat /tmp/uninstall.json; exit 1; }
cat /tmp/uninstall.json
if claude plugin list --json | grep -q "@makeit\""; then echo "a makeit plugin is still installed"; exit 1; fi
if grep -q -e advisorModel -e cleanupPeriodDays "$HOME/.claude/settings.json"; then echo "makeit settings values are still there"; exit 1; fi
test ! -e "$HOME/.claude/CLAUDE.md"
if claude plugin marketplace list --json | grep -q "\"name\": \"makeit\""; then echo "the makeit marketplace is still there"; exit 1; fi
test ! -e "$HOME/.makeit/catalog"
makeit setup --yes
EOS
chmod 755 /tmp/upkeep.sh
UPKEEP
)

# As the user, last: self install of 1.0.0, self update to 1.1.0, a rollback that skips 1.1.0, an
# update that keeps the skip, then an update that swaps in 1.2.0 and runs it. The fake release
# server and its certificate reach only the makeit commands.
selfchain=$(cat <<'SELFCHAIN'
cat > /tmp/selfchain.sh <<"EOS"
set -e
PATH="$HOME/.local/bin:$PATH"
R=/opt/rel
bin=$HOME/.local/bin/makeit
receipt=$HOME/.makeit/receipt.json
echo 1.1.0 > /tmp/selfchain-latest
"$R/fakerelease" serve -dir "$R/site" -repo test/makeit -addr 127.0.0.1:8443 -ca /tmp/selfchain-ca.pem -latest /tmp/selfchain-latest &
srv=$!
trap 'kill "$srv" 2>/dev/null' EXIT
i=0
while [ ! -s /tmp/selfchain-ca.pem ]; do i=$((i + 1)); [ "$i" -lt 30 ] || exit 1; sleep 1; done
for b in /etc/ssl/certs/ca-certificates.crt /etc/pki/tls/certs/ca-bundle.crt; do
  if [ -f "$b" ]; then cat "$b" > /tmp/selfchain-bundle.pem; break; fi
done
cat /tmp/selfchain-ca.pem >> /tmp/selfchain-bundle.pem
m() { SSL_CERT_FILE=/tmp/selfchain-bundle.pem MAKEIT_RELEASES_URL=https://127.0.0.1:8443 "$@"; }
same() { [ "$(sha256sum < "$1")" = "$(sha256sum < "$2")" ]; }
m "$R/makeit-1.0.0" self install --yes
same "$bin" "$R/makeit-1.0.0"
m "$bin" self update --yes
same "$bin" "$R/makeit-1.1.0"
grep -q "\"version\": \"1.1.0\"" "$receipt"
grep -q "\"previous\": \"1.0.0\"" "$receipt"
echo "self update: ok"
m "$bin" self rollback --yes
same "$bin" "$R/makeit-1.0.0"
grep -q "\"version\": \"1.0.0\"" "$receipt"
grep -q "\"skipped\": \"1.1.0\"" "$receipt"
echo "self rollback: ok"
m "$bin" update --yes > /tmp/selfchain-update1.log 2>&1 || { cat /tmp/selfchain-update1.log; exit 1; }
cat /tmp/selfchain-update1.log
grep -q "makeit 1.1.0 is skipped (you rolled back from it)" /tmp/selfchain-update1.log
same "$bin" "$R/makeit-1.0.0"
echo "update keeps the skip: ok"
echo 1.2.0 > /tmp/selfchain-latest
m "$bin" update --yes > /tmp/selfchain-update2.log 2>&1 || { cat /tmp/selfchain-update2.log; exit 1; }
cat /tmp/selfchain-update2.log
grep -q "makeit updated from 1.0.0 to 1.2.0" /tmp/selfchain-update2.log
same "$bin" "$R/makeit-1.2.0"
[ "$("$bin" version)" = "makeit 1.2.0" ]
grep -q "\"previous\": \"1.0.0\"" "$receipt"
if grep -q "\"skipped\"" "$receipt"; then echo "the skip stayed after 1.2.0"; exit 1; fi
echo "update re-exec: ok"
EOS
chmod 755 /tmp/selfchain.sh
SELFCHAIN
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
if grep -q "\"status\": \"done\"" /tmp/second.json; then echo "the second setup changed something"; exit 1; fi
'"$upkeep"'
sudo -iu u sh /tmp/upkeep.sh
'"$selfchain"'
sudo -iu u sh /tmp/selfchain.sh'

# As root: the prerequisites only, no sudo; setup must install Claude Code natively.
no_sudo='set -e
apt-get update -qq && apt-get install -y -qq git nodejs jq ca-certificates curl >/dev/null
useradd -m u
su - u -c "makeit setup --yes"
su - u -c "~/.local/bin/claude --version"
'"$upkeep"'
su - u -c "sh /tmp/upkeep.sh"
'"$selfchain"'
su - u -c "sh /tmp/selfchain.sh"'

run() { # name image script
  local log="$cli/dist/setup-$1.log"
  if docker run --rm -v "$cli/dist/makeit:/usr/local/bin/makeit:ro" -v "$cli/dist/rel:/opt/rel:ro" "$2" sh -c "$3" >"$log" 2>&1; then
    echo "PASS $1"
    grep -E '^(self update|self rollback|update keeps the skip|update re-exec): ok$' "$log" | sed 's/^/  /' || true
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
