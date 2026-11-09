#!/usr/bin/env bash
# docs-nudge.sh, PreToolUse (Edit|Write|MultiEdit|Bash). When an edit uses a dependency whose locked
# version was released after the model's training cutoff (or whose date is unknown), and nothing
# this session looked that library up (see docs-mark.sh), deny the call once with a message telling
# the agent to fetch that version's docs first. The second attempt always passes (no loops).
# Manifests, nearest to the edited file up to the session cwd: package.json + package-lock.json (no lock,
# no lookup; likewise a go.mod `replace` target and a pyproject.toml without uv.lock/poetry.lock),
# go.mod, requirements.txt / pyproject.toml + uv.lock / poetry.lock, Cargo.toml + Cargo.lock,
# Chart.yaml + Chart.lock, *.tf + .terraform.lock.hcl. Dates: release-dates.json beside this script,
# then a per-user cache, then one registry lookup (3 s cap, none after the first failure; a failure
# is cached as "skip" for the day and never denies).
# Only public registries are asked: a dependency from a private registry, GOPRIVATE, a path or a git
# source gets no lookup and no nudge. Silent exit 0 without jq or on anything unexpected.
set -u
command -v jq >/dev/null 2>&1 || exit 0
input=$(cat)
here=$(cd "$(dirname "$0")" && pwd)
tool=$(jq -r '.tool_name // empty' <<<"$input" 2>/dev/null) || exit 0
cwd=$(jq -r '.cwd // "."' <<<"$input")
sid=$(jq -r '.session_id // empty' <<<"$input" | tr -cd 'A-Za-z0-9_-')
# PreToolUse input carries no model id, so one cutoff for every model; bump it when models move on.
cutoff=${DOCS_NUDGE_CUTOFF:-2026-06-01}
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
    for m in "$@"; do compgen -G "$d/$m" >/dev/null && { cd "$d" && return 0; }; done
    [ "$d" = "$cwd" ] || [ "$d" = / ] && return 0
    d=$(dirname "$d")
  done
}

