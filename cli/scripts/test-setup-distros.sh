#!/usr/bin/env bash
# Runs `makeit setup --yes` for real in fresh ubuntu, debian, fedora and alpine containers, as a
# user with passwordless sudo: prerequisites from the distribution, Claude Code from Anthropic's
# signed repository, then the stack. A second run must change nothing. Then update, uninstall
# (no makeit plugin, settings value or global file left) and setup again. One more ubuntu pass,
# with the prerequisites preinstalled and no sudo, must fall back to the native installer. Each pass
# ends with the self update chain against fake signed releases served in the container, the git
# catalog chain, and makeit init in a fresh project.
# Only the binary is mounted; no credentials or tokens enter a container (--rm cleans up).
# Checks that something is absent use `if ...; then exit 1; fi`: under set -e a failing `! cmd` line
# does not stop the script.
#   bash cli/scripts/test-setup-distros.sh [image...]   (all passes run at once)
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
# What makeit writes into an empty settings.json: the merge of settings.Desired, for uninstall's check.
"$cli/scripts/go.sh" sh -c 'printf "{}\n" > /tmp/empty.json && go run ./tools/applysettings -theirs /tmp/empty.json \
-baseline internal/catalog/data/files/settings.baseline.json -out dist/rel/settings-desired.json -state /tmp/state.json' >/dev/null
chmod 644 "$cli/dist/rel/settings-desired.json"
for v in 1.1.0 1.2.0; do
  "$cli/dist/rel/fakerelease" release -seed "$seed" -repo test/makeit -version "$v" -target linux-x64 \
    -binary "$cli/dist/rel/makeit-$v" -dir "$cli/dist/rel/site"
