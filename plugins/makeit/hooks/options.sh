#!/usr/bin/env bash
# options.sh: Stop. When a turn the user started ends by handing them a question, a wait, an offer or
# a phrase to type in prose, block once. The reason sends a decision the work cannot go on without to
# AskUserQuestion, a decision that needs reading first (a plan, a release step) to the reply shape
# that ends on numbered choices, and suggestions after finished work to numbered options laid out
# like the picker (the rules in templates/global/CLAUDE.md). A reply that already ends on such options passes
# before any pattern is tried, unless they sit right under another numbered list (a terminal joins the
# two and renumbers the choices): then the reason asks for the plain line before them. stop_hook_active guards the loop.
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
# emit TEXT: Stop additionalContext reaches Claude as a reminder at the end of the turn and continues
# it, with no "Stop hook error" (decision:block) under the reply; it is phrased as facts, not
# commands, as the hooks docs advise.
emit() { jq -cn --arg r "$1" '{hookSpecificOutput: {hookEventName: "Stop", additionalContext: $r}}'; }
marked_fn='function marked(l) {
    return tolower(l) ~ /\(recommended\)|\(recommand(é|É)\)|· recommended|· recommand(é|É)/ || l ~ /[*][*][^*]+[*][*][[:space:]]*·/
  }'
opts=$(printf '%s\n' "$clean" | awk "$marked_fn"'
  NF == 0 { next }
  { i++ }
  /^[[:space:]]*(\*\*)?[0-9]+[.)](\*\*)?[[:space:]]/ { if (!run) { run = 1; rec = 0 }; end = i; if (marked($0)) rec = 1; next }
  run && /^[[:space:]][[:space:]]/ { end = i; if (marked($0)) rec = 1; next }
  { if (run) { run = 0; done_rec = rec; done_end = end } }
  END { if (run) { done_rec = rec; done_end = end }; if (done_rec && i - done_end <= 2) print "yes" }') || true
if [ "$opts" = yes ]; then
  # The options may still show renumbered: Claude Code numbers an ordered list as marked parses it,
  # its start plus each item's place, and Markdown keeps a list going across empty lines, lines
  # indented into the item and a line written right under an item. This pass follows those rules
  # on the raw reply (a code fence, a quote, a heading, a rule, a bullet or a 1) after 1. ends the
  # list) and says yes when an option up to the last marked one shows another number than the one
  # written. A list whose items all carry one number (1, 1) counts up from it and is left alone.
  joined=$(printf '%s\n' "$msg" | awk "$marked_fn"'
    function lead(s) { match(s, /^ */); return RLENGTH }
    fence != "" { if ($0 ~ ("^ *" fence)) fence = ""; next }
    /^[[:space:]]*$/ { blank = 1; next }
    {
      k = lead($0); rest = substr($0, k + 1); isfence = rest ~ /^(```|~~~)/
      if (on && k >= col) { if (isfence) fence = substr(rest, 1, 3); blank = 0; next }
      if (k <= 3 && match(rest, /^[0-9]+[.)]([ \t]|$)/)) {
        num = substr(rest, 1, RLENGTH); d = num; gsub(/[0-9 \t]/, "", d)
        if (!(on && d == delim)) { n++; start[n] = num + 0; cnt[n] = 0; delim = d; on = 1 }
        c = cnt[n]++; wrote[n, c] = num + 0; mk[n, c] = marked($0); col = k + RLENGTH; blank = 0; next
      }
      if (isfence) fence = substr(rest, 1, 3)
      else if (on && !blank && rest !~ /^(#|>|[-*+][ \t]|(- *- *-|\* *\* *\*|_ *_ *_)[-*_ ]*$)/) next
      on = 0; blank = 0
    }
    END {
      for (l = n; l >= 1; l--) { last = -1; for (c = 0; c < cnt[l]; c++) if (mk[l, c]) last = c; if (last >= 0) break }
      if (l < 1) exit
      same = 1; for (c = 1; c < cnt[l]; c++) if (wrote[l, c] != wrote[l, 0]) same = 0
      if (same) exit
      for (c = 0; c <= last; c++) if (wrote[l, c] != start[l] + c) { print "yes"; exit }
    }') || true
  [ "$joined" = yes ] || exit 0
  emit 'The numbered choices that end this reply belong, for the terminal, to a numbered list above them, so it shows them renumbered (11, 12 instead of 1, 2): Markdown keeps a list going across empty lines, indented lines and a line written right under an item. The owner'\''s rule puts a plain line that is not a list item before the choices, with an empty line above it. Only that plain line and the choices are written again; the rest of the reply stays as it is.'
  exit 0
fi
# The last two paragraphs (the last one is often ponytail's not-checked line, with the handback
# just above it).
tail=$(printf '%s\n' "$clean" | awk 'BEGIN { RS = "" } { a = b; b = $0 } END { if (a != "") print a "\n"; print b }') || exit 0
# A paragraph of those two whose last line ends in "?" asks the user something.
asks=$(printf '%s\n' "$tail" | awk 'BEGIN { RS = "" } { sub(/\n+$/, ""); n = split($0, l, "\n"); print l[n] }' |
  sed -E 's/[][[:space:]*_`"»”)]+$//' | grep -c '?$') || true
hand=$(IFS='|' && printf '%s' "${pats[*]}")
# AskUserQuestion is allowed only when the work cannot go on: on a false flag (a finished report
# ending on a rhetorical "?") the dialog would hide the report.
if [ "${asks:-0}" -gt 0 ] || grep -Eqi -- "$hand" <<<"$tail"; then
  emit 'This reply ends on a question or a request in prose. The owner'\''s rule: a decision the work cannot go on without (a delete, a go-ahead, a choice, a step only they can do) goes in AskUserQuestion, the question naming the item and the fact it turns on, unless it needs reading first (a plan, a release, merge or publish step): then the reply gives the full text, then a TL;DR and the decisions as lettered lines with their picks, a plain line, then 2-3 numbered choices, nothing in a dialog preview; finished work with next-step suggestions ends on 2-3 numbered options under the close, laid out like the picker (1. **Action** · recommended, the reason indented on the next line), with nothing after them; an answer to a question ends on the answer; with nothing for the user, the reply ends on one plain line. The ending is rewritten without repeating the reply.'
fi
exit 0
