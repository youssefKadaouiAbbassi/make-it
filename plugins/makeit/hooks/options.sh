#!/usr/bin/env bash
# options.sh: Stop. When a turn the user started ends by handing them a question, a wait, an offer
# or a phrase to type in prose, block once and say where it belongs (templates/global/CLAUDE.md): a
# blocking decision in AskUserQuestion, a decision that needs reading in the reply shape that ends
# on numbered choices, suggestions after finished work as numbered options. A reply that already
# ends on such options passes, unless they sit right under another numbered list, which a terminal
# joins and renumbers. stop_hook_active guards the loop.
# Only prompts with origin.kind human or task-notification and entrypoint cli are checked:
# claude -p (sdk-cli, no origin) and messages from other sessions (origin peer) pass, and Stop does
# not fire for subagents.
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
# A fence closes on a run of its own character at least as long as the one that opened it; a line
# with a backtick after its opening run (```make``` runs it) is inline code, not a fence.
# A fence indented 4 spaces or more opens only inside a list item (from its marker line to the next
# line at the margin): at the margin marked reads it as indented code, and the lines after it show.
clean=$(printf '%s\n' "$msg" | awk '
  f != "" { if ($0 ~ ("^[[:space:]]*" f "+[[:space:]]*$")) f = ""; next }
  /^([-*+]|[0-9]+[.)])([ \t]|$)/ { item = 1 } /^[^[:space:]]/ && !/^([-*+]|[0-9]+[.)])([ \t]|$)/ { item = 0 }
  match($0, /^[[:space:]]*(```+|~~~+)/) && !/^[[:space:]]*`+[^`]+`/ && (item || $0 !~ /^(    |\t)/) { f = substr($0, RSTART, RLENGTH); sub(/^[[:space:]]+/, "", f); next }
  !/claude --resume/ && !/^[[:space:]]*>/') || exit 0
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
renumbered='The numbered choices that end this reply belong, for the terminal, to a numbered list above them, so it shows them renumbered (11, 12 instead of 1, 2): Markdown keeps a list going across empty lines, indented lines and a line written right under an item. The owner'\''s rule puts a plain line that is not a list item before the choices, with an empty line above it. Only that plain line and the choices are written again; the rest of the reply stays as it is.'
# lists TEXT: lists.mjs's answer, or nothing when it fails or takes more than 2 seconds. The
# watchdog holds no pipe of the hook's, so a hang never keeps Claude Code waiting.
lists() {
  local out pid dog
  out=$(mktemp "${TMPDIR:-/tmp}/makeit-lists.XXXXXX") || return 0
  printf '%s\n' "$1" | node "$(dirname "${BASH_SOURCE[0]}")/lists.mjs" >"$out" 2>/dev/null &
  pid=$!
  (sleep 2 && kill "$pid") >/dev/null 2>&1 </dev/null &
  dog=$!
  wait "$pid" 2>/dev/null || true
  kill "$dog" 2>/dev/null || true
  cat "$out"
  /bin/rm -f "$out"
}
marked_fn='function marked(l) {
    return tolower(l) ~ /\(recommended\)|\(recommand(é|É)\)|· recommended|· recommand(é|É)/ || l ~ /[*][*][^*]+[*][*][[:space:]]*·/
  }'
# marked itself reads the ending when node and makeit's marked are here (lists.mjs, 2 s at most);
# any other outcome (no node, no marked, an error, a hang, an odd answer) leaves it to the awk.
answer=
if [ -z "${MAKEIT_NO_NODE:-}" ] && command -v node >/dev/null 2>&1; then answer=$(lists "$msg"); fi
case "$answer" in
joined) emit "$renumbered" && exit 0 ;;
options) exit 0 ;;
hidden) opts=no ;;
*) opts= ;;
esac
[ -n "$opts" ] || opts=$(printf '%s\n' "$clean" | awk "$marked_fn"'
  NF == 0 { next }
  { i++ }
  /^[[:space:]]*(\*\*)?[0-9]+[.)](\*\*)?[[:space:]]/ { if (!run) { run = 1; rec = 0 }; end = i; if (marked($0)) rec = 1; next }
  run && /^[[:space:]][[:space:]]/ { end = i; if (marked($0)) rec = 1; next }
  { if (run) { run = 0; done_rec = rec; done_end = end } }
  END { if (run) { done_rec = rec; done_end = end }; if (done_rec && i - done_end <= 2) print "yes" }') || true