libs=() # "eco|name|version|lockfile|repo"
# name<TAB>version pairs of a uv.lock, poetry.lock or Cargo.lock, public registry packages only:
# uv and cargo name their registry on a source line; poetry adds a [package.source] table otherwise.
tomllock() {
  awk -v eco="$2" '
    function out() { if (n != "" && v != "" && ok) { gsub(/"/, "", n); gsub(/"/, "", v); print n "\t" v } n = v = ""; ok = (eco == "poetry"); head = 1 }
    /^\[\[package\]\]/ { out(); next }
    /^\[/ { head = 0; if (eco == "poetry" && $0 == "[package.source]") ok = 0; next }
    !head { next }
    /^name = / { n = $3 }
    /^version = / { v = $3 }
    eco == "uv" && /^source = .*registry = "https:\/\/pypi\.org\/simple"/ { ok = 1 }
    eco == "cargo" && /^source = "(registry\+https:\/\/github\.com\/rust-lang\/crates\.io-index|sparse\+https:\/\/index\.crates\.io\/)"/ { ok = 1 }
    END { out() }' "$1"
}
npm_libs() {
  gotoroot package.json
  [ -f package.json ] && [ -f package-lock.json ] || return # no lock: the registry cannot be told
  while IFS=$'\t' read -r n range; do
    v=""
    if [ -f package-lock.json ]; then
      case "$(jq -r --arg n "$n" '.packages["node_modules/"+$n].resolved // empty' package-lock.json 2>/dev/null)" in
        "" | https://registry.npmjs.org/*) ;;
        *) continue ;;
      esac
      v=$(jq -r --arg n "$n" '.packages["node_modules/"+$n].version // empty' package-lock.json 2>/dev/null)
    fi
    [ -n "$v" ] || v=$(sed 's/^[^0-9]*//' <<<"$range")
    libs+=("npm|$n|$v|${v:+package-lock.json}")
  done < <(jq -r '(.dependencies // {}) + (.devDependencies // {}) | to_entries[] | "\(.key)\t\(.value)"' package.json 2>/dev/null)
}
go_libs() {
  gotoroot go.mod
  [ -f go.mod ] || return
  local priv p replaced
  replaced=$(awk '/^replace \(/ { b = 1; next } b && /^\)/ { b = 0; next } b { print $1 } /^replace [^(]/ { print $2 }' go.mod)
  priv=$( (command -v go >/dev/null 2>&1 && go env GOPRIVATE GONOPROXY) || printf '%s\n%s\n' "${GOPRIVATE:-}" "${GONOPROXY:-}")
  priv=$(tr '\n' ',' <<<"$priv")
  while read -r n v; do
    case "${n%%/*}" in *.*) ;; *) continue ;; esac # no dot in the host: not on the public proxy
    grep -qxF -- "$n" <<<"$replaced" && continue # replaced: not fetched from the registry
    p=0
    # shellcheck disable=SC2254
    while IFS= read -r pat; do [ -n "$pat" ] && case "$n" in $pat | $pat/*) p=1 ;; esac; done < <(tr ',' '\n' <<<"$priv")
    [ "$p" = 0 ] && libs+=("go|$n|$v|go.mod")
  done < <(awk '
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
    while IFS=$'\t' read -r n v; do libs+=("pypi|$n|$v|$lock"); done < <(tomllock "$lock" "${lock%.lock}")
  elif [ -f requirements.txt ] && ! grep -qE '^(-i|--(extra-)?index-url)' requirements.txt; then
    while read -r n v; do libs+=("pypi|$n|$v|requirements.txt"); done < <(sed -n 's/^\([A-Za-z0-9_.-][A-Za-z0-9_.-]*\)[[:space:]]*[=~<>!]*[[:space:]]*\([0-9][^[:space:];,]*\).*$/\1 \2/p' requirements.txt)
  fi
}
cargo_libs() {
  gotoroot Cargo.lock Cargo.toml
  if [ -f Cargo.lock ]; then
    while IFS=$'\t' read -r n v; do libs+=("crates|$n|$v|Cargo.lock"); done < <(tomllock Cargo.lock cargo)
  elif [ -f Cargo.toml ]; then
    while read -r n v; do libs+=("crates|$n|$v|Cargo.toml"); done < <(awk '
      /^\[/ { d = ($0 ~ /dependencies\]$/); next }
      d && /^[A-Za-z0-9_-]+ *=/ && /"/ && $0 !~ /(path|git|registry|workspace) *=/ {
        n = $1; v = $0
        if (v ~ /version *= *"/) sub(/.*version *= *"/, "", v); else sub(/^[^"]*"/, "", v)
        sub(/".*/, "", v); print n " " v }' Cargo.toml)
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
  gotoroot '*.tf'
  compgen -G './*.tf' >/dev/null || return
  while IFS='|' read -r src v; do
    src=${src#registry.terraform.io/}; src=${src#registry.opentofu.org/}
    case "$src" in */*/*) continue ;; esac # another registry host: not asked
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
      *.yaml | *.yml) gotoroot Chart.yaml; [ -f Chart.yaml ] && chart_libs ;;
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
  local eco=$1 n short id
  # escaped for the regexes below: a dot in socket.io is not a wildcard
  esc() { sed 's/[].[\*^$+?(){}|]/\\&/g' <<<"$1"; }
  n=$(esc "$2"); short=$(esc "${2##*/}")
  case "$eco" in
    npm) [ "$base" = package.json ] && grep -qE "\"$n\"" <<<"$text" && return 0
         grep -qE "(^|[^A-Za-z0-9_])(from|require\(|import\(?) *['\"]$n(/[^'\"]*)?['\"]" <<<"$text" ;;
    go) grep -qF "\"$2\"" <<<"$text" || grep -qF "\"$2/" <<<"$text" ;;
    pypi) id=$(esc "$(tr 'A-Z-' 'a-z_' <<<"$2")")
          tr 'A-Z-' 'a-z_' <<<"$text" | grep -qE "^[[:space:]]*(import|from)[[:space:]]+$id([[:space:].,]|$)" ;;
    crates) id=$(esc "$(tr '-' '_' <<<"$2")")
            grep -qE "(^|[^A-Za-z0-9_])$id::|extern crate $id" <<<"$text" ;;
    helm) [ "$tool" = Bash ] || grep -qE "^$short:" <<<"$text" || [ "$base" = values.yaml ] ;;
    tofu) [ "$tool" = Bash ] || grep -qE "(resource|data|provider) +\"${short}[_\"]" <<<"$text" ;;
  esac
}
netfail=""
today=$(date +%F)
# eco name version [repo] -> $rd: YYYY-MM-DD, "skip" (no answer: no nudge) or empty (the registry
# has no date). Sets netfail in this shell, so it is called without a subshell.
release_date() {
  rd=""
  rd_get "$@"
}
rd_get() {
  local key="$1:$2@$3" d="" url body f=".published_at"
  d=$(jq -r --arg k "$key" '.[$k].date // empty' "$here/release-dates.json" 2>/dev/null)
  [ -z "$d" ] && [ -f "$cache" ] && d=$(awk -F'\t' -v k="$key" '$1 == k { r = $2 } END { print r }' "$cache")
  case "$d" in "skip:$today") rd=skip; return ;; skip:*) d="" ;; esac
  [ -n "$d" ] && { rd=${d:0:10}; return; }
  if [ -n "$netfail" ] || ! command -v curl >/dev/null 2>&1; then rd=skip; return; fi
  case "$1" in
    npm) url="https://registry.npmjs.org/$2"; f='.time[$v] // empty' ;;
    go) url="https://proxy.golang.org/$2/@v/$3.info"; f='.Time // empty' ;; # upper-case module paths are not !-escaped: the proxy answers 404 and the library is skipped
    pypi) url="https://pypi.org/pypi/$2/$3/json"; f='.urls[0].upload_time_iso_8601 // empty' ;;
    crates) url="https://crates.io/api/v1/crates/$2/$3"; f='.version.created_at // empty' ;;
    helm) url="${4%/}/index.yaml" ;;
    tofu) url="https://api.github.com/repos/${2%%/*}/terraform-provider-${2##*/}/releases/tags/v$3" ;;
  esac
  # --proto: https only, also across redirects and for a chart repository named by a manifest
  if body=$(curl -fsSL -m 3 --proto =https --proto-redir =https -A makeit-docs-nudge "$url" 2>/dev/null); then
    if [ "$1" = helm ]; then
      d=$(awk -v n="  $2:" -v v="$3" '$0 == n { f = 1; next } f && /^  [^ -]/ { f = 0 }
        f && /created:/ { c = $2 } f && $1 == "version:" && $2 == v { print c; exit }' <<<"$body" | tr -d '"')
    else
      d=$(jq -r --arg v "$3" "$f" <<<"$body" 2>/dev/null)
    fi
    d=${d:0:10}
    [ -n "$d" ] && { mkdir -p "$(dirname "$cache")" && printf '%s\t%s\n' "$key" "$d" >>"$cache"; } 2>/dev/null
    rd=$d
  else
    # 22: an HTTP error for this library only; anything else (no route, timeout, TLS) stops the lookups
    [ $? = 22 ] || netfail=1
    { mkdir -p "$(dirname "$cache")" && printf '%s\tskip:%s\n' "$key" "$today" >>"$cache"; } 2>/dev/null
    rd=skip
  fi
}

