#!/usr/bin/env bash
# options_test.sh [HOOK]: edge cases for options.sh with synthetic transcripts. Prints one line per
# case, exits 1 if any case fails. HOOK defaults to options.sh beside this file (pass a mutated
# copy to check that the tests catch a broken hook). hooks/test.sh runs it under LC_ALL=C and C.UTF-8.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
hook=${1:-$here/options.sh}
work=$(mktemp -d "${TMPDIR:-/tmp}/options-test.XXXXXX")
trap '/bin/rm -rf "$work"' EXIT
fail=0
# transcript KIND ENTRYPOINT -> path of a one-prompt transcript (KIND "none" = no origin, as in -p)
transcript() {
  local f="$work/$1-$2.jsonl"
  if [ "$1" = none ]; then
    printf '{"type":"user","promptId":"p1","entrypoint":"%s","message":{"content":"x"}}\n' "$2" >"$f"
  else
    printf '{"type":"user","promptId":"p1","entrypoint":"%s","origin":{"kind":"%s"},"message":{"content":"x"}}\n' "$2" "$1" >"$f"
  fi
  printf '{"type":"assistant","message":{"content":[{"type":"text","text":"y"}]}}\n' >>"$f"
  printf '%s' "$f"
}
# check NAME WANT(fire|quiet) TRANSCRIPT ACTIVE MESSAGE
check() {
  local out code payload
  payload=$(jq -n --arg t "$3" --argjson a "$4" --arg m "$5" \
    '{hook_event_name: "Stop", stop_hook_active: $a, prompt_id: "p1", transcript_path: $t, last_assistant_message: $m}')
  set +e
  out=$(bash "$hook" <<<"$payload")
  code=$?
  set -e
  local got=quiet
  if jq -e '(.decision == "block") or ((.hookSpecificOutput.additionalContext // "") | length > 0)' <<<"$out" >/dev/null 2>&1; then got=fire; fi
  if [ "$got" = "$2" ] && [ "$code" = 0 ]; then
    echo "ok    $1"
  else
    echo "FAIL  $1 (want $2, got $got, exit $code)"
    fail=1
  fi
}
human=$(transcript human cli)
# A prose handback fires; anything that is not one stays quiet.
check "user turn ends with a question" fire "$human" false $'Fixed on dev.\n\nShould I release it to prod now?'
check "user turn ends with a statement" quiet "$human" false $'Fixed on dev, not on prod.\nA real outage was only simulated.'
check "loop guard: second stop passes" quiet "$human" true $'Should I release it to prod now?'
check "claude -p turn (sdk-cli, no origin)" quiet "$(transcript none sdk-cli)" false $'Should I release it?'
check "message from another session (peer)" quiet "$(transcript peer cli)" false $'Should I release it?'
check "background task notice (the user reads it)" fire "$(transcript task-notification cli)" false $'Done.\nTell me when the DNS is switched.'
check "prompt line not on disk yet: fail open" quiet "$work/missing.jsonl" false $'Should I?'
check "resume command after the question" fire "$human" false $'Should I release it?\n\n`cd /x && claude --resume abc`'
check "question only inside a code block" quiet "$human" false $'Ran it.\n\n```\nread -p "Continue?"\n```'
check "quoted draft for someone else" quiet "$human" false $'Draft for a colleague:\n\n> On accepte ou on contre-propose ?'
check "say a quoted phrase" fire "$human" false $'To ship it, say "release dev to master".'
check "Next: line with a prompt to type" fire "$human" false $'**Done:** fixed.\n**Next:** `finish the 2272 items`'
check "waiting on you: nothing" quiet "$human" false $'**Done:** fixed.\n**Waiting on you:** nothing.'
check "waiting on you: something" fire "$human" false $'Still waiting on you: the GitHub token approval.'
check "word boundary: sender \`UNICE\`" quiet "$human" false $'Keys go to OpenTofu (sender `UNICE`) and deploy.'
check "offer with if you'd rather" fire "$human" false $'If you\'d rather keep 1.12.0, say so.'
check "French question at the end" fire "$human" false $'C\'est en place sur dev.\nTu en penses quoi ?'
check "empty message" quiet "$human" false ''
check "handback above ponytail's not-checked line" fire "$human" false $'Upload a file on dev-admin. Tell me when it\'s done.\n\nI didn\'t watch the deploy finish.'
check "question above ponytail's not-checked line" fire "$human" false $'Should I release it now?\n\nI did not test a real outage.'
check "question three paragraphs up is not the end" quiet "$human" false $'Should I keep it?\n\nKept it.\n\nNothing else changed.'
check "then tell me" fire "$human" false $'Open the page and upload an image, then tell me.'
check "tell me a quoted word" fire "$human" false $'Then tell me "done".'
# New cases: the wanted ending passes, everything near it that is not that ending still fires.
close=$'The SLD fix is on dev, not on prod.\nA real SLD outage was only simulated in tests.'
check "close then numbered options, first (Recommended)" quiet "$human" false "$close"$'\n\n1. Release to prod now (Recommended): prod stops losing products.\n2. Wait one night: prod keeps the bug a night longer.'
check "question line, then the options" quiet "$human" false "$close"$'\n\nRelease it to prod?\n\n1. Release now (Recommended): the fix reaches prod.\n2. Wait one night: I watch dev first.'
check "options with an indented trade-off line each" quiet "$human" false "$close"$'\n\n1. Release now (Recommended)\n   Prod stops losing products.\n2. Wait one night\n   Prod keeps the bug.'
check "bold numbers" quiet "$human" false "$close"$'\n\n**1.** Release now (Recommended): prod is fixed.\n**2.** Wait: dev gets watched.'
check "lead-in says reply 1 or 2, then the options" quiet "$human" false $'Reply 1 or 2.\n1. Release now (Recommended): prod is fixed.\n2. Wait: dev gets watched.'
check "options, then ponytail's not-checked line" quiet "$human" false "$close"$'\n\n1. Release now (Recommended): prod is fixed.\n2. Wait: dev gets watched.\n\nNot checked: a real outage.'
check "options, then the resume command" quiet "$human" false "$close"$'\n\n1. Release now (Recommended): prod is fixed.\n2. Wait: dev gets watched.\n\n`cd /x && claude --resume abc`'
check "options, then one redundant line" quiet "$human" false "$close"$'\n\n1. Release now (Recommended): prod is fixed.\n2. Wait: dev gets watched.\n\nTell me which one.'
check "recommendation on option 2" quiet "$human" false "$close"$'\n\n1. Wait one night: dev gets watched.\n2. Release now (Recommended): prod is fixed.'
check "a wait on the user: one Done option" quiet "$human" false $'The TXT record has to be added by hand in the OVH panel.\n\n1. Done (Recommended): I check the record and finish the setup.'
check "(Recommended) in prose, no numbers" fire "$human" false "$close"$'\nRelease now (Recommended), or wait a night.'
check "numbered menu without a recommendation, then Which option?" fire "$human" false $'Implementation complete.\n\n1. Merge back to main locally\n2. Push and create a Pull Request\n3. Keep the branch as-is\n\nWhich option?'
check "numbered findings, no question" quiet "$human" false $'Three things are left on dev:\n1. the cost alert\n2. the secrets\n3. the idle memory'
check "options three lines up, then a new question" fire "$human" false $'1. Release now (Recommended): prod is fixed.\n2. Wait: dev gets watched.\n\nShould I also delete s3test?\nIt holds 2 GiB.\nNothing else changed.'
check "options only inside a code block" fire "$human" false $'Which one?\n\n```\n1. Release now (Recommended)\n2. Wait\n```'
# The picker-like layout and its marker words, in English and French; other languages pass by layout.
check "· recommended" quiet "$human" false "$close"$'\n\nRelease it?\n\n1. **Release now** · recommended\n   prod is fixed\n2. **Wait one night**\n   dev gets watched'
check "· Recommended, capitalised" quiet "$human" false "$close"$'\n\nRelease it?\n\n1. Release now · Recommended\n2. Wait one night'
check "(Recommandé)" quiet "$human" false $'C\'est sur dev.\n\nOn publie ?\n\n1. Publier maintenant (Recommandé) : la prod est corrigée.\n2. Attendre une nuit.'
check "· recommandé" quiet "$human" false $'C\'est sur dev.\n\nOn publie ?\n\n1. **Publier maintenant** · recommandé\n   la prod est corrigée\n2. **Attendre une nuit**\n   la dev est surveillée'
check "· RECOMMANDÉ, capitalised" quiet "$human" false $'C\'est sur dev.\n\nOn publie ?\n\n1. Publier maintenant · RECOMMANDÉ\n2. Attendre une nuit'
check "Spanish · recomendado passes by layout" quiet "$human" false $'Está en dev.\n\n¿Lo publico?\n\n1. **Publicar ahora** · recomendado\n   prod queda corregido\n2. **Esperar una noche**'
check "· recommended in a - list under a question" fire "$human" false $'Which one?\n\n- **Release now** · recommended\n- **Wait one night**'
check "· recommended in prose, no numbers" fire "$human" false "$close"$'\nRelease now · recommended, or wait a night.'
check "(Recommandé) in prose, no numbers" fire "$human" false $'C\'est sur dev.\nPublier maintenant (Recommandé), ou attendre.'
# A plan approval asked in the reply (templates/global/CLAUDE.md): the plan's own numbered steps
# sit right above the choices.
check "plan approval in the reply" quiet "$human" false $'## Backend\n1. PaymentMethodTypeEnum.INTERNE\n2. VENTES export: 6 columns at the end\n3. 409 on deleting a user with orders\n\nTL;DR\n- backend: Interne + 6 export columns\n- app: welcome texts in the org colour\n\nDecisions (my pick, change any by letter)\n  a. welcome text colour → a colour field\n  b. ID RCU → hidden without READ_USER_SSO_IDS\n\nYour answer:\n\n1. **Go** · recommended\n   build all three repos; stop before any push\n2. **Change something**\n   say what, or a: … to switch a decision'
# Choices that a terminal shows renumbered because Markdown joins them to a numbered list above
# (the 2026-11-05 Uni-CE reply showed 11, 12). Claude Code numbers a list as its start plus each
# item's place, from marked's parse; every expected result below is what marked 18.1.3 builds.
renum=$'The design\n1. **Why:** prod moves to OVH.\n2. **Cluster:** 3 zones.\n   - one node per zone\n3. **Not in this round:** the switch night.\n\n1. **Approve, write the build plan** · recommended\n   next is the step-by-step plan\n2. **Change something**\n   tell me which line'
go=$'1. **Go** · recommended\n2. **Stop**'
check "joined: choices under the plan's list" fire "$human" false "$renum"
check "joined: plain line with no empty line above" fire "$human" false $'1. step\n2. step\nPick one:\n\n'"$go"
check "joined: one plan step, then the choices" fire "$human" false $'1. step\n\n'"$go"
check "joined: plan indented 3 spaces" fire "$human" false $'   1. step\n   2. step\n\n'"$go"
check "joined: choices indented 1 space" fire "$human" false $'1. step\n2. step\n\n 1. **Go** · recommended\n 2. **Stop**'
check "joined: plan, then choices numbered 1, 1" fire "$human" false $'1. a\n2. b\n\n1. **Go** · recommended\n1. **Stop**'
check "apart: plain line with an empty line above" quiet "$human" false $'1. step\n2. step\n\nYour answer:\n\n'"$go"
check "apart: code block between" quiet "$human" false $'1. step\n2. step\n\n```\ncode\n```\n\n'"$go"
check "apart: quote between" quiet "$human" false $'1. step\n2. step\n\n> note\n\n'"$go"
check "apart: heading between" quiet "$human" false $'1. step\n2. step\n## Choices\n'"$go"
check "apart: rule between" quiet "$human" false $'1. a\n2. b\n\n---\n\n'"$go"
check "apart: 1. list, then 1) choices" quiet "$human" false $'1. step\n2. step\n\n1) **Go** · recommended\n2) **Stop**'
check "apart: bullet list above" quiet "$human" false $'- a\n- b\nnote\n\n'"$go"
check "apart: 2-space line after an empty line" quiet "$human" false $'1. a\n2. b\n\n  just two spaces\n\n'"$go"
check "apart: bold numbers are not a list" quiet "$human" false $'Left:\n**1.** the alert\n**2.** the secrets\n\n**1.** Fix both (Recommended)\n**2.** Leave them'
check "one list: choices numbered 1, 1" quiet "$human" false $'Next:\n\n1. **Go** · recommended\n   why\n1. **Stop**'
check "one list: numbered steps nested under a choice" quiet "$human" false "$close"$'\n\n1. **Go** · recommended\n   1. dev first\n   2. then prod\n2. **Change something**'
check "one list: a code block inside a choice" quiet "$human" false $'Done.\n\n1. **Go** · recommended\n   ```\n   make it\n   ```\n2. **Stop**'
# The rule's own example (templates/global/CLAUDE.md), dedented as a reply would print it, passes.
rule=$here/../../../templates/global/CLAUDE.md
if [ -f "$rule" ]; then
  example=$(awk '/laid out like the picker:/ { f = 1; next } f && /replies with one digit/ { exit } f' "$rule" | sed -E 's/^    //')
  if [ -n "$example" ]; then
    check "the CLAUDE.md template's example" quiet "$human" false "$close"$'\n\nWant one of these?\n\n'"$example"
  else
    echo "FAIL  the CLAUDE.md template's example was not found"
    fail=1
  fi
