#!/usr/bin/env bash
# gen-hooks-doc.sh [OUT]: writes the hooks audit page (default docs/hooks.md).
# Reads the hooks of every plugin in .claude-plugin/marketplace.json: local plugins from the repo,
# url / git-subdir plugins fetched at their pinned sha. Needs git, jq and network.
# A hook whose command has no description below fails the run: classify it, then regenerate.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
out=${1:-$root/docs/hooks.md}
mf=$root/.claude-plugin/marketplace.json
work=$(mktemp -d "${TMPDIR:-/tmp}/hooks-doc.XXXXXX")
trap '/bin/rm -rf "$work"' EXIT

# norm: the command as a reader wants it (no quoting, no plugin-root variable, no launcher guards).
norm() {
  case "$1" in
    *impeccable\"\ hook) echo "skills/impeccable/scripts/impeccable hook" ;;
    node\ -e*) printf '%s\n' "$1" | grep -o 'hooks/[A-Za-z-]*\.js' | head -n 1 ;;
    *) printf '%s\n' "$1" | sed 's/"\${CLAUDE_PLUGIN_ROOT}\///; s/^\${CLAUDE_PLUGIN_ROOT}\///; s/"//g' ;;
  esac
}
# what: "<kind>: <effect>". kinds: injects (adds context), blocks (denies or asks), notifies, records.
what() { # $1 event, $2 normalised command
  case "$2" in
    hooks/handoff.sh)
      case "$1" in
        SessionStart) echo "injects: the handoff file into context" ;;
        PreCompact) echo "injects: a reminder to rewrite the handoff before the context is summarised" ;;
        Stop) echo "injects: once, a reminder to rewrite the handoff when files changed after it was written" ;;
        *) return 1 ;;
      esac ;;
    hooks/autoupdate.sh) echo "notifies: shows the note the last daily update left, then starts the daily update in the background" ;;
    hooks/docs.sh) echo "injects: the project's direct dependencies and a reminder to read the docs of the version in use" ;;
    hooks/skills.sh) echo "injects: a one-line skill-check reminder next to each prompt of two words or more" ;;
    hooks/docs-mark.sh) echo "records: which libraries this session looked up (no output)" ;;
    hooks/docs-nudge.sh) echo "blocks: once, an edit that uses a recent dependency nobody looked up" ;;
    hooks/options.sh) echo "blocks: once, a reply that ends by handing the user a question or a phrase to type in prose" ;;
    hooks/notify.sh) echo "notifies: desktop and ntfy message when the agent finishes or needs the user" ;;
    hooks/push.sh) echo "blocks: asks first for a push that names no branch from a deploy branch" ;;
    "hooks/deja.sh hook-context") echo "injects: a digest of past sessions at session start; says once if the deja binary is missing" ;;
    "hooks/deja.sh hook-precompact") echo "records: saves the session to deja memory before compaction (no output)" ;;
    "hooks/deja.sh hook-session-end") echo "records: marks the session as ended in deja (no output)" ;;
    hooks/hint.sh) echo "injects: one line pointing at deja search, no recall content" ;;
    hooks/ponytail-activate.js) echo "injects: the ponytail ruleset at session start" ;;
    hooks/ponytail-subagent.js) echo "injects: the ponytail ruleset into each subagent" ;;
    hooks/ponytail-mode-tracker.js) echo "records: the active ponytail mode in a flag file (no output)" ;;
    "hooks/run-hook.cmd session-start") echo "injects: the superpowers skill rules at session start" ;;
    skills/impeccable/scripts/impeccable\ hook)
      case "$1" in
        SessionStart) echo "injects: sets up the design build session" ;;
        PostToolUse) echo "injects: design findings for the edited UI file, after the edit" ;;
        Stop) echo "injects: the deep pass, remaining design findings over the UI files touched this session, and a reminder when a design build is unfinished; never blocks" ;;
        *) return 1 ;;
      esac ;;
    *) return 1 ;;
  esac
}

# fetch NAME: prints the plugin's source directory (repo path, or the pinned sha checked out).
fetch() {
  local n=$1 src url sha sub d
  src=$(jq -r --arg n "$n" '.plugins[] | select(.name == $n) | .source | if type == "string" then . else "" end' "$mf")
  [ -n "$src" ] && { echo "$root/$src"; return; }
  url=$(jq -r --arg n "$n" '.plugins[] | select(.name == $n) | .source.url' "$mf")
  sha=$(jq -r --arg n "$n" '.plugins[] | select(.name == $n) | .source.sha' "$mf")
  sub=$(jq -r --arg n "$n" '.plugins[] | select(.name == $n) | .source.path // ""' "$mf")
  d=$work/$n; mkdir -p "$d"
  git -C "$d" init -q && git -C "$d" fetch -q --depth 1 "$url" "$sha" && git -C "$d" checkout -q FETCH_HEAD
  echo "$d/$sub"
}

