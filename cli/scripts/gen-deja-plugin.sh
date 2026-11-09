#!/usr/bin/env bash
# Generate plugins/deja-vu from vshulcz/deja-vu at a tag: keep the session summary, PreCompact,
# SessionEnd, the MCP server, skills and /deja; drop the hooks that inject recall on every prompt
# and tool call (hook-prompt, hook-tool, hook-tool-after); add the one-line search hint.
#   cli/scripts/gen-deja-plugin.sh <tag> [outdir]     (needs git, jq, network)
set -euo pipefail
tag=${1:?usage: gen-deja-plugin.sh <tag> [outdir]}
here=$(cd "$(dirname "$0")" && pwd)
out=${2:-$here/../../plugins/deja-vu}
tmp=$(mktemp -d "${TMPDIR:-/tmp}/gen-deja.XXXXXX")
trap '/bin/rm -rf "$tmp"' EXIT

git clone -q -c advice.detachedHead=false --depth 1 --branch "$tag" https://github.com/vshulcz/deja-vu.git "$tmp/src"
sha=$(git -C "$tmp/src" rev-parse HEAD)

/bin/rm -rf "$out"; mkdir -p "$out"
cp -R "$tmp/src/claude-plugin/." "$out/"

# The plugin carries makeit's catalog version, so a change here at the same upstream tag still updates.
ver=$(jq -r .metadata.version "$here/../../.claude-plugin/marketplace.json")
# The folder-root plugin.json is another tool's manifest; Claude Code reads .claude-plugin/plugin.json.
/bin/rm -f "$out/plugin.json"

# Drop by what the hook does (the deja subcommand), not by event name, so a renamed event still goes.
strip='.hooks |= (with_entries(.value |= (map(.hooks |= map(select(.command | test("hook-(prompt|tool|tool-after)\"?$") | not))) | map(select(.hooks | length > 0)))) | with_entries(select(.value | length > 0)))'
jq --arg v "$ver" "$strip | .version = \$v" "$out/.claude-plugin/plugin.json" >"$tmp/p.json"; mv "$tmp/p.json" "$out/.claude-plugin/plugin.json"
jq "$strip"' | .description = "Hint hook for makeit: one search nudge per prompt, no recall content (the upstream per-prompt and per-tool recall hooks are removed)." | .hooks.UserPromptSubmit = [{hooks: [{type: "command", command: "\"${CLAUDE_PLUGIN_ROOT}/hooks/hint.sh\"", timeout: 5}]}]' "$out/hooks/hooks.json" >"$tmp/h.json"; mv "$tmp/h.json" "$out/hooks/hooks.json"

{ printf '> makeit variant of deja-vu %s: keeps the session summary, PreCompact, SessionEnd, the MCP server, skills and /deja; the per-prompt and per-tool recall hooks described below are removed and replaced by one search hint.\n\n' "$tag"; cat "$out/README.md"; } >"$tmp/r.md"; mv "$tmp/r.md" "$out/README.md"
cp "$here/deja-hint.sh" "$out/hooks/hint.sh"; chmod +x "$out/hooks/hint.sh"
printf 'repo https://github.com/vshulcz/deja-vu\ntag %s\ncommit %s\n' "$tag" "$sha" >"$out/UPSTREAM"
