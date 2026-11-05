#!/usr/bin/env bash
# ntfy.sh: post a short line to an ntfy topic when the agent finishes or needs you.
# Events: Stop ("done"), PreToolUse on AskUserQuestion|ExitPlanMode ("needs you: <tool>"),
# Notification (the notification message). Sends only the repository name and the event line.
# Never returns a decision; always exits 0 so it cannot block anything.
# Config: NTFY_TOPIC (required; silently does nothing without it), NTFY_URL (default https://ntfy.sh),
# NTFY_TOKEN (optional bearer token). Put them in the settings `env` block or the shell environment.
set -u
input=$(cat)
[ -n "${NTFY_TOPIC:-}" ] || exit 0
command -v jq >/dev/null 2>&1 || exit 0
event=$(jq -r '.hook_event_name // empty' <<<"$input")
repo=$(basename "$(jq -r '.cwd // "."' <<<"$input")")
case "$event" in
  Stop) msg="done" ;;
  PreToolUse) msg="needs you: $(jq -r '.tool_name // "input"' <<<"$input")" ;;
  Notification) msg=$(jq -r '.message // "needs attention"' <<<"$input") ;;
  *) msg="$event" ;;
esac
msg=${msg:0:200}
# No array: bash 3.2 (macOS) treats an empty one under set -u as unset.
curl -fsS -m 5 ${NTFY_TOKEN:+-H "Authorization: Bearer ${NTFY_TOKEN}"} -H "Title: claude - $repo" -d "$msg" "${NTFY_URL:-https://ntfy.sh}/${NTFY_TOPIC}" >/dev/null 2>&1 || true
exit 0
