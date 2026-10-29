#!/usr/bin/env bash
# Measures, in a throwaway ubuntu container with real Claude Code, the plugin behaviours that
# makeit update and uninstall key on (cli/UPDATE-UNINSTALL.md; results in
# cli/research/plugin-cli-measured-2026-10-29.md). A local directory marketplace "m" holds a bundle
# that depends on deja-vu (git-subdir, pinned) and on a local plugin. No credentials, tokens or
# host directories enter the container; --rm removes it.
#   bash cli/scripts/measure-plugin-cli.sh
set -euo pipefail
# deja-vu v0.23.0 (annotated tag, peeled) and v0.23.1: both carry version 0.23.0 in plugin.json.
old=9f1e7c4e648d6e7d2d0eacf372df59ba2c770235
new=9ce7c980b9b805c7c4a4f07ffea544a423690eda
docker run --rm -i -e OLD="$old" -e NEW="$new" ubuntu:24.04 bash -s <<'INNER'
set -euo pipefail
apt-get update -qq && apt-get install -y -qq curl git ca-certificates jq >/dev/null
curl -fsSL https://claude.ai/install.sh | bash >/dev/null 2>&1
export PATH="$HOME/.local/bin:$PATH"
echo "claude $(claude --version)"
# Each measured command may fail; its exit code is part of the result.
run() {
  echo
  echo "\$ $*"
  local rc=0
  "$@" || rc=$?
  echo "[exit $rc]"
}
sha() { jq -r --arg id "$1" '.plugins[$id][0].gitCommitSha // "none"' "$HOME/.claude/plugins/installed_plugins.json" || echo none; }
data() { for d in "$@"; do mkdir -p "$HOME/.claude/plugins/data/$d" && echo kept > "$HOME/.claude/plugins/data/$d/marker"; done; }

m=/m
mkdir -p "$m/.claude-plugin" "$m/plugins/bundle/.claude-plugin" "$m/plugins/local/.claude-plugin" "$m/plugins/local2/.claude-plugin"
cat > "$m/.claude-plugin/marketplace.json" <<JSON
{"name": "m", "owner": {"name": "measure"}, "plugins": [
 {"name": "bundle", "source": "./plugins/bundle"},
 {"name": "local", "source": "./plugins/local"},
 {"name": "local2", "source": "./plugins/local2"},
 {"name": "deja-vu", "source": {"source": "git-subdir", "url": "https://github.com/vshulcz/deja-vu.git", "path": "claude-plugin", "sha": "$OLD"}}]}
JSON
echo '{"name": "bundle", "version": "1.0.0", "dependencies": ["deja-vu", "local"]}' > "$m/plugins/bundle/.claude-plugin/plugin.json"
echo '{"name": "local", "version": "1.0.0"}' > "$m/plugins/local/.claude-plugin/plugin.json"
echo '{"name": "local2", "version": "1.0.0"}' > "$m/plugins/local2/.claude-plugin/plugin.json"

run claude plugin marketplace add "$m" --scope user --json
run claude plugin install bundle@m --scope user --json
run claude plugin list --json
echo "deja-vu commit after install: $(sha deja-vu@m) (want OLD $OLD)"
run claude plugin marketplace list --json

echo; echo "=== (e) marketplace update on a directory marketplace"
run claude plugin marketplace update m --json

echo; echo "=== (a) pin moved, same version"
jq --arg s "$NEW" '(.plugins[] | select(.name == "deja-vu") | .source.sha) = $s' "$m/.claude-plugin/marketplace.json" > /tmp/mk
mv /tmp/mk "$m/.claude-plugin/marketplace.json"
run claude plugin marketplace update m --json
run claude plugin update deja-vu@m --scope user --json
echo "deja-vu commit after update: $(sha deja-vu@m) (NEW is $NEW)"
run claude plugin update bundle@m --scope user --json

echo; echo "=== (c) uninstall a dependency the installed bundle needs"
data local-m deja-vu-m bundle-m
run claude plugin uninstall local@m --scope user --keep-data --json
run claude plugin list --json