rows=$work/rows; : >"$rows"; srcs=$work/srcs; : >"$srcs"; none=()
while IFS=$'\037' read -r name kind sha; do
  dir=$(fetch "$name")
  if [ "$kind" = local ]; then echo "- \`$name\`: from the repo (\`plugins/$name\`)" >>"$srcs"
  else echo "- \`$name\`: pinned at \`${sha:0:12}\`" >>"$srcs"; fi
  files=()
  [ -f "$dir/hooks/hooks.json" ] && files+=("$dir/hooks/hooks.json")
  if [ -f "$dir/.claude-plugin/plugin.json" ]; then
    kindh=$(jq -r '.hooks | type' "$dir/.claude-plugin/plugin.json")
    case "$kindh" in
      null) ;;
      array | string)
        while IFS= read -r h; do
          [ -f "$dir/$h" ] || { echo "gen-hooks-doc: $name: hooks file $h not found" >&2; exit 1; }
          files+=("$dir/$h")
        done < <(jq -r '.hooks | if type == "array" then .[] else . end' "$dir/.claude-plugin/plugin.json") ;;
      object) jq '.hooks' "$dir/.claude-plugin/plugin.json" | jq '{hooks: .}' >"$work/$name.inline.json"; files+=("$work/$name.inline.json") ;;
      *) echo "gen-hooks-doc: $name: unreadable hooks shape ($kindh) in plugin.json" >&2; exit 1 ;;
    esac
  fi
  n=0
  for f in ${files[@]+"${files[@]}"}; do
    while IFS=$'\037' read -r ev m c i; do
      cmd=$(norm "$c"); w=$(what "$ev" "$cmd") || { echo "gen-hooks-doc: unclassified hook: $name $ev $cmd" >&2; exit 1; }
      m=${m//|/\\|}
      printf '| %s | %s | %s | %s | `%s` | %s |\n' "$name" "$ev" "${m:+\`$m\`}" "${i:+\`$i\`}" "$cmd" "$w" >>"$rows"; n=$((n+1))
    done < <(jq -r '.hooks // {} | to_entries[] | .key as $e | .value[] | (.matcher // "") as $m | .hooks[] | [$e, $m, .command, (.if // "")] | join("\u001f")' "$f")
  done
  [ "$n" -gt 0 ] || none+=("$name")
done < <(jq -r '.plugins[] | [.name, (.source | if type == "string" then "local" else "pinned" end), (.source.sha? // "")] | join("\u001f")' "$mf")

{
  echo "# Hooks"
  echo
  echo "Every hook that the bundled plugins run, generated by \`ci/gen-hooks-doc.sh\` and checked by \`ci/test.sh\`. Do not edit by hand."
  echo
  echo "What it does: **injects** adds context for the model, **blocks** denies or asks, **notifies** tells the user, **records** only writes a file."
  echo
  echo "Sources:"
  echo
  cat "$srcs"
  echo
  echo "| Plugin | Event | Matcher | If | Command | What it does |"
  echo "|---|---|---|---|---|---|"
  cat "$rows"
  echo
  echo "No hooks: $(printf '`%s`, ' "${none[@]}" | sed 's/, $//')."
  cat <<'EOF'

## Overlaps

Pairs of hooks that fire on the same event, and what was decided.

| Pair | Same thing? | Outcome |
|---|---|---|
| impeccable Stop and makeit `options.sh` Stop | No. impeccable runs its design detector over the UI files touched this session and adds findings; `options.sh` checks how the reply ends. | Both stay. impeccable has no reply-format check, so there is nothing to turn off. Its own switch (`impeccable hooks off`, per project) stays available if the deep pass is unwanted. |
| superpowers SessionStart and makeit `skills.sh` | Complementary. superpowers puts its skill rules in context once; `skills.sh` repeats a one-line reminder on later prompts, when the model tends to skip them. | Both stay. |
| makeit `skills.sh` and deja-vu `hint.sh` (UserPromptSubmit) | No. One reminds about skills, the other about past-session search. | Both stay. `skills.sh` is silent on a one-word prompt and `hint.sh` under four words, so a bare "1" or "go" gets neither. |
| ponytail mode tracker and the other UserPromptSubmit hooks | No. It only writes a flag file and prints nothing. | Both stay. |
| SessionStart: makeit `handoff.sh`, `docs.sh`, superpowers, ponytail, impeccable, deja-vu | No. Each adds different context (handoff, dependencies, skill rules, ponytail rules, design session, past-session digest). | All stay. |
| makeit `handoff.sh` Stop and `notify.sh` Stop | No. One asks for a rewrite of the handoff, the other sends a message and runs async. | Both stay. |
| impeccable PostToolUse and makeit `docs-nudge.sh` / `docs-mark.sh` (Edit and Write) | No. Different events: impeccable fires after an edit, `docs-nudge.sh` before it, `docs-mark.sh` after a web lookup. | Both stay. |
EOF
} >"$out"
