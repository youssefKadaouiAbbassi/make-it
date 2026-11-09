#!/usr/bin/env bash
# docs-nudge.sh, PreToolUse (Edit|Write|MultiEdit|Bash). When an edit uses a dependency whose locked
# version was released after the model's training cutoff (or whose date is unknown), and nothing
# this session looked that library up (see docs-mark.sh), deny the call once with a message telling
# the agent to fetch that version's docs first. The second attempt always passes (no loops).
# Manifests, nearest to the edited file up to the session cwd: package.json + package-lock.json,
# go.mod, requirements.txt / pyproject.toml + uv.lock / poetry.lock, Cargo.toml + Cargo.lock,
# Chart.yaml + Chart.lock, *.tf + .terraform.lock.hcl. Dates: release-dates.json beside this script,
# then a per-user cache, then one registry lookup (1 s cap, none after the first failure).
# Cutoff: DOCS_NUDGE_CUTOFF, else by the hook input's model id, else the default. Silent exit 0
# without jq or on anything unexpected.
set -u
command -v jq >/dev/null 2>&1 || exit 0
input=$(cat)
here=$(cd "$(dirname "$0")" && pwd)
tool=$(jq -r '.tool_name // empty' <<<"$input" 2>/dev/null) || exit 0
cwd=$(jq -r '.cwd // "."' <<<"$input")
sid=$(jq -r '.session_id // empty' <<<"$input" | tr -cd 'A-Za-z0-9_-')
model=$(jq -r '.model // empty' <<<"$input")
cutoff=2026-06-01
case "$model" in # one line per model family as their cutoffs diverge
  claude-opus-5* | claude-sonnet-5* | claude-haiku-5* | claude-fable-5*) cutoff=2026-06-01 ;;
esac
cutoff=${DOCS_NUDGE_CUTOFF:-$cutoff}
state="${TMPDIR:-/tmp}/makeit-docs/${sid:-nosession}"
mkdir -p "$state" 2>/dev/null || exit 0
cache="${XDG_CACHE_HOME:-$HOME/.cache}/makeit-docs/dates.tsv"
cd "$cwd" 2>/dev/null || exit 0
cwd=$(pwd)

