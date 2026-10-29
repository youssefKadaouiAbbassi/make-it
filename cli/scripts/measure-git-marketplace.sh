#!/usr/bin/env bash
# Measures, in a throwaway ubuntu container with real Claude Code, how a git marketplace is pinned
# to a ref and whether `claude plugin marketplace update` moves it: the two behaviours the git
# catalog design keys on (results in cli/research/git-marketplace-measured-2026-10-29.md). A local
# bare repository, served over HTTPS in the container, holds a marketplace "cat" with annotated
# tags and a main branch. No
# credentials, tokens or host directories enter the container; --rm removes it.
#   bash cli/scripts/measure-git-marketplace.sh
set -euo pipefail
cli=$(cd "$(dirname "$0")/.." && pwd)
# Claude Code takes only owner/repo, https://... or a path, so the repository is served over HTTPS
# (smart protocol, which shallow clones need) by git http-backend behind a throwaway TLS server.
# It is built as its own module in the Go container, outside this one.
"$cli/scripts/go.sh" sh -c 'mkdir -p /tmp/gs && cd /tmp/gs && cat > main.go && go mod init gs >/dev/null 2>&1 &&
  go build -o /src/cli/dist/measure-gitserver .' <<'GO'
package main

import (
	"log"
	"net/http"
	"net/http/cgi"
	"os"
)

// gitserver root cert key: serves the repositories under root on https://127.0.0.1:8443/.
func main() {
	h := &cgi.Handler{Path: "/usr/lib/git-core/git-http-backend",
		Env: []string{"GIT_PROJECT_ROOT=" + os.Args[1], "GIT_HTTP_EXPORT_ALL=1"}}
	log.Fatal(http.ListenAndServeTLS("127.0.0.1:8443", os.Args[2], os.Args[3], h))
}
GO
docker run --rm -i -v "$cli/dist/measure-gitserver:/usr/local/bin/gitserver:ro" ubuntu:24.04 bash -s <<'INNER'
set -euo pipefail
apt-get update -qq && apt-get install -y -qq curl git ca-certificates jq openssl >/dev/null
curl -fsSL https://claude.ai/install.sh | bash >/dev/null 2>&1
export PATH="$HOME/.local/bin:$PATH"
echo "claude $(claude --version)"
openssl req -x509 -newkey rsa:2048 -nodes -keyout /k.pem -out /c.pem -days 1 -subj /CN=127.0.0.1 \
  -addext "subjectAltName=IP:127.0.0.1" 2>/dev/null
cp /c.pem /usr/local/share/ca-certificates/measure.crt
update-ca-certificates >/dev/null 2>&1
export NODE_EXTRA_CA_CERTS=/c.pem
mkdir -p /repos
gitserver /repos /c.pem /k.pem &
URL=https://127.0.0.1:8443/src.git
# Each measured command may fail; its exit code is part of the result.
run() {
  echo
  echo "\$ $*"
  local rc=0
  "$@" || rc=$?
  echo "[exit $rc]"
}
known() { jq -c . "$HOME/.claude/plugins/known_marketplaces.json" 2>&1 || true; }
head_of() {
  local loc
  loc=$(jq -r '.cat.installLocation // empty' "$HOME/.claude/plugins/known_marketplaces.json" 2>/dev/null || true)
  if [ -n "$loc" ]; then echo "clone $loc HEAD $(git -C "$loc" rev-parse HEAD 2>&1)"; else echo "no clone recorded"; fi
}
bump() { # version message
  echo "{\"name\": \"p\", \"version\": \"$1\"}" > /w/plugins/p/.claude-plugin/plugin.json
  git -C /w commit -qam "$2"
}
git config --global user.email m@m
git config --global user.name m
git config --global init.defaultBranch main
mkdir -p /w/.claude-plugin /w/plugins/p/.claude-plugin /neutral
cd /neutral
echo '{"name": "cat", "owner": {"name": "measure"}, "plugins": [{"name": "p", "source": "./plugins/p"}]}' > /w/.claude-plugin/marketplace.json
echo '{"name": "p", "version": "1.0.0"}' > /w/plugins/p/.claude-plugin/plugin.json
git -C /w init -q
git -C /w add -A
git -C /w commit -qm one
git -C /w tag -a v1.0.0 -m v1.0.0
git clone -q --bare /w /repos/src.git
A=$(git -C /w rev-parse HEAD)
echo "A = $A (v1.0.0, main)"
i=0
until curl -sf "$URL/info/refs?service=git-upload-pack" >/dev/null; do i=$((i + 1)); [ "$i" -lt 30 ] || exit 1; sleep 1; done
echo "server up: $(git ls-remote "$URL" HEAD)"

echo; echo "=== (k) the add command's syntax"
run claude plugin marketplace add --help

echo; echo "=== (l) add pinned to a tag: https URL with #v1.0.0"
run claude plugin marketplace add "$URL#v1.0.0" --scope user --json
run claude plugin marketplace list --json
known
cat "$HOME/.claude/settings.json" 2>/dev/null || echo "no settings.json"
head_of
run claude plugin install p@cat --scope user --json
claude plugin list --json | jq -c '[.[] | {id, version}]'

echo; echo "=== (m) a newer tag appears: does update move a tag-pinned marketplace?"
bump 1.1.0 two
git -C /w tag -a v1.1.0 -m v1.1.0
git -C /w push -q /repos/src.git main v1.1.0
B=$(git -C /w rev-parse HEAD)
echo "B = $B (v1.1.0, main)"
run claude plugin marketplace update cat --json
head_of
echo "(A $A, B $B)"

echo; echo "=== (n) the pinned tag itself is moved (force push): does update follow it?"
git -C /w tag -f -a v1.0.0 -m moved >/dev/null
git -C /w push -q -f /repos/src.git v1.0.0
run claude plugin marketplace update cat --json
head_of
echo "(B $B)"

echo; echo "=== (o) a branch ref: does update follow the branch head?"
run claude plugin marketplace remove cat --scope user --json
run claude plugin marketplace add "$URL#main" --scope user --json
known
head_of
bump 1.2.0 three
git -C /w push -q /repos/src.git main
C=$(git -C /w rev-parse HEAD)
echo "C = $C (main)"
run claude plugin marketplace update cat --json
head_of
echo "(C $C)"

echo; echo "=== (p) no ref: which commit, and does update follow the default branch?"
run claude plugin marketplace remove cat --scope user --json
run claude plugin marketplace add "$URL" --scope user --json
known
head_of
bump 1.3.0 four
git -C /w push -q /repos/src.git main
D=$(git -C /w rev-parse HEAD)
echo "D = $D (main)"
run claude plugin marketplace update cat --json
head_of
echo "(D $D)"

echo; echo "=== (q) a commit ref: #<40-hex>"
run claude plugin marketplace remove cat --scope user --json
run claude plugin marketplace add "$URL#$A" --scope user --json
known
head_of
echo "(A $A)"

echo; echo "=== (r) git ls-remote shows the peeled commit of an annotated tag"
git ls-remote --tags "$URL"
INNER