msg=""
for l in "${libs[@]}"; do
  IFS='|' read -r eco n v lock repo <<<"$l"
  used "$eco" "$n" || continue
  mark="$state/$(tr '/@' '__' <<<"$eco-$n")"
  [ -e "$mark.nudged" ] && continue
  # Looked up this session: a web fetch or search named the library (docs-mark.sh logs those inputs).
  key=${n##*/}; [ "$eco" = go ] && key=$n
  # whole token: "vit" is not found in "vitest"
  grep -qiE "(^|[^A-Za-z0-9_-])$(sed 's/[^A-Za-z0-9_-]/\\&/g' <<<"$key")([^A-Za-z0-9_-]|$)" "$state/lookups.txt" 2>/dev/null && continue
  release_date "$eco" "$n" "$v" "$repo"; d=$rd
  [ "$d" = skip ] && continue
  if [ -n "$d" ] && [[ ! "$d" > "$cutoff" ]]; then continue; fi
  touch "$mark.nudged"
  when=${d:+released $d, after your training data (cutoff ${cutoff:0:7})}
  msg+="- $n $v (${lock:-manifest}): ${when:-release date unknown}."$'\n'
done
[ -n "$msg" ] || exit 0
reason="Stop: this edit uses a dependency version that may postdate what you know:
${msg}Its API may differ from what you remember. Before writing this, fetch the docs for exactly that version (the library's docs site, or its repository at that release tag), then retry. This check runs once per library."
jq -nc --arg r "$reason" '{hookSpecificOutput: {hookEventName: "PreToolUse", permissionDecision: "deny", permissionDecisionReason: $r}}'