file=$(jq -r '.tool_input.file_path // empty' <<<"$input")
case "$file" in "" | /*) ;; *) file="$cwd/$file" ;; esac
text=$(jq -r '[.tool_input.content, .tool_input.new_string, (.tool_input.edits // [] | .[].new_string)] | map(select(. != null)) | join("\n")' <<<"$input")
cmd=$(jq -r '.tool_input.command // empty' <<<"$input")

# Nearest folder from the edited file up to the cwd that holds one of the given files.
gotoroot() {
  local d=${file:+$(dirname "$file")} m
  [ -n "$d" ] || return 0
  while :; do
    for m in "$@"; do [ -f "$d/$m" ] && { cd "$d" && return 0; }; done
    [ "$d" = "$cwd" ] || [ "$d" = / ] && return 0
    d=$(dirname "$d")
  done
}

libs=() # "eco|name|version|lockfile|repo"
# name<TAB>version pairs of a uv.lock, poetry.lock or Cargo.lock.
tomllock() {
  awk '/^\[\[package\]\]/ { n = v = "" } /^name = / { n = $3 } /^version = / { v = $3 }
    n != "" && v != "" { gsub(/"/, "", n); gsub(/"/, "", v); print n "\t" v; n = v = "" }' "$1"
}
npm_libs() {
  gotoroot package.json
  [ -f package.json ] || return
  while IFS=$'\t' read -r n range; do
    v=""
    [ -f package-lock.json ] && v=$(jq -r --arg n "$n" '.packages["node_modules/"+$n].version // empty' package-lock.json 2>/dev/null)
    [ -n "$v" ] || v=$(sed 's/^[^0-9]*//' <<<"$range")
    libs+=("npm|$n|$v|${v:+package-lock.json}")
  done < <(jq -r '(.dependencies // {}) + (.devDependencies // {}) | to_entries[] | "\(.key)\t\(.value)"' package.json 2>/dev/null)
}
go_libs() {
  gotoroot go.mod
  [ -f go.mod ] || return
  while read -r n v; do libs+=("go|$n|$v|go.mod"); done < <(awk '
    /^require \(/ { r = 1; next } r && /^\)/ { r = 0; next }
    r && $0 !~ /\/\/ indirect/ && NF >= 2 { print $1 " " $2 }
    /^require [^(]/ && $0 !~ /\/\/ indirect/ { print $2 " " $3 }' go.mod)
}
py_libs() {
  gotoroot uv.lock poetry.lock pyproject.toml requirements.txt
  local lock=""
  [ -f uv.lock ] && lock=uv.lock
  [ -z "$lock" ] && [ -f poetry.lock ] && lock=poetry.lock
  if [ -n "$lock" ]; then
    while IFS=$'\t' read -r n v; do libs+=("pypi|$n|$v|$lock"); done < <(tomllock "$lock")
  elif [ -f requirements.txt ]; then
    while read -r n v; do libs+=("pypi|$n|$v|requirements.txt"); done < <(sed -n 's/^\([A-Za-z0-9_.-][A-Za-z0-9_.-]*\)[[:space:]]*[=~<>!]*[[:space:]]*\([0-9][^[:space:];,]*\).*$/\1 \2/p' requirements.txt)
  elif [ -f pyproject.toml ] && command -v python3 >/dev/null 2>&1; then
    while read -r n v; do libs+=("pypi|$n|$v|pyproject.toml"); done < <(python3 -I -c '
import sys, tomllib, re
for s in tomllib.load(open(sys.argv[1], "rb")).get("project", {}).get("dependencies", []):
    m = re.match(r"([A-Za-z0-9_.-]+)[^0-9]*([0-9][^\s;,]*)?", s)
    if m: print(m.group(1), m.group(2) or "")' pyproject.toml 2>/dev/null)
  fi
}
cargo_libs() {
  gotoroot Cargo.lock Cargo.toml
  if [ -f Cargo.lock ]; then
    while IFS=$'\t' read -r n v; do libs+=("crates|$n|$v|Cargo.lock"); done < <(tomllock Cargo.lock)
  elif [ -f Cargo.toml ]; then
    while read -r n v; do libs+=("crates|$n|$v|Cargo.toml"); done < <(awk '
      /^\[/ { d = ($0 ~ /dependencies\]$/) } d && /^[A-Za-z0-9_-]+ *=/ { n = $1; v = $0; sub(/.*version *= *"/, "", v); if (v == $0) { sub(/^[^"]*"/, "", v) } sub(/".*/, "", v); print n " " v }' Cargo.toml)
  fi
}
chart_libs() {
  local src=Chart.yaml; [ -f Chart.lock ] && src=Chart.lock
  [ -f "$src" ] || return
  # name / version / repository triples from the dependencies list (flat YAML, as helm writes it).
  while IFS='|' read -r n v r; do libs+=("helm|$n|$v|$src|$r"); done < <(awk '
    /^dependencies:/ { d = 1; next } d && /^[^ -]/ { d = 0 }
    d && /name:/ { sub(/.*name:[ ]*/, ""); n = $0 } d && /version:/ { sub(/.*version:[ ]*/, ""); v = $0 }
    d && /repository:/ { sub(/.*repository:[ ]*/, ""); r = $0 }
    d && n != "" && v != "" && r != "" { gsub(/["\047]/, "", n); gsub(/["\047]/, "", v); gsub(/["\047]/, "", r); print n "|" v "|" r; n = v = r = "" }' "$src")
}
tf_libs() {
  ls ./*.tf >/dev/null 2>&1 || return
  while IFS='|' read -r src v; do
    lv=""
    [ -f .terraform.lock.hcl ] && lv=$(awk -v s="$src" '$0 ~ "provider \"[^\"]*/"s"\"" { f = 1 } f && /version *=/ { gsub(/[^0-9.]/, "", $0); print; exit }' .terraform.lock.hcl)
    libs+=("tofu|$src|${lv:-$(sed 's/^[^0-9]*//' <<<"$v")}|${lv:+.terraform.lock.hcl}")
  done < <(awk '/source *=/ { s = $0; sub(/.*source *= *"/, "", s); sub(/".*/, "", s) } /version *=/ && s != "" { v = $0; sub(/.*version *= *"/, "", v); sub(/".*/, "", v); print s "|" v; s = "" }' ./*.tf)
}

case "$tool" in
  Edit | Write | MultiEdit)
    base=$(basename "$file")
    case "$file" in
      *.tf) tf_libs ;;
      *.go) go_libs ;;
      *.py) py_libs ;;
      *.rs) cargo_libs ;;
      *.yaml | *.yml) [ -f Chart.yaml ] && chart_libs ;;
      *) npm_libs ;;
    esac ;;
  Bash)
    base=""
    grep -qE '(^|[;&| ])(tofu|terraform)( |$)' <<<"$cmd" && tf_libs
    grep -qE '(^|[;&| ])helm( |$)' <<<"$cmd" && chart_libs ;;
  *) exit 0 ;;
esac
[ ${#libs[@]} -gt 0 ] || exit 0

used() { # does this edit use the library?
  local eco=$1 n=$2 short=${2##*/} id
  case "$eco" in
    npm) [ "$base" = package.json ] && grep -q "\"$n\"" <<<"$text" && return 0
         grep -qE "(from|require\(|import\() *['\"]$n(/[^'\"]*)?['\"]" <<<"$text" ;;
    go) grep -qF "\"$n\"" <<<"$text" || grep -qF "\"$n/" <<<"$text" ;;
    pypi) id=$(tr 'A-Z-' 'a-z_' <<<"$n")
          tr 'A-Z-' 'a-z_' <<<"$text" | grep -qE "^[[:space:]]*(import|from)[[:space:]]+$id([[:space:].,]|$)" ;;
    crates) id=$(tr '-' '_' <<<"$n")
            grep -qE "(^|[^A-Za-z0-9_])$id::|extern crate $id" <<<"$text" ;;
    helm) [ "$tool" = Bash ] || grep -qE "^$short:" <<<"$text" || [ "$base" = values.yaml ] ;;
    tofu) [ "$tool" = Bash ] || grep -qE "(resource|data|provider) +\"${short}[_\"]" <<<"$text" ;;
  esac
}
netfail=""
release_date() { # eco name version [repo] -> YYYY-MM-DD or empty
  local key="$1:$2@$3" d=""
  d=$(jq -r --arg k "$key" '.[$k].date // empty' "$here/release-dates.json" 2>/dev/null)
  [ -z "$d" ] && [ -f "$cache" ] && d=$(awk -F'\t' -v k="$key" '$1 == k { print $2; exit }' "$cache")
  if [ -z "$d" ] && [ -z "$netfail" ] && command -v curl >/dev/null 2>&1; then
    case "$1" in
      npm) d=$(curl -fsS -m 1 "https://registry.npmjs.org/$2" 2>/dev/null | jq -r --arg v "$3" '.time[$v] // empty') ;;
      go) d=$(curl -fsS -m 1 "https://proxy.golang.org/$2/@v/$3.info" 2>/dev/null | jq -r '.Time // empty') ;; # shortcut: no !-escaping of upper-case module paths, they stay "unknown"
      pypi) d=$(curl -fsS -m 1 "https://pypi.org/pypi/$2/$3/json" 2>/dev/null | jq -r '.urls[0].upload_time_iso_8601 // empty') ;;
      crates) d=$(curl -fsS -m 1 -A makeit-docs-nudge "https://crates.io/api/v1/crates/$2/$3" 2>/dev/null | jq -r '.version.created_at // empty') ;;
      helm) d=$(curl -fsSL -m 1 "${4%/}/index.yaml" 2>/dev/null | awk -v n="  $2:" -v v="$3" '$0 == n { f = 1; next } f && /^  [^ -]/ { f = 0 }
              f && /created:/ { c = $2 } f && $1 == "version:" && $2 == v { print c; exit }' | tr -d '"') ;;
      tofu) d=$(curl -fsS -m 1 "https://api.github.com/repos/${2%%/*}/terraform-provider-${2##*/}/releases/tags/v$3" 2>/dev/null | jq -r '.published_at // empty') ;;
    esac
    d=${d:0:10}
    [ -n "$d" ] && { mkdir -p "$(dirname "$cache")" && printf '%s\t%s\n' "$key" "$d" >>"$cache"; } 2>/dev/null
    [ -n "$d" ] || netfail=1
  fi
  printf '%s' "${d:0:10}"
}

