#!/usr/bin/env bash
# push.sh: PreToolUse on `git push*` and `git -C * push*`. A push that names no branch (bare, HEAD,
#   --all, --mirror) from a deploy branch asks first. Every push in the command line is judged, each
#   in the directory a preceding `cd` or `git -C` points at. Named pushes are covered by the ask rules.
#   Deploy branches = main, master and the branches in `Bash(git push * <b>)` / `Bash(git push *:<b>)`
#   ask rules of <cwd>/.claude/settings.json.
# Exits 0 and prints nothing on anything it does not understand. Needs jq and git.
set -u
command -v jq >/dev/null 2>&1 || exit 0
input=$(cat)
cmd=$(jq -r '.tool_input.command // empty' <<<"$input")
cwd=$(jq -r '.cwd // "."' <<<"$input")
deploys="main
master"
if [ -f "$cwd/.claude/settings.json" ]; then
  deploys="$deploys
$(jq -r '.permissions.ask[]? | strings | capture("^Bash\\(git push \\*[ :](?<b>[^ )]+)\\)$").b' "$cwd/.claude/settings.json" 2>/dev/null)"
fi
absdir() { case "$2" in /*) printf '%s' "$2" ;; *) printf '%s/%s' "$1" "$2" ;; esac; }
ask() {
  jq -nc --arg r "git push from $1, a deploy branch: confirm before it goes out." \
    '{hookSpecificOutput:{hookEventName:"PreToolUse",permissionDecision:"ask",permissionDecisionReason:$r}}'
  exit 0
}
# judge DIR ARGS...: the words after `git push`.
judge() {
  local dir=$1 all=0 n=0 named=0 tags=0 skip=0 w b first=1
  shift
  for w in "$@"; do
    if [ "$skip" = 1 ]; then skip=0; continue; fi
    case "$w" in
      -o|--push-option|--repo|--receive-pack|--exec) skip=1 ;;
      --all|--mirror) all=1 ;;
      --tags) tags=1 ;;
      -*) ;;
      *) n=$((n + 1))
         if [ "$first" = 1 ]; then first=0; else
           case "$w" in HEAD|HEAD:HEAD) ;; *) named=1 ;; esac
         fi ;;
    esac
  done
  if [ "$all" = 1 ]; then
    while IFS= read -r b; do
      [ -n "$b" ] && git -C "$dir" show-ref -q --verify "refs/heads/$b" 2>/dev/null && ask "$b"
    done <<<"$deploys"
    return
  fi
  [ "$named" = 0 ] || return
  [ "$n" -gt 0 ] || [ "$tags" = 0 ] || return # `git push --tags` sends no branch
  b=$(git -C "$dir" symbolic-ref --short -q HEAD 2>/dev/null) || return
  [ -n "$b" ] || return
  printf '%s\n' "$deploys" | grep -Fxq -- "$b" && ask "$b"
}
set -f # the words below are never globbed
dir=$cwd
# ; | & and newline end a command; quotes, subshell parens and redirections are dropped.
while IFS= read -r seg; do
  set -- $seg
  [ "$#" -gt 0 ] || continue
  if [ "$1" = cd ] && [ "$#" -ge 2 ]; then dir=$(absdir "$dir" "$2"); continue; fi
  while [ "$#" -gt 0 ] && [ "$1" != git ]; do shift; done
  [ "$#" -gt 0 ] || continue
  shift
  gd=$dir
  while [ "$#" -gt 0 ]; do # git's own options before the subcommand
    case "$1" in
      -C) [ "$#" -ge 2 ] || break; gd=$(absdir "$dir" "$2"); shift 2 ;;
      -c) [ "$#" -ge 2 ] || break; shift 2 ;;
      --git-dir=*|--work-tree=*|--no-pager|-P) shift ;;
      *) break ;;
    esac
  done
  [ "${1:-}" = push ] || continue
  shift
  judge "$gd" "$@"
done <<<"$(printf '%s' "$cmd" | tr -d "'\"(){}" |
  sed -E 's/[0-9]*>&[0-9-]+//g; s/&>>?[^ ;|&]*//g; s/[0-9]*>>?[^ ;|&]*//g' | tr ';|&' '\n\n\n')"
exit 0