echo; echo "=== (b) uninstall --keep-data then install moves the pin"
run claude plugin uninstall deja-vu@m --scope user --keep-data --json
run claude plugin install deja-vu@m --scope user --json
echo "deja-vu commit after reinstall: $(sha deja-vu@m) (NEW is $NEW)"
ls "$HOME/.claude/plugins/data/deja-vu-m" 2>&1 || true

echo; echo "=== (f) install the bundle again after it gains a dependency"
echo '{"name": "bundle", "version": "1.0.0", "dependencies": ["deja-vu", "local", "local2"]}' > "$m/plugins/bundle/.claude-plugin/plugin.json"
run claude plugin install bundle@m --scope user --json
run claude plugin list --json

echo; echo "=== (d) marketplace remove after --keep-data uninstalls"
run claude plugin uninstall bundle@m --scope user --keep-data --json
run claude plugin uninstall local2@m --scope user --keep-data --json
data local2-m
run claude plugin marketplace remove m --scope user --json
run claude plugin list --json
ls -la "$HOME/.claude/plugins/data/" 2>&1 || true
for d in bundle-m local-m local2-m deja-vu-m; do echo "$d: $(cat "$HOME/.claude/plugins/data/$d/marker" 2>&1 || true)"; done

echo; echo "=== (a2) pin moved, same version: a local git repository, commits A and B both at 1.0.0"
git init -q /src
mkdir -p /src/p/.claude-plugin
echo '{"name": "same", "version": "1.0.0"}' > /src/p/.claude-plugin/plugin.json
echo one > /src/p/README
git -C /src add -A
git -C /src -c user.email=m@m -c user.name=m commit -qm one
A=$(git -C /src rev-parse HEAD)
echo two > /src/p/README
git -C /src -c user.email=m@m -c user.name=m commit -qam two
B=$(git -C /src rev-parse HEAD)
mkdir -p /m2/.claude-plugin
cat > /m2/.claude-plugin/marketplace.json <<JSON
{"name": "m2", "owner": {"name": "measure"}, "plugins": [
 {"name": "same", "source": {"source": "git-subdir", "url": "file:///src", "path": "p", "sha": "$A"}}]}
JSON
run claude plugin marketplace add /m2 --scope user --json
run claude plugin install same@m2 --scope user --json
echo "same commit after install: $(sha same@m2) (A is $A)"
jq --arg s "$B" '.plugins[0].source.sha = $s' /m2/.claude-plugin/marketplace.json > /tmp/mk
mv /tmp/mk /m2/.claude-plugin/marketplace.json
run claude plugin marketplace update m2 --json
run claude plugin update same@m2 --scope user --json
echo "same commit after update: $(sha same@m2) (B is $B)"
run claude plugin uninstall same@m2 --scope user --keep-data --json
run claude plugin install same@m2 --scope user --json
echo "same commit after reinstall: $(sha same@m2) (B is $B)"

echo; echo "=== (f2) (f3) (g) (h): new dependencies, prune and dependents, marketplace m3"
mkdir -p /m3/.claude-plugin
for p in bundle local local2 local3 uses; do mkdir -p "/m3/plugins/$p/.claude-plugin"; done
cat > /m3/.claude-plugin/marketplace.json <<'JSON'
{"name": "m3", "owner": {"name": "measure"}, "plugins": [
 {"name": "bundle", "source": "./plugins/bundle"}, {"name": "local", "source": "./plugins/local"},
 {"name": "local2", "source": "./plugins/local2"}, {"name": "local3", "source": "./plugins/local3"},
 {"name": "uses", "source": "./plugins/uses"}]}
