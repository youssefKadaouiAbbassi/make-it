#!/usr/bin/env bash
# notify.sh: tell the user at once, on the desktop and on ntfy, when the agent finishes or needs them.
# Events: Stop ("done", plus "still running: N" for background tasks), PreToolUse on
# AskUserQuestion|ExitPlanMode ("needs you: <tool>"), Notification only for permission_prompt
# ("needs permission"). Sends only the repository name and the event line.
# Never returns a decision; always exits 0 so it cannot block anything.
# Desktop: notify-send, else osascript, else powershell.exe. ntfy config: NTFY_TOPIC (without it
# no post), NTFY_URL (default https://ntfy.sh), NTFY_TOKEN (optional bearer token).
set -u
input=$(cat)
command -v jq >/dev/null 2>&1 || exit 0
event=$(jq -r '.hook_event_name // empty' <<<"$input" 2>/dev/null) || exit 0
repo=$(basename "$(jq -r '.cwd // "."' <<<"$input" 2>/dev/null)")
case "$event" in
  Stop)
    [ "$(jq -r ".stop_hook_active // false" <<<"$input" 2>/dev/null)" = true ] && exit 0 # re-fired after another hook blocked
    msg="done"
    n=$(jq -r '.background_tasks // empty | if type=="array" then length else 0 end' <<<"$input" 2>/dev/null)
    case "$n" in ''|*[!0-9]*|0) ;; *) msg="$msg, still running: $n" ;; esac ;;
  PreToolUse) msg="needs you: $(jq -r '.tool_name // "input"' <<<"$input" 2>/dev/null)" ;;
  Notification)
    [ "$(jq -r '.notification_type // empty' <<<"$input" 2>/dev/null)" = permission_prompt ] || exit 0
    msg="needs permission" ;;
  *) exit 0 ;;
esac
msg=${msg:0:200}
title="claude - $repo"
if command -v notify-send >/dev/null 2>&1; then
  notify-send "$title" "$msg" >/dev/null 2>&1 || true
elif command -v osascript >/dev/null 2>&1; then
  osascript -e 'on run a' -e 'display notification (item 1 of a) with title (item 2 of a)' -e 'end run' "$msg" "$title" >/dev/null 2>&1 || true
elif command -v powershell.exe >/dev/null 2>&1; then
  # The text goes into single quotes: strip everything but plain characters first.
  pm=$(printf '%s' "$msg" | tr -c 'A-Za-z0-9 :,._-' '_'); pt=$(printf '%s' "$title" | tr -c 'A-Za-z0-9 :,._-' '_')
  powershell.exe -NoProfile -Command "[void][Windows.UI.Notifications.ToastNotificationManager,Windows.UI.Notifications,ContentType=WindowsRuntime];\$x=[Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent('ToastText02');\$t=\$x.GetElementsByTagName('text');[void]\$t.Item(0).AppendChild(\$x.CreateTextNode('$pt'));[void]\$t.Item(1).AppendChild(\$x.CreateTextNode('$pm'));[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('Claude Code').Show([Windows.UI.Notifications.ToastNotification]::new(\$x))" >/dev/null 2>&1 || true
fi
if [ -n "${NTFY_TOPIC:-}" ] && command -v curl >/dev/null 2>&1; then
  # No array: bash 3.2 (macOS) treats an empty one under set -u as unset.
  curl -fsS -m 5 ${NTFY_TOKEN:+-H "Authorization: Bearer ${NTFY_TOKEN}"} -H "Title: $title" -d "$msg" "${NTFY_URL:-https://ntfy.sh}/${NTFY_TOPIC}" >/dev/null 2>&1 || true
fi
exit 0
