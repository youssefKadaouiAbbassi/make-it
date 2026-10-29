#!/usr/bin/env bash
# docs.sh: SessionStart. Lists the project's direct dependencies from its manifests (go.mod,
# package.json, pyproject.toml, Cargo.toml, requirements.txt; cwd and two levels down) and tells
# the agent to check the docs index and index what is missing or out of date before it relies
# on that library. Runs no npx itself: the agent has list_libraries and the docs-manage skill.
# Silent when no manifest is found. Needs jq; python3 is optional (TOML manifests are skipped
# without it). Exits 0 on every path.
set -u
input=$(cat)
command -v jq >/dev/null 2>&1 || exit 0
[ "$(jq -r '.hook_event_name // empty' <<<"$input")" = "SessionStart" ] || exit 0
cwd=$(jq -r '.cwd // "."' <<<"$input")
[ -d "$cwd" ] || exit 0

deps=""
add() { deps+="$1"$'\n'; }
while IFS= read -r f; do
  rel=${f#"$cwd"/}
  case "$f" in
    */go.mod)
      while IFS= read -r line; do add "$line ($rel)"; done < <(awk '
        /^go [0-9]/ { print "go " $2 }
        /^require \(/ { inreq = 1; next }
        inreq && /^\)/ { inreq = 0; next }
        inreq && $0 !~ /\/\/ indirect/ && NF >= 2 { print $1 " " $2 }
        /^require [^(]/ && $0 !~ /\/\/ indirect/ { print $2 " " $3 }' "$f")
      ;;
    */package.json)
      while IFS= read -r line; do add "$line ($rel)"; done < <(jq -r '(.dependencies // {}) + (.devDependencies // {}) | to_entries[] | "\(.key) \(.value)"' "$f" 2>/dev/null)
      ;;
    */requirements.txt)
      while IFS= read -r line; do add "$line ($rel)"; done < <(sed -n 's/^\([A-Za-z0-9_.-][A-Za-z0-9_.-]*\)[[:space:]]*\([=<>~!].*\)$/\1 \2/p' "$f")
      ;;
    */pyproject.toml|*/Cargo.toml)
      command -v python3 >/dev/null 2>&1 || continue
      while IFS= read -r line; do add "$line ($rel)"; done < <(python3 -I -c '
import sys, tomllib, re
d = tomllib.load(open(sys.argv[1], "rb"))
if sys.argv[1].endswith("Cargo.toml"):
    for k, v in d.get("dependencies", {}).items():
        print(k, v if isinstance(v, str) else v.get("version", ""))
else:
    for s in d.get("project", {}).get("dependencies", []):
        m = re.match(r"([A-Za-z0-9_.-]+)\s*(.*)", s)
        if m: print(m.group(1), m.group(2))
' "$f" 2>/dev/null)
      ;;
  esac
done < <(find "$cwd" -maxdepth 3 \( -name node_modules -o -name vendor -o -name .git -o -name target -o -name '.venv' \) -prune -o \
  \( -name go.mod -o -name package.json -o -name pyproject.toml -o -name Cargo.toml -o -name requirements.txt \) -type f -print 2>/dev/null | sort)

[ -n "$deps" ] || exit 0
count=$(printf '%s' "$deps" | grep -c .)
list=$(printf '%s' "$deps" | head -n 40)
[ "$count" -gt 40 ] && list+=$'\n'"... and $((count - 40)) more"
note="Project dependencies (direct, from manifests):
$list

Docs index: before you rely on one of these libraries for the task, check what is indexed with the docs-search skill (or list_libraries). If the library is missing, or indexed at a different version than the manifest, index it with the docs-manage skill: find the official documentation URL for that version and scrape it with --version. If its indexedAt is more than 30 days old, run refresh. Index only what the task needs, then search it (docs-search skill or search_docs)."
printf '{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":%s}}\n' "$(jq -Rs . <<<"$note")"
exit 0
