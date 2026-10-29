#!/usr/bin/env bash
# order-note.sh (optional, pairs with order-ask.sh): UserPromptSubmit hook that records the
# user's latest prompt per session, so order-ask.sh can tell an ordered action from an
# unrequested one. The transcript file lags the conversation (hooks.md, transcript_path),
# which is why the prompt is stored here instead of read back from the transcript.
# Wire by hand in ~/.claude/settings.json:
#   "UserPromptSubmit": [{"hooks": [{"type": "command",
#      "command": "~/.claude/hooks/order-note.sh", "timeout": 5}]}]
set -u
input=$(cat)
command -v jq >/dev/null 2>&1 || exit 0
sid=$(jq -r '.session_id // empty' <<<"$input")
prompt=$(jq -r '.prompt // empty' <<<"$input")
[ -n "$sid" ] || exit 0
dir=${MAKEIT_STATE:-${XDG_STATE_HOME:-$HOME/.local/state}/makeit/orders}
mkdir -p "$dir" 2>/dev/null || exit 0
chmod 700 "$dir" 2>/dev/null
umask 077
printf '%s' "$prompt" >"$dir/${sid//[^A-Za-z0-9_-]/_}"
exit 0
