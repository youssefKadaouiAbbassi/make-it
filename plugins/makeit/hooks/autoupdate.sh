#!/usr/bin/env bash
# SessionStart: show the note the last daily update left (once), then start today's daily update
# in the background. `makeit update --auto` decides itself whether it is due (once a day, not
# while another runs, not when turned off), so this hook never waits and never fails a start.
set -uo pipefail
stack=${MAKEIT_DIR:-$HOME/.makeit}
note="$stack/pending-note"
if [ -s "$note" ] && line=$(head -n 1 "$note") && rm -f "$note"; then
  jq -cn --arg l "$line" '{systemMessage: $l, hookSpecificOutput: {hookEventName: "SessionStart", additionalContext: ("makeit: " + $l)}}'
fi
if command -v makeit >/dev/null 2>&1; then
  (nohup makeit update --auto </dev/null >/dev/null 2>&1 &)
fi
exit 0