if [ "$opts" = yes ]; then
  # The options may still show renumbered: Claude Code numbers an ordered list as marked parses it,
  # its start plus each item's place. This pass follows marked's list rules on the raw reply and
  # says yes when an option up to the last marked one shows another number than the one written.
  # A list goes on across empty lines, lines indented into an item, and a lazy line (one written
  # right under an item whose last line is ordinary text). It ends at a fence, quote, heading,
  # rule, bullet, HTML block or 1) after 1. at the margin. A number other than 1, or an empty item,
  # cannot start a list under a paragraph line; lines indented into a bullet item are its own. As in
  # lists.mjs, the options begin after the last empty line above the last marked item, or at an item
  # numbered lower than the one above it, and the first of them must show the number written on it.
  joined=$(printf '%s\n' "$msg" | awk "$marked_fn"'
    { while (match($0, /^ *\t/)) $0 = substr($0, 1, RLENGTH - 1) "    " substr($0, RLENGTH + 1) }
    fence != "" { if ($0 ~ ("^ *" fence "+[ \t]*$")) fence = ""; next }
    /^[[:space:]]*$/ { blank = 1; para = 0; next }
    {
      match($0, /^ */); k = RLENGTH; rest = substr($0, k + 1)
      inner = on && k >= col && !(iempty && blank); rel = inner ? k - col : k
      isfence = rel < 4 && match(rest, /^(```+|~~~+)/) && rest !~ /^`+[^`]+`/; if (isfence) f = substr(rest, 1, RLENGTH)
      blk = rel < 4 && rest ~ /^((#|##|###|####|#####|######)([ \t]|$)|(- *- *-|\* *\* *\*|_ *_ *_)[-*_ ]*$|<!--|<\/?(address|article|aside|blockquote|details|dialog|div|dl|fieldset|figcaption|figure|footer|form|h[1-6]|header|hr|li|main|nav|ol|p|pre|section|summary|table|ul)([ \t>\/]|$))/
      if (inner) { if (marked($0)) mk[n, cnt[n] - 1] = 1; ipara = !isfence && !blk && rel < 4; blank = 0; next }
      if (bon && k >= bcol) { blank = 0; next }
      if (k <= 3 && match(rest, /^[0-9]+[.)]/) && RLENGTH <= 10 && substr(rest, RLENGTH + 1) ~ /^([ \t]|$)/) {
        m = RLENGTH; after = substr(rest, m + 1); empty = after ~ /^[ \t]*$/
        if (on || !para || (rest ~ /^1[.)]/ && !empty)) {
          if (substr(after, 1, 1) == "\t") sp = 4 - (k + m) % 4; else { match(after, /^ */); sp = RLENGTH }
          d = substr(rest, m, 1)
          if (!(on && d == delim)) { n++; start[n] = rest + 0; cnt[n] = 0; delim = d; on = 1 }
          c = cnt[n]++; wrote[n, c] = rest + 0; mk[n, c] = marked($0); gap[n, c] = blank
          col = k + m + (empty || sp > 4 ? 1 : sp); ipara = !empty && sp <= 4; iempty = empty
          blank = 0; para = 0; bon = 0; next
        }
      }
      brk = blk || rel < 4 && (rest ~ /^(>|[-*+]([ \t]|$))/ || !on && para && rest ~ /^=+[ \t]*$/)
      if (isfence) fence = f
      else if (on && ipara && !blank && !brk) next
      else if (bon && !blank && !brk) next
      # A bullet item: lines indented into it (numbered sub-steps too) and its lazy lines are its own.
      bon = !blk && rel < 4 && match(rest, /^[-*+]([ \t]+|$)/); if (bon) bcol = k + (RLENGTH > 1 ? RLENGTH : 2)
      on = 0; blank = 0; para = !isfence && !brk && (para || k < 4)
    }
    END {
      for (l = n; l >= 1; l--) { last = -1; for (c = 0; c < cnt[l]; c++) if (mk[l, c]) last = c; if (last >= 0) break }
      if (l < 1) exit
      first = 0; for (c = 1; c <= last; c++) if (gap[l, c] || wrote[l, c] < wrote[l, c - 1]) first = c
      if (wrote[l, first] != start[l] + first) print "yes"
    }') || true
  [ "$joined" = yes ] || exit 0
  emit "$renumbered"
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
