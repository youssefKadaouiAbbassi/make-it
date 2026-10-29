#!/usr/bin/env bash
# check-task-stop.sh (optional): Stop hook that runs the repository's check:task when files
# changed in this turn's working tree and blocks a false "done" once if it fails. Costs up to the
# task's budget (90 s by CONSTRAINTS.md) per stop, so it is not wired by default. Wire by hand:
#   "hooks": {"Stop": [{"hooks": [{"type": "command",
#      "command": "~/.claude/hooks/check-task-stop.sh", "timeout": 120}]}]}
# Finds the task in package.json scripts, a Makefile target, a Taskfile task or a mise task;
# does nothing when none exists, when the tree is clean, outside git, or on the second pass.
set -u
input=$(cat)
command -v jq >/dev/null 2>&1 || exit 0
[ "$(jq -r '.hook_event_name // empty' <<<"$input")" = "Stop" ] || exit 0
[ "$(jq -r '.stop_hook_active // false' <<<"$input")" = "true" ] && exit 0
cwd=$(jq -r '.cwd // "."' <<<"$input")
git -C "$cwd" rev-parse --is-inside-work-tree >/dev/null 2>&1 || exit 0
[ -n "$(git -C "$cwd" status --porcelain --untracked-files=all 2>/dev/null)" ] || exit 0
cmd=""
if [ -f "$cwd/package.json" ] && jq -e '.scripts["check:task"]' "$cwd/package.json" >/dev/null 2>&1; then
  cmd="npm run -s check:task"
elif [ -f "$cwd/Makefile" ] && grep -Eq '^check:task:' "$cwd/Makefile"; then
  cmd="make check:task"
elif [ -f "$cwd/Taskfile.yml" ] && grep -Eq '^  check:task:' "$cwd/Taskfile.yml"; then
  cmd="task check:task"
elif [ -f "$cwd/mise.toml" ] && grep -Eq '^\[tasks\."?check:task"?\]' "$cwd/mise.toml"; then
  cmd="mise run check:task"
fi
[ -n "$cmd" ] || exit 0
out=$(cd "$cwd" && timeout "${CHECK_TASK_TIMEOUT:-100}" bash -c "$cmd" 2>&1); rc=$?
[ "$rc" = 0 ] && exit 0
tail=$(printf '%s' "$out" | tail -n 15)
printf '{"decision":"block","reason":%s}\n' \
  "$(printf 'check:task failed (exit %s) after files changed; fix it or report the failure, do not claim done. Last lines:\n%s' "$rc" "$tail" | jq -Rs .)"
exit 0
