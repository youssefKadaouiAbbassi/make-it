#!/usr/bin/env bash
# options.sh: Stop. When a turn the user started ends by handing them a question, a wait, an offer or
# a phrase to type in prose, block once. The reason sends a decision the work cannot go on without to
# AskUserQuestion, and suggestions after finished work to numbered options laid out like the picker
# (the closing rule in templates/global/CLAUDE.md). A reply that already ends on such options passes
# before any pattern is tried. stop_hook_active guards the loop.
# Only turns whose prompt line in the transcript has origin.kind human or task-notification and
# entrypoint cli are checked: claude -p (entrypoint sdk-cli, no origin) and messages from other
# sessions (origin peer) pass untouched, and Stop does not fire for subagents.
# Exits 0 on every path it does not understand. Needs jq; without it it does nothing.
set -euo pipefail
# Handback patterns, case-insensitive ERE, matched against the reply's last two paragraphs.
pats=(
  '\b(say|type|reply|send|ask|tell)( me)?( with| to)? ["“«]'
  '\b(reply|answer|type|press|pick)( with)? (\(?[0-9]\)?|\([a-c]\))'
  '\b(tell me (when|if|which|whether|what|once|and i)|(and|then) tell me)\b'
  '\b(let me know|say so|which option|please confirm|what i need from you|ask me to)\b'
  '\bif you( would|.d) rather\b'
  '\bi can\b[^.]{0,120}\bif you (want|like|prefer)\b'
  'waiting on you[*: ]+([^n*: ]|n[^o]|no[^t])'
  '\(recommended\)|\(recommand(é|É)\)|· recommended|· recommand(é|É)'
  '(^|\*\*)next( step)?:?\**:?[^.]{0,30}`'
  '\b(dis|tape|tapez|réponds|répondez|écris)( moi)?( avec)? ["“«]'
  '\b(dis-moi|dites-moi|tiens-moi au courant)\b'
)
input=$(cat) || exit 0
command -v jq >/dev/null 2>&1 || exit 0
[ "$(jq -r '.hook_event_name // empty' <<<"$input" 2>/dev/null)" = "Stop" ] || exit 0
[ "$(jq -r '.stop_hook_active // false' <<<"$input")" = "true" ] && exit 0
pid=$(jq -r '.prompt_id // empty' <<<"$input") || exit 0
tp=$(jq -r '.transcript_path // empty' <<<"$input") || exit 0
[ -n "$pid" ] && [ -f "$tp" ] || exit 0
# The turn's prompt line says who started it; fail open while it is not on disk yet.
who=$({ grep -F -m 20 "\"promptId\":\"$pid\"" "$tp" |
  jq -r 'select(.type == "user" and .origin.kind != null) | "\(.origin.kind) \(.entrypoint // "")"' |
  head -n 1; } 2>/dev/null) || true
case "$who" in "human cli" | "task-notification cli") ;; *) exit 0 ;; esac
msg=$(jq -r '.last_assistant_message // empty' <<<"$input") || exit 0
[ -n "$msg" ] || exit 0
# What the user reads, outside fenced code. Skipped: a resume command (claude --resume) and
# quoted lines (a draft for someone else, "> ...").
clean=$(printf '%s\n' "$msg" | awk '/^[[:space:]]*```/ { f = !f; next } !f && !/claude --resume/ && !/^[[:space:]]*>/') || exit 0
# The wanted ending: the reply's last run of numbered lines ("1. ", "2) ", "**1.** "; indented
# lines under an item and blank lines stay in the run) has a marked line and ends at most two lines
# before the end (room for ponytail's not-checked line). A line is marked by "(recommended)",
# "(recommandé)", "· recommended" or "· recommandé", or by the layout itself, a bold label then
# " · ", so other languages pass without a word list (and a run in that layout with no marker word
# passes too). The user answers it with one digit whatever trails it, so blocking would only print
# the options twice. É is listed because awk's tolower does not fold it in the C locale or in mawk.
opts=$(printf '%s\n' "$clean" | awk '
  function marked(l) {
    return tolower(l) ~ /\(recommended\)|\(recommand(é|É)\)|· recommended|· recommand(é|É)/ || l ~ /[*][*][^*]+[*][*][[:space:]]*·/
  }
  NF == 0 { next }
  { i++ }
  /^[[:space:]]*(\*\*)?[0-9]+[.)](\*\*)?[[:space:]]/ { if (!run) { run = 1; rec = 0 }; end = i; if (marked($0)) rec = 1; next }
  run && /^[[:space:]][[:space:]]/ { end = i; if (marked($0)) rec = 1; next }
  { if (run) { run = 0; done_rec = rec; done_end = end } }
  END { if (run) { done_rec = rec; done_end = end }; if (done_rec && i - done_end <= 2) print "yes" }') || true
[ "$opts" = yes ] && exit 0
# The last two paragraphs (the last one is often ponytail's not-checked line, with the handback
# just above it).
tail=$(printf '%s\n' "$clean" | awk 'BEGIN { RS = "" } { a = b; b = $0 } END { if (a != "") print a "\n"; print b }') || exit 0
# A paragraph of those two whose last line ends in "?" asks the user something.
asks=$(printf '%s\n' "$tail" | awk 'BEGIN { RS = "" } { sub(/\n+$/, ""); n = split($0, l, "\n"); print l[n] }' |
  sed -E 's/[][[:space:]*_`"»”)]+$//' | grep -c '?$') || true
hand=$(IFS='|' && printf '%s' "${pats[*]}")
# Stop additionalContext reaches Claude as a reminder at the end of the turn and continues it, with
# no "Stop hook error" (decision:block) under the reply; it is phrased as facts, not commands, as the
# hooks docs advise. AskUserQuestion is allowed only when the work cannot go on: on a false flag (a finished
# report ending on a rhetorical "?") the dialog would hide the report.
if [ "${asks:-0}" -gt 0 ] || grep -Eqi -- "$hand" <<<"$tail"; then
  jq -cn --arg r 'This reply ends on a question or a request in prose. The owner'\''s rule: a decision the work cannot go on without (a release, a merge, a choice, a step only they can do) goes in AskUserQuestion, the question naming the item and the fact it turns on, unless it needs reading first (a plan, a release step): then the reply ends with a TL;DR, the decisions as lettered lines with their picks, the full text and 2-3 numbered choices, nothing in a dialog preview; finished work with next-step suggestions ends on 2-3 numbered options under the close, laid out like the picker (1. **Action** · recommended, the reason indented on the next line), with nothing after them; an answer to a question ends on the answer; with nothing for the user, the reply ends on one plain line. The ending is rewritten without repeating the reply.' \
    '{hookSpecificOutput: {hookEventName: "Stop", additionalContext: $r}}'
fi
exit 0
