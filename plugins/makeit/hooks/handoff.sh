#!/usr/bin/env bash
# handoff.sh: the handoff file across sessions. Default branch (or no git, or detached HEAD):
#   HANDOFF.md; any other branch: HANDOFF.<slug>.md, slug = branch with chars outside A-Za-z0-9._-
#   replaced by "-". Default branch = main or master, a branch with no commits, or the origin/HEAD target.
#   SessionStart (startup|resume|clear|compact): print the handoff into context (a branch without
#         its file reads HANDOFF.md); after compact also point at the active plan.
#   PreCompact: tell the agent to rewrite the handoff before the context is summarised.
#   Stop: if files changed in this session after the handoff was last written, say so once as Stop
#         additionalContext so the agent rewrites it; stop_hook_active guards against a loop.
#         "This session" means modified after the start marker SessionStart writes.
# Exits 0 on every path it does not understand. Needs jq and git; without them it does nothing.
set -u
input=$(cat)
command -v jq >/dev/null 2>&1 || exit 0
event=$(jq -r '.hook_event_name // empty' <<<"$input")
cwd=$(jq -r '.cwd // "."' <<<"$input")
# Sets name/f/onbranch. One git call on main/master; origin/HEAD and the unborn check only elsewhere.
resolve() {
  name=HANDOFF.md; onbranch=0
  local br def
  br=$(git -C "$cwd" symbolic-ref --short -q HEAD 2>/dev/null)
  case "$br" in
    ""|main|master) ;;
    *)
      def=$(git -C "$cwd" symbolic-ref --short -q refs/remotes/origin/HEAD 2>/dev/null); def=${def#origin/}
      # No origin/HEAD: a branch with no commits yet is the default.
      if [ "$br" != "$def" ] && { [ -n "$def" ] || git -C "$cwd" rev-parse --verify -q HEAD >/dev/null 2>&1; }; then
        name="HANDOFF.$(printf '%s' "$br" | tr -c 'A-Za-z0-9._-' '-').md"; onbranch=1
      fi ;;
  esac
  f="$cwd/$name"
}
sid=$(jq -r '.session_id // empty' <<<"$input" | tr -c 'A-Za-z0-9_-' '_')
marks="${XDG_STATE_HOME:-$HOME/.local/state}/makeit/sessions"
mark="$marks/$sid"

json_escape() { jq -Rs . ; }

case "$event" in
  SessionStart)
    resolve
    # First start only: resume, clear and compact keep the original start time.
    if [ -n "$sid" ] && [ ! -e "$mark" ]; then
      mkdir -p "$marks" 2>/dev/null && : >"$mark" 2>/dev/null
      find "$marks" -type f -mtime +7 -delete 2>/dev/null
    fi
    src=$f; [ -f "$src" ] || src="$cwd/HANDOFF.md"
    body=; [ -f "$src" ] && body=$(head -c 6000 "$src")
    msg=
    [ -n "$body" ] && msg=$(printf '%s (rewrite it as %s, under 30 lines, before a context reset and at the end of a session that changed files):\n%s' "${src##*/}" "$name" "$body")
    if [ "$(jq -r '.source // empty' <<<"$input")" = "compact" ]; then
      # The plan named in the handoff, else the newest one touched in the last 14 days.
      plan=
      for p in $(printf '%s' "$body" | grep -o 'docs/superpowers/plans/[A-Za-z0-9._-]*\.md'); do
        [ -f "$cwd/$p" ] && { plan=$p; break; }
      done
      if [ -z "$plan" ]; then
        p=$(ls -t "$cwd"/docs/superpowers/plans/*.md 2>/dev/null | head -n 1)
        [ -n "$p" ] && [ -n "$(find "$p" -mtime -14 2>/dev/null)" ] && plan=docs/superpowers/plans/${p##*/}
      fi
      [ -n "$plan" ] && msg="${msg:+$msg
}Context was compacted. The active plan is ${plan} (${plan##*/}): re-read it and the task list before going on."
    fi
    [ -n "$msg" ] || exit 0
    printf '{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":%s}}\n' "$(printf '%s' "$msg" | json_escape)"
    ;;
  PreCompact)
    resolve
    [ -f "$f" ] || [ "$onbranch" = 1 ] || exit 0
    printf '{"systemMessage":"Context is about to be compacted: rewrite %s (under 30 lines) first if files changed, with the open tasks, the plan in use and the last decision."}\n' "$name"
    ;;
  Stop)
    [ "$(jq -r '.stop_hook_active // false' <<<"$input")" = "true" ] && exit 0
    [ -n "$sid" ] && [ -e "$mark" ] || exit 0
    resolve
    [ -f "$f" ] || [ "$onbranch" = 1 ] || exit 0
    # Working-tree files changed this session and newer than HANDOFF.md, HANDOFF.md excluded.
    stale=0
    while IFS= read -r path; do
      [ -n "$path" ] || continue
      [ "$path" = "HANDOFF.md" ] || [ "$path" = "$name" ] && continue
      if [ "$cwd/$path" -nt "$f" ] && [ "$cwd/$path" -nt "$mark" ]; then stale=1; break; fi
    done < <(git -C "$cwd" status --porcelain --untracked-files=all 2>/dev/null | cut -c4- | sed 's/^.* -> //')
    [ "$stale" = 1 ] || exit 0
    jq -cn --arg n "$name" '{hookSpecificOutput: {hookEventName: "Stop", additionalContext: ("Files changed this session after " + $n + " was last written. The project'\''s rule: " + $n + " is rewritten before the turn ends, under 30 lines (goal, branch and commit, done, next step, open questions, blocked on, verify with, do not touch).")}}'
    ;;
esac
exit 0
