#!/usr/bin/env bash
# handoff.sh: HANDOFF.md across sessions.
#   SessionStart (startup|resume|clear|compact): print HANDOFF.md into context.
#   PreCompact: tell the agent to rewrite HANDOFF.md before the context is summarised.
#   Stop: if files changed in this session after HANDOFF.md was last written, block once with a
#         reason, so the agent rewrites it before ending the turn; stop_hook_active guards
#         against a loop. "This session" = modified after the session's start marker, which
#         SessionStart writes; edits from other sessions or copies do not count.
# Exits 0 on every path it does not understand. Needs jq and git; without them it does nothing.
set -u
input=$(cat)
command -v jq >/dev/null 2>&1 || exit 0
event=$(jq -r '.hook_event_name // empty' <<<"$input")
cwd=$(jq -r '.cwd // "."' <<<"$input")
f="$cwd/HANDOFF.md"
sid=$(jq -r '.session_id // empty' <<<"$input" | tr -c 'A-Za-z0-9_-' '_')
marks="${XDG_STATE_HOME:-$HOME/.local/state}/makeit/sessions"
mark="$marks/$sid"

json_escape() { jq -Rs . ; }

case "$event" in
  SessionStart)
    # First start only: resume, clear and compact keep the original start time.
    if [ -n "$sid" ] && [ ! -e "$mark" ]; then
      mkdir -p "$marks" 2>/dev/null && : >"$mark" 2>/dev/null
      find "$marks" -type f -mtime +7 -delete 2>/dev/null
    fi
    [ -f "$f" ] || exit 0
    body=$(head -c 6000 "$f")
    printf '{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":%s}}\n' \
      "$(printf 'HANDOFF.md (rewrite it, under 30 lines, before a context reset and at the end of a session that changed files):\n%s' "$body" | json_escape)"
    ;;
  PreCompact)
    [ -f "$f" ] || exit 0
    printf '{"systemMessage":"Context is about to be compacted: rewrite HANDOFF.md (under 30 lines) first if files changed."}\n'
    ;;
  Stop)
    [ "$(jq -r '.stop_hook_active // false' <<<"$input")" = "true" ] && exit 0
    [ -f "$f" ] || exit 0
    [ -n "$sid" ] && [ -e "$mark" ] || exit 0
    git -C "$cwd" rev-parse --is-inside-work-tree >/dev/null 2>&1 || exit 0
    # Working-tree files changed this session and newer than HANDOFF.md, HANDOFF.md excluded.
    stale=0
    while IFS= read -r path; do
      [ -n "$path" ] || continue
      [ "$path" = "HANDOFF.md" ] && continue
      if [ "$cwd/$path" -nt "$f" ] && [ "$cwd/$path" -nt "$mark" ]; then stale=1; break; fi
    done < <(git -C "$cwd" status --porcelain --untracked-files=all 2>/dev/null | cut -c4- | sed 's/^.* -> //')
    [ "$stale" = 1 ] || exit 0
    printf '{"decision":"block","reason":"Files changed this session and HANDOFF.md is older than them. Rewrite HANDOFF.md (under 30 lines: goal, branch and commit, done, next step, open questions, blocked on, verify with, do not touch), then stop."}\n'
    ;;
esac
exit 0
