#!/usr/bin/env bash
# docs.sh: SessionStart. Lists the direct dependencies of the project (cwd and two levels down; not
# the go directive: the standard library is not a dependency) and tells the agent to fetch the docs
# of the version in use before it relies on their API. docs-nudge.sh enforces that per edit.
# Silent and exit 0 without jq; without python3, TOML manifests are skipped.
set -u
input=$(cat)
command -v jq >/dev/null 2>&1 || exit 0
event=$(jq -r '.hook_event_name // empty' <<<"$input")
cwd=$(jq -r '.cwd // "."' <<<"$input")
[ -d "$cwd" ] || exit 0
[ "$event" = SessionStart ] || exit 0

deps=""
add() { deps+="$1"$'\n'; }
# A folder of repositories (outside git, with children holding their own .git directory): the
# root and each repository; never a worktree (.git is a file) or another folder.
roots=("$cwd")
depth=3
if ! git -C "$cwd" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  repos=()
  for d in "$cwd"/*/; do [ -d "$d.git" ] && repos+=("${d%/}"); done
  if [ ${#repos[@]} -gt 0 ]; then
    roots=("${repos[@]}")
    depth=2
  fi
fi
manifests() {
  find "$@" \( -name node_modules -o -name vendor -o -name .git -o -name target -o -name '.venv' \) -prune -o \
    \( -name go.mod -o -name package.json -o -name pyproject.toml -o -name Cargo.toml -o -name requirements.txt \) -type f -print 2>/dev/null
}
while IFS= read -r f; do
  rel=${f#"$cwd"/}
  case "$f" in
    */go.mod)
      while IFS= read -r line; do add "$line ($rel)"; done < <(awk '
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
done < <({
  [ "$depth" = 2 ] && manifests "$cwd" -maxdepth 1
  manifests "${roots[@]}" -maxdepth "$depth"
} | sort -u)

[ -n "$deps" ] || exit 0
count=$(printf '%s' "$deps" | grep -c .)
list=$(printf '%s' "$deps" | head -n 40)
[ "$count" -gt 40 ] && list+=$'\n'"... and $((count - 40)) more"
note="Project dependencies (direct, from manifests):
$list

Before you rely on a dependency's API, fetch the docs of the version in use (its docs site, or the repository at that release tag)."
printf '{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":%s}}\n' "$(jq -Rs . <<<"$note")"
exit 0