msg=""
for l in "${libs[@]}"; do
  IFS='|' read -r eco n v lock repo <<<"$l"
  used "$eco" "$n" || continue
  mark="$state/$(tr '/@' '__' <<<"$eco-$n")"
  [ -e "$mark.nudged" ] && continue
  # Looked up this session: a web fetch or search named the library (docs-mark.sh logs those inputs).
  key=${n##*/}; [ "$eco" = go ] && key=$n
  grep -qiF -- "$key" "$state/lookups.txt" 2>/dev/null && continue
  d=$(release_date "$eco" "$n" "$v" "$repo")
  if [ -n "$d" ] && [[ ! "$d" > "$cutoff" ]]; then continue; fi
  touch "$mark.nudged"
  when=${d:+released $d, after your training data (cutoff ${cutoff:0:7})}
  msg+="- $n $v (${lock:-manifest}): ${when:-release date unknown}."$'\n'
done
[ -n "$msg" ] || exit 0
reason="Stop: this edit uses a dependency version that may postdate what you know:
${msg}Its API may differ from what you remember. Before writing this, fetch the docs for exactly that version (the library's docs site, or its repository at that release tag), then retry. This check runs once per library."
jq -nc --arg r "$reason" '{hookSpecificOutput: {hookEventName: "PreToolUse", permissionDecision: "deny", permissionDecisionReason: $r}}'
