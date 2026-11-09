#!/usr/bin/env bash
# push.sh: PreToolUse on `git push*`. A bare push (no branch named) from a deploy branch asks first.
#   Deploy branches = main, master and the branches in `Bash(git push * <b>)` / `Bash(git push *:<b>)`
#   ask rules of <cwd>/.claude/settings.json. Named pushes are covered by those rules themselves.
# Exits 0 and prints nothing on anything it does not understand. Needs jq and git.
set -u
command -v jq >/dev/null 2>&1 || exit 0
input=$(cat)
cmd=$(jq -r '.tool_input.command // empty' <<<"$input")
cwd=$(jq -r '.cwd // "."' <<<"$input")
# Only the first push of the command line; shortcut: a later push in a chained command is not checked.
rest=$(printf '%s' "$cmd" | sed -n 's/.*git push\([^;&|]*\).*/\1/p' | head -1)
n=0
for w in $rest; do case "$w" in -*) ;; *) n=$((n + 1)) ;; esac; done
[ "$n" -le 1 ] || exit 0 # remote plus a ref: the push names its branch
br=$(git -C "$cwd" symbolic-ref --short -q HEAD 2>/dev/null) || exit 0
[ -n "$br" ] || exit 0
deploy=0
case "$br" in main|master) deploy=1 ;; esac
if [ "$deploy" = 0 ] && [ -f "$cwd/.claude/settings.json" ]; then
  jq -r '.permissions.ask[]? | strings | capture("^Bash\\(git push \\*[ :](?<b>[^ )]+)\\)$").b' "$cwd/.claude/settings.json" 2>/dev/null |
    grep -Fxq -- "$br" && deploy=1
fi
[ "$deploy" = 1 ] || exit 0
jq -nc --arg r "git push from $br, a deploy branch: confirm before it goes out." \
  '{hookSpecificOutput:{hookEventName:"PreToolUse",permissionDecision:"ask",permissionDecisionReason:$r}}'
