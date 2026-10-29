#!/usr/bin/env bash
# order-ask.sh (optional, pairs with order-note.sh): PreToolUse on Bash. Runs variant-ask.sh's
# detection of the irreversible core and asks only when the user's latest prompt did not order
# that action. "Push it to main" then runs without a prompt; a push the user never asked for
# still asks. Fails closed: no recorded prompt, no jq, or an unreadable file means ask.
# This replaces the literal permissions.ask rules for the same commands, because an ask rule
# in settings prompts whatever a hook returns (permissions.md, PreToolUse hook decisions).
# Wire by hand in ~/.claude/settings.json, next to order-note.sh on UserPromptSubmit:
#   "PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command",
#      "command": "~/.claude/hooks/order-ask.sh", "timeout": 5}]}]
set -u
input=$(cat)
here=$(cd "$(dirname "$0")" && pwd)
verdict=$(printf '%s' "$input" | bash "$here/variant-ask.sh")
[ -n "$verdict" ] || exit 0
command -v jq >/dev/null 2>&1 || { printf '%s\n' "$verdict"; exit 0; }
reason=$(jq -r '.hookSpecificOutput.permissionDecisionReason // empty' <<<"$verdict")
cmd=$(jq -r '.tool_input.command // empty' <<<"$input" | tr '[:upper:]' '[:lower:]')
sid=$(jq -r '.session_id // empty' <<<"$input")
dir=${MAKEIT_STATE:-${XDG_STATE_HOME:-$HOME/.local/state}/makeit/orders}
file="$dir/${sid//[^A-Za-z0-9_-]/_}"
order=""
[ -n "$sid" ] && [ -r "$file" ] && order=$(tr '[:upper:]' '[:lower:]' <"$file")
pattern=""
case "$reason" in
  *"force push"*)
    if printf '%s' "$cmd" | grep -Eq -- '(^|[[:space:]])(-f|--force|--force-with-lease|--force-if-includes)([[:space:]=]|$)|\+[^[:space:]]*:'; then pattern='force'; else pattern='push.*(main|master)|(main|master).*push'; fi ;;
  *"reset --hard"*) pattern='reset|discard|revert|throw away|wipe|undo' ;;
  *"merge or DELETE"*) pattern='merge|delete|remove|close' ;;
  *"rm -rf"*) pattern='delete|remove|rm |wipe|clean' ;;
esac
if [ -n "$order" ] && [ -n "$pattern" ] && printf '%s' "$order" | grep -Eq "$pattern"; then
  exit 0
fi
printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask","permissionDecisionReason":%s}}\n' \
  "$(printf '%s (not named in your last message)' "$reason" | jq -Rs .)"
exit 0