done
# The catalog the git catalog chain serves, copied now: go generate replaces the generated tree, and
# a container whose bind mount points at the replaced directory sees it empty.
catsrc=$(mktemp -d "$cli/dist/catsrc.XXXXXX")
trap '/bin/rm -rf "$catsrc"' EXIT
cp -r "$cli/internal/catalog/data/files/." "$catsrc/"
chmod 755 "$catsrc"
images=("$@")
[ ${#images[@]} -gt 0 ] || images=(ubuntu:24.04 debian:trixie-slim fedora:latest alpine:3.23)

# As the user, after setup: update, uninstall, check what is left, setup again.
upkeep=$(cat <<'UPKEEP'
cat > /tmp/upkeep.sh <<"EOS"
set -e
PATH="$HOME/.local/bin:$PATH"
# The first update may move plugins to releases newer than the tested pins; a second one changes nothing.
makeit update --yes --json > /tmp/update1.json || { cat /tmp/update1.json; exit 1; }
cat /tmp/update1.json
if grep -q "\"status\": \"failed\"" /tmp/update1.json; then echo "update failed a step"; exit 1; fi
makeit update --yes --json > /tmp/update.json || { cat /tmp/update.json; exit 1; }
cat /tmp/update.json
if grep -q "\"status\": \"failed\"" /tmp/update.json; then echo "update failed a step"; exit 1; fi
if grep -q "\"status\": \"done\"" /tmp/update.json; then echo "a second update changed something"; exit 1; fi
# The npm tools makeit installed are checked against the real registry (marked, where npm is).
if command -v npm >/dev/null; then
  grep -q "\"id\": \"upgrade.marked\"" /tmp/update.json || { echo "update has no marked upgrade step"; exit 1; }
  if grep -q "could not read the npm registry" /tmp/update.json; then echo "update could not read the npm registry"; exit 1; fi
fi
makeit uninstall --yes --json > /tmp/uninstall.json || { cat /tmp/uninstall.json; exit 1; }
cat /tmp/uninstall.json
if claude plugin list --json | grep -q "@makeit\""; then echo "a makeit plugin is still installed"; exit 1; fi
# Every value makeit writes (settings-desired.json, from settings.Desired) must be gone; the
# claude-plugins-official declaration stays by design (cli/UPDATE-UNINSTALL.md).
s="$HOME/.claude/settings.json"; if [ -f "$s" ]; then left=$(jq -c -n --slurpfile w /opt/rel/settings-desired.json --slurpfile l "$s" '($w[0] | del(.extraKnownMarketplaces)) as $want | [$want | paths(type != "object") | select(all(.[]; type == "string"))] | map(. as $p | ($want | getpath($p)) as $v | ($l[0] | try getpath($p) catch null) as $x | select($x != null) | select(($v | type) != "array" or (($x | type) == "array" and any($x[]; . as $e | $v | index([$e]) != null))) | join("/"))') || { echo "could not compare $s"; exit 1; }; if [ "$left" != "[]" ]; then echo "values makeit wrote are still in settings.json: $left"; exit 1; fi; fi
test ! -e "$HOME/.claude/CLAUDE.md"
if claude plugin marketplace list --json | grep -q "\"name\": \"makeit\""; then echo "the makeit marketplace is still there"; exit 1; fi
test ! -e "$HOME/.makeit/catalog"
test ! -e "$HOME/.makeit/mcp/chrome-devtools"
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
m "$bin" update --yes > /tmp/selfchain-update0.log 2>&1 || { cat /tmp/selfchain-update0.log; exit 1; }
same "$bin" "$R/makeit-1.1.0"
grep -q "\"version\": \"1.1.0\"" "$receipt"
grep -q "\"previous\": \"1.0.0\"" "$receipt"
echo "update to 1.1.0: ok"
m "$bin" rollback --yes > /tmp/selfchain-rollback.log 2>&1 || { cat /tmp/selfchain-rollback.log; exit 1; }
same "$bin" "$R/makeit-1.0.0"
grep -q "\"version\": \"1.0.0\"" "$receipt"
grep -q "\"skipped\": \"1.1.0\"" "$receipt"
echo "rollback to 1.0.0: ok"
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

# As the user, after the self chain: the stack switches to a git catalog served over HTTPS (the
# embedded files, tagged v1.0.0), follows a newer tag, stays on a pin with the newer-tag line, refuses
# a moved tag, and goes back to the embedded catalog with plugin data kept (cli/CATALOG-GIT.md).
catchain=$(cat <<'CATCHAIN'
cat > /tmp/catchain.sh <<"EOS"
set -e
PATH="$HOME/.local/bin:$PATH"
M=/usr/local/bin/makeit
st=$HOME/.makeit/state.json
work=/tmp/catchain-work
cp -r /opt/catsrc "$work"
g() { git -C "$work" -c user.name=t -c user.email=t@t -c init.defaultBranch=main "$@"; }
g init -q
g add -A
g commit -q -m catalog
g tag -a v1.0.0 -m v1.0.0
mkdir -p /tmp/catchain-srv
git clone -q --bare "$work" /tmp/catchain-srv/cat.git
/opt/rel/fakerelease serve-git -root /tmp/catchain-srv -addr 127.0.0.1:8444 -ca /tmp/catchain-ca.pem &
srv=$!
trap 'kill "$srv" 2>/dev/null' EXIT
i=0
while [ ! -s /tmp/catchain-ca.pem ]; do i=$((i + 1)); [ "$i" -lt 30 ] || exit 1; sleep 1; done
for b in /etc/ssl/certs/ca-certificates.crt /etc/pki/tls/certs/ca-bundle.crt; do
  if [ -f "$b" ]; then cat "$b" > /tmp/catchain-bundle.pem; break; fi
done
cat /tmp/catchain-ca.pem >> /tmp/catchain-bundle.pem
URL=https://127.0.0.1:8444/cat.git
m() { GIT_SSL_CAINFO=/tmp/catchain-bundle.pem "$M" "$@"; }
data=$HOME/.claude/plugins/data/superpowers-makeit
mkdir -p "$data"
echo keep > "$data/catchain-marker"
m update --catalog "$URL" --yes > /tmp/catchain-1.log 2>&1 || { cat /tmp/catchain-1.log; exit 1; }
grep -q "\"kind\": \"git\"" "$st"
grep -q "\"tag\": \"v1.0.0\"" "$st"
echo "catalog from git: ok"
echo chain > "$work/templates/catalog-chain.md"
g add -A
g commit -q -m v1.1.0
g tag -a v1.1.0 -m v1.1.0
g push -q /tmp/catchain-srv/cat.git main v1.1.0
m update --yes > /tmp/catchain-2.log 2>&1 || { cat /tmp/catchain-2.log; exit 1; }
grep -q "\"tag\": \"v1.1.0\"" "$st"
test -f "$HOME/.makeit/catalog/templates/catalog-chain.md"
echo "catalog follows the newest tag: ok"
m update --catalog "$URL#v1.0.0" --yes > /tmp/catchain-3.log 2>&1 || { cat /tmp/catchain-3.log; exit 1; }
m update --yes > /tmp/catchain-4.log 2>&1 || { cat /tmp/catchain-4.log; exit 1; }
grep -q "plugin set v1.1.0 is newer than v1.0.0" /tmp/catchain-4.log
echo "a pinned catalog stays, with the newer-tag line: ok"
before=$(grep -o "\"resolved\": \"[0-9a-f]*\"" "$st")
g tag -f -a v1.0.0 -m moved >/dev/null
g push -q -f /tmp/catchain-srv/cat.git v1.0.0
m update --yes > /tmp/catchain-5.log 2>&1 || { cat /tmp/catchain-5.log; exit 1; }
grep -q "plugin set tag v1.0.0 now points to" /tmp/catchain-5.log
[ "$(grep -o "\"resolved\": \"[0-9a-f]*\"" "$st")" = "$before" ]
echo "a moved tag is refused: ok"
m update --catalog embedded --yes > /tmp/catchain-6.log 2>&1 || { cat /tmp/catchain-6.log; exit 1; }
grep -q "\"kind\": \"embedded\"" "$st"
if [ "$(cat "$data/catchain-marker")" != keep ]; then echo "a plugin lost its data"; exit 1; fi
echo "back to embedded, plugin data kept: ok"
EOS
chmod 755 /tmp/catchain.sh
CATCHAIN
)

# As the user: makeit init in a fresh node + Go project. A fake npx stands in for docs-mcp-server,
# so nothing leaves the container; it logs what makeit asks it.
initchain=$(cat <<'INITCHAIN'
cat > /tmp/initchain.sh <<"EOS"
set -e
M=/usr/local/bin/makeit
i() { PATH="$HOME/.local/bin:$PATH" "$M" "$@"; }
p=/tmp/initchain-proj
mkdir -p "$p"
cd "$p"
git init -q
printf '{\n  "name": "shop"\n}\n' > package.json
printf 'module x\n\ngo 1.27.3\n\nrequire golang.org/x/term v0.47.0\n' > go.mod
i init --yes > /tmp/initchain-1.log 2>&1 || { cat /tmp/initchain-1.log; exit 1; }
for f in CLAUDE.md CONSTRAINTS.md HANDOFF.md .gitignore .env.example .makeit.json .pre-commit-config.yaml renovate.json .grype.yaml; do
  if [ ! -f "$p/$f" ]; then echo "init did not write $f"; cat /tmp/initchain-1.log; exit 1; fi
done
if ! grep -q '"check:full"' package.json; then echo "no check stubs"; cat package.json; exit 1; fi
echo "init writes the project files: ok"
i init --yes --json > /tmp/initchain-2.json 2>/tmp/initchain-2.log || { cat /tmp/initchain-2.log /tmp/initchain-2.json; exit 1; }
if grep -q '"status": "done"' /tmp/initchain-2.json; then echo "the second init changed something"; cat /tmp/initchain-2.json; exit 1; fi
echo "a second init changes nothing: ok"
echo "mine" >> CLAUDE.md
i init --update --yes > /tmp/initchain-3.log 2>&1 || { cat /tmp/initchain-3.log; exit 1; }
if [ "$(tail -n 1 CLAUDE.md)" != mine ]; then echo "init --update overwrote an edited file"; exit 1; fi
if ! grep -q "kept: CLAUDE.md: changed by you" /tmp/initchain-3.log; then echo "no kept line"; cat /tmp/initchain-3.log; exit 1; fi
echo "init --update keeps an edited file: ok"
EOS
chmod 755 /tmp/initchain.sh
INITCHAIN
)

# As the user, right after the first setup: every tool makeit's table knows is installed, by its
# route, or (chrome on a distribution Google ships no package for) reported needs-you; the shell
# profile loads makeit's PATH folders.
toolcheck=$(cat <<'TOOLCHECK'
cat > /tmp/toolcheck.sh <<"EOS"
set -e
installed() { env PATH="$HOME/.local/bin:$PATH" sh -c "command -v \"\$1\"" -- "$1" >/dev/null; }
# gh and Chrome come through the package manager, which needs sudo; without it they are left to
# the user (the no-sudo pass), like Chrome on a distribution Google ships no package for (Alpine).
tools="prek deja grype"
if command -v sudo >/dev/null; then tools="gh $tools"; fi
# The npm route needs npm: setup installs it with node where it may; the no-sudo pass has it preinstalled.
if command -v npm >/dev/null; then tools="$tools typescript-language-server pyright-langserver marked"; fi
for t in $tools; do
  installed "$t" || { echo "$t not installed"; exit 1; }
done
echo "tools installed: ok"
if installed google-chrome || installed google-chrome-stable; then
  echo "chrome installed: ok"
elif command -v apk >/dev/null || ! command -v sudo >/dev/null; then
  echo "chrome needs you: ok"
else
  echo "chrome missing where a route exists"; exit 1
fi
grep -q ">>> makeit >>>" "$HOME/.profile"
echo "profile block: ok"
# A brand new terminal: no ~/.local/bin on PATH until .profile adds it.
env -i HOME="$HOME" PATH=/usr/bin:/bin sh -c '. "$HOME/.profile"; command -v prek' >/dev/null
echo "PATH through the profile: ok"
# The plugin's reply check asks the marked setup installed: a reply whose choices join the plan's
# list reads as joined, one with a plain line between as options.
if command -v npm >/dev/null; then
  lists=$(find "$HOME/.claude/plugins" -name lists.mjs | head -n 1)
  [ -n "$lists" ] || { echo "lists.mjs not installed"; exit 1; }
  [ "$(printf '1. a\n2. b\n\n1. **Go** · recommended\n2. **Stop**\n' | node "$lists")" = joined ] || { echo "lists.mjs did not read joined"; exit 1; }
  [ "$(printf '1. a\n2. b\n\nPick:\n\n1. **Go** · recommended\n2. **Stop**\n' | node "$lists")" = options ] || { echo "lists.mjs did not read options"; exit 1; }
  echo "reply check through marked: ok"
  # chrome-devtools-mcp is installed locally, and the shipped .mcp.json starts that copy (a PATH
  # with node only, so npx could not have served it) and gets an initialize reply.
  test -f "$HOME/.makeit/mcp/chrome-devtools/node_modules/chrome-devtools-mcp/package.json" || { echo "no local chrome-devtools-mcp"; exit 1; }
  # chrome-devtools-mcp 1.12.0 needs Node 20.19 or newer (Ubuntu 24.04's apt node is 18, where npx fails too).
  if ! node -e 'const [a,b]=process.versions.node.split(".").map(Number);process.exit(a>20||(a===20&&b>=19)?0:1)'; then
    echo "chrome-devtools-mcp needs Node 20.19+, this is $(node -v): server start not checked"
  else
  nd=$(mktemp -d)
  ln -s "$(command -v node)" "$nd/node"
  PATH="$nd" node -e 'const j=require(process.argv[1]).mcpServers["chrome-devtools"];const c=require("child_process").spawn(j.command,j.args,{stdio:["pipe","pipe","inherit"],env:process.env});c.stdout.once("data",d=>{console.log(String(d));c.kill();process.exit(/"serverInfo"/.test(String(d))?0:1)});c.stdin.write(JSON.stringify({jsonrpc:"2.0",id:1,method:"initialize",params:{protocolVersion:"2025-06-18",capabilities:{},clientInfo:{name:"t",version:"0"}}})+"\n");setTimeout(()=>process.exit(1),30000)' "$HOME/.makeit/catalog/plugins/makeit/.mcp.json" || { echo "the shipped .mcp.json did not start the local copy"; exit 1; }
  echo "chrome-devtools-mcp from the local copy: ok"
  fi
fi
EOS
chmod 755 /tmp/toolcheck.sh
TOOLCHECK
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
'"$toolcheck"'
sudo -iu u sh /tmp/toolcheck.sh
sudo -iu u makeit setup --yes --json > /tmp/second.json
cat /tmp/second.json
if grep -q "\"status\": \"done\"" /tmp/second.json; then echo "the second setup changed something"; exit 1; fi
'"$upkeep"'
sudo -iu u sh /tmp/upkeep.sh
'"$selfchain"'
sudo -iu u sh /tmp/selfchain.sh
'"$catchain"'
# Alpine ships git http-backend, which the fake catalog server needs, in git-daemon.
if command -v apk >/dev/null; then apk add -q git-daemon; fi
sudo -iu u sh /tmp/catchain.sh
'"$initchain"'
sudo -iu u sh /tmp/initchain.sh'

# As root: the prerequisites only, no sudo; setup must install Claude Code natively.
no_sudo='set -e
apt-get update -qq && apt-get install -y -qq git nodejs npm jq ca-certificates curl >/dev/null
useradd -m u
su - u -c "makeit setup --yes"
su - u -c "~/.local/bin/claude --version"
'"$toolcheck"'
su - u -c "sh /tmp/toolcheck.sh"
'"$upkeep"'
su - u -c "sh /tmp/upkeep.sh"
'"$selfchain"'
su - u -c "sh /tmp/selfchain.sh"
'"$catchain"'
su - u -c "sh /tmp/catchain.sh"
'"$initchain"'
su - u -c "sh /tmp/initchain.sh"'

run() { # name image script; prints its result in one write, so parallel passes do not interleave
  local log="$cli/dist/setup-$1.log"
  if docker run --rm -v "$cli/dist/makeit:/usr/local/bin/makeit:ro" -v "$cli/dist/rel:/opt/rel:ro" -v "$catsrc:/opt/catsrc:ro" "$2" sh -c "$3" >"$log" 2>&1; then
    local ok
    ok=$(grep -E ': ok$' "$log" | sed 's/^/  /') || true
    printf '%s\n' "PASS $1" ${ok:+"$ok"}
  else
    printf 'FAIL %s (log: %s)\n' "$1" "$log"
    return 1
  fi
}

# The passes share only read-only mounts and write their own logs, so they run at once; the time is
# about the slowest pass instead of the sum.
# An image named twice runs once: two passes of it would write the same log.
pids=()
seen=" "
for img in "${images[@]}"; do
  case "$seen" in *" $img "*) continue ;; esac
  seen="$seen$img "
  run "$(echo "$img" | tr ':/' '__')" "$img" "$sudo_user" &
  pids+=($!)
done
run ubuntu-no-sudo ubuntu:24.04 "$no_sudo" &
pids+=($!)
status=0
for p in "${pids[@]}"; do wait "$p" || status=1; done
exit $status