fi
# The context text, word for word: it acts on the model, so a change to it is a change to the hook.
# It goes out as Stop additionalContext (non-error feedback), never as decision:block, which Claude
# Code shows as "Stop hook error".
want='This reply ends on a question or a request in prose. The owner'\''s rule: a decision the work cannot go on without (a delete, a go-ahead, a choice, a step only they can do) goes in AskUserQuestion, the question naming the item and the fact it turns on, unless it needs reading first (a plan, a release, merge or publish step): then the reply gives the full text, then a TL;DR and the decisions as lettered lines with their picks, a plain line, then 2-3 numbered choices, nothing in a dialog preview; finished work with next-step suggestions ends on 2-3 numbered options under the close, laid out like the picker (1. **Action** · recommended, the reason indented on the next line), with nothing after them; an answer to a question ends on the answer; with nothing for the user, the reply ends on one plain line. The ending is rewritten without repeating the reply.'
payload=$(jq -n --arg t "$human" '{hook_event_name: "Stop", stop_hook_active: false, prompt_id: "p1", transcript_path: $t, last_assistant_message: "Should I release it?"}')
got=$(bash "$hook" <<<"$payload")
if [ "$(jq -r '.hookSpecificOutput.additionalContext' <<<"$got" 2>/dev/null)" = "$want" ] &&
  [ "$(jq -r '.hookSpecificOutput.hookEventName' <<<"$got")" = Stop ] && [ "$(jq -r '.decision // "none"' <<<"$got")" = none ]; then
  echo "ok    Stop additionalContext, word for word, no decision"