JSON
echo '{"name": "bundle", "version": "1.0.0", "dependencies": ["local"]}' > /m3/plugins/bundle/.claude-plugin/plugin.json
for p in local local2 local3; do echo "{\"name\": \"$p\", \"version\": \"1.0.0\"}" > "/m3/plugins/$p/.claude-plugin/plugin.json"; done
echo '{"name": "uses", "version": "1.0.0", "dependencies": ["local"]}' > /m3/plugins/uses/.claude-plugin/plugin.json
run claude plugin marketplace add /m3 --scope user --json
run claude plugin install bundle@m3 --scope user --json
echo '{"name": "bundle", "version": "1.0.0", "dependencies": ["local", "local2"]}' > /m3/plugins/bundle/.claude-plugin/plugin.json
echo "--- (f2) refresh, then install the bundle again"
run claude plugin marketplace update m3 --json
run claude plugin install bundle@m3 --scope user --json
run claude plugin list --json
echo '{"name": "bundle", "version": "1.1.0", "dependencies": ["local", "local2", "local3"]}' > /m3/plugins/bundle/.claude-plugin/plugin.json
echo "--- (f3) version bump with a new dependency: update, then install again"
run claude plugin marketplace update m3 --json
run claude plugin update bundle@m3 --scope user --json
run claude plugin list --json
run claude plugin install bundle@m3 --scope user --json
run claude plugin list --json
echo "--- (h) a plugin of the user's that needs local"
run claude plugin install uses@m3 --scope user --json
run claude plugin uninstall bundle@m3 --scope user --keep-data --json
echo "--- (g) prune dry run, no terminal"
run claude plugin prune --dry-run </dev/null
run claude plugin uninstall local@m3 --scope user --keep-data --json
run claude plugin list --json

echo; echo "=== (a3) stale pin of a bundle dependency, fixed by reinstalling the bundle"
mkdir -p /m4/.claude-plugin /m4/plugins/b4/.claude-plugin
cat > /m4/.claude-plugin/marketplace.json <<JSON
{"name": "m4", "owner": {"name": "measure"}, "plugins": [
 {"name": "b4", "source": "./plugins/b4"},
 {"name": "same", "source": {"source": "git-subdir", "url": "file:///src", "path": "p", "sha": "$A"}}]}
JSON
echo '{"name": "b4", "version": "1.0.0", "dependencies": ["same"]}' > /m4/plugins/b4/.claude-plugin/plugin.json
run claude plugin marketplace add /m4 --scope user --json
run claude plugin install b4@m4 --scope user --json
echo "same@m4 commit after the bundle install: $(sha same@m4) (A is $A)"
jq --arg s "$B" '.plugins[1].source.sha = $s' /m4/.claude-plugin/marketplace.json > /tmp/mk
mv /tmp/mk /m4/.claude-plugin/marketplace.json
run claude plugin marketplace update m4 --json
run claude plugin update same@m4 --scope user --json
run claude plugin uninstall same@m4 --scope user --keep-data --json
run claude plugin install b4@m4 --scope user --json
echo "same@m4 commit after the bundle reinstall: $(sha same@m4) (B is $B)"
run claude plugin uninstall b4@m4 --scope user --keep-data --json
run claude plugin prune --dry-run </dev/null

echo; echo "=== (i) remove --scope user from / (no project settings): the marketplace is gone"
mkdir -p /m5/.claude-plugin /tmp/neutral
echo '{"name": "m5", "owner": {"name": "measure"}, "plugins": []}' > /m5/.claude-plugin/marketplace.json
run claude plugin marketplace add /m5 --scope user --json
run claude plugin marketplace remove m5 --scope user --json
claude plugin marketplace list --json | jq -c '[.[].name]'

echo; echo "=== (j) the same from the home directory: ~/.claude/settings.json also counts as project settings"
cd "$HOME"
run claude plugin marketplace add /m5 --scope user --json
run claude plugin marketplace remove m5 --scope user --json
claude plugin marketplace list --json | jq -c '[.[].name]'
cd /tmp/neutral
run claude plugin marketplace remove m5 --scope user --json
run claude plugin marketplace remove m5 --json
claude plugin marketplace list --json | jq -c '[.[].name]'
cd /
INNER