else
  echo "FAIL  Stop output differs: $got"
  fail=1
fi
want_renum='The numbered choices that end this reply belong, for the terminal, to a numbered list above them, so it shows them renumbered (11, 12 instead of 1, 2): Markdown keeps a list going across empty lines, indented lines and a line written right under an item. The owner'\''s rule puts a plain line that is not a list item before the choices, with an empty line above it. Only that plain line and the choices are written again; the rest of the reply stays as it is.'
payload=$(jq -n --arg t "$human" --arg m "$renum" '{hook_event_name: "Stop", stop_hook_active: false, prompt_id: "p1", transcript_path: $t, last_assistant_message: $m}')
got=$(bash "$hook" <<<"$payload")
if [ "$(jq -r '.hookSpecificOutput.additionalContext' <<<"$got" 2>/dev/null)" = "$want_renum" ] && [ "$(jq -r '.decision // "none"' <<<"$got")" = none ]; then
  echo "ok    joined choices: additionalContext, word for word"
else
  echo "FAIL  joined choices output differs: $got"
  fail=1
fi
out=$(bash "$hook" <<<'not json') && code=0 || code=$?
if [ -z "$out" ] && [ "$code" = 0 ]; then echo "ok    invalid JSON input: silent, exit 0"; else
  echo "FAIL  invalid JSON input (out=$out exit=$code)"
  fail=1
fi
exit "$fail"
