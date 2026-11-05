#!/usr/bin/env bash
# options_test.sh [HOOK]: edge cases for options.sh with synthetic transcripts. Prints one line per
# case, exits 1 if any case fails. HOOK defaults to options.sh beside this file (pass a mutated
# copy to check that the tests catch a broken hook). hooks/test.sh runs it under LC_ALL=C and C.UTF-8.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
hook=${1:-$here/options.sh}
# Every case runs twice: through the awk pass, and through lists.mjs with the marked found at
# MARKED_PREFIX (hooks/test.sh installs one there). Without one the node run is skipped, said so.
if [ -z "${OPT_MODE:-}" ]; then
  st=0
  env OPT_MODE=awk bash "$0" "$@" || st=1
  if [ -n "${MARKED_PREFIX:-}" ]; then env OPT_MODE=node bash "$0" "$@" || st=1; else echo "SKIP  node path: MARKED_PREFIX is not set"; fi
  exit "$st"
fi
work=$(mktemp -d "${TMPDIR:-/tmp}/options-test.XXXXXX")
trap '/bin/rm -rf "$work"' EXIT
fail=0
mkdir -p "$work/empty" "$work/home"
export HOME="$work/home"
if [ "$OPT_MODE" = awk ]; then export MAKEIT_NO_NODE=1 MAKEIT_NPM_PREFIX="$work/empty"; else export MAKEIT_NPM_PREFIX="$MARKED_PREFIX"; fi
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
    echo "ok    $1 [$OPT_MODE]"
  else
    echo "FAIL  $1 [$OPT_MODE] (want $2, got $got, exit $code)"
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
check "handback inside a tilde fence" quiet "$human" false $'Ran:\n\n~~~\ntell me when\n~~~\n\nShipped.'
check "a \`\`\`js line inside a fence does not close it" quiet "$human" false $'Ran it.\n\n```\nx\n```js\nContinue?\n```'
check "question after inline triple backticks" fire "$human" false $'```make``` runs it.\n\nShould I ship it?'
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
check "joined: code block above, then the plan" fire "$human" false $'```\nx\n```\n1. a\n2. b\n\n'"$go"
check "joined: marker on the indented line under a choice" fire "$human" false $'1. step\n2. step\n\n1. Release now\n   (Recommended) prod is fixed\n2. Wait one night'
check "joined: #3 right under an item is not a heading" fire "$human" false $'1. a\n2. b\n#3 is the risky one\n\n'"$go"
check "joined: tab-indented paragraph in a step" fire "$human" false $'1. a\n\n\tmore about a\n\n'"$go"
check "joined: two spaces after the plan's marker" fire "$human" false $'1.  a\n\n    more\n2. b\n\n'"$go"
check "joined: a heading, then a list from 4" fire "$human" false $'## Left\n4. a\n5. b\n\n'"$go"
check "joined: indented code, then a list from 2" fire "$human" false $'Run this:\n\n    make test\n2. then deploy\n\n'"$go"
check "joined: a bullet's lazy line, then a list from 3" fire "$human" false $'- a\nlazy\n3. x\n\n'"$go"
check "joined: a bullet's wrapped line, then a list from 4" fire "$human" false $'- a\n  wrapped\n4. x\n5. y\n\n'"$go"
check "joined: a === heading, then a list from 4" fire "$human" false $'Title\n===\n4. a\n5. b\n\n'"$go"
check "joined: === right under an item is its text" fire "$human" false $'1. a\n2. b\n===\n\n'"$go"
check "apart: numbered steps nested under a bullet" quiet "$human" false $'- a\n  1. x\n  2. y\n\n'"$go"
check "apart: numbered steps under two bullets" quiet "$human" false $'- a\n  1. x\n- b\n  1. y\n\n'"$go"
check "apart: a deeper line, then a lazy line" quiet "$human" false $'1. a\n       deeper text\nlazy\n\n1. **Go** · recommended\n2. **Stop**'
check "apart: a deeper line in the plan, then a lazy line" quiet "$human" false $'1. step one\n2. step two\n        see below\nwhy it matters\n\n1. **Go** · recommended\n2. **Stop**'
check "apart: a heading in an item, then a lazy line" quiet "$human" false $'1. a\n   ### Notes\nlazy\n\n1. **Go** · recommended\n2. **Stop**'
check "apart: a rule in an item, then a lazy line" quiet "$human" false $'1. a\n   ***\nlazy\n\n1. **Go** · recommended\n2. **Stop**'
check "apart: HTML in an item, then a lazy line" quiet "$human" false $'1. a\n   <details>\nlazy\n\n1. **Go** · recommended\n2. **Stop**'
check "apart: <details> right under an item" quiet "$human" false $'1. a\n2. b\n<details>\n\n1. **Go** · recommended\n2. **Stop**'
check "apart: <div> right under an item" quiet "$human" false $'1. a\n2. b\n<div>x</div>\n\n1. **Go** · recommended\n2. **Stop**'
check "apart: an empty 1. under a sentence" quiet "$human" false $'Text\n1.\n2. b\n\n1. **Go** · recommended\n2. **Stop**'
check "apart: an empty 1., an empty line, then text" quiet "$human" false $'1.\n\n   text\n\n1. **Go** · recommended\n2. **Stop**'
check "apart: a ten-digit number is not an item" quiet "$human" false $'1234567890. a\n\n1. **Go** · recommended\n2. **Stop**'
check "apart: a tab after the marker" quiet "$human" false $'1.\ta\n\n   three\n\n1. **Go** · recommended\n2. **Stop**'
check "joined: a 4-backtick fence holds a 3-backtick line" fire "$human" false $'1. a\n2. b\n\n````\n```\n````\n3. c\n\n1. **Go** · recommended\n2. **Stop**'
check "joined: seven # is not a heading" fire "$human" false $'1. a\n2. b\n####### not\n\n1. **Go** · recommended\n2. **Stop**'
check "apart: code right after the marker, then a lazy line" quiet "$human" false $'1.      code\nlazy\n\n1. **Go** · recommended\n2. **Stop**'
check "not the layout: the marker on a lazy line under a choice" fire "$human" false $'1. a\n2. b\n\n1. Go\n(Recommended) because\n2. Stop'
check "apart: numbered steps under a * bullet with a bold label" quiet "$human" false $'* **Build**\n  1. make\n  2. sign\n\n1. **Go** · recommended\n   x\n2. **Stop**'
check "apart: a 4-space bullet is an item's text, not a block" quiet "$human" false $'**8.** Check\n    - logs\n9. Ship\n    - tag\n\n1. **Go** · recommended\n   x\n2. **Stop**'
check "joined: a fence in a 10. step stays in the step" fire "$human" false $' 10. Deploy\n    ```bash\n    make\n    ```\n\n1. **Go** · recommended\n   x\n2. **Stop**'
check "apart: a 4-backtick fence with a language tag holds a list" quiet "$human" false $'Draft:\n\n````markdown\n1. a\n2. b\n````\n\n1. **Go** · recommended\n   x\n2. **Stop**'
check "joined: inline triple backticks open no code block" fire "$human" false $'```make``` runs it.\n\n1. a\n2. b\n\n1. **Go** · recommended\n   x\n2. **Stop**'
check "joined: a plan of bold labels, then the choices" fire "$human" false $'1. **Backend**\n2. **App**\n\n'"$go"
check "joined: a bold-label plan with a marker, then the choices" fire "$human" false $'1. **Backend** · 3 files\n2. **App**\n\n'"$go"
check "joined: a plain line right under a bold-label plan" fire "$human" false $'1. **Backend**\n2. **App**\nPick:\n\n'"$go"
check "apart: a marked list above bold-number options" quiet "$human" false $'1. Plan A (recommended)\n2. Plan B\n\nDone.\n\n**1.** **Ship** · recommended\n**2.** **Wait**'
check "apart: options, then a bulleted not-checked line" quiet "$human" false "$go"$'\n\n- not checked: x'
check "apart: a marked list above options nested under a bullet" quiet "$human" false $'1. a (recommended)\n2. b\n\nNext:\n\n- Options:\n  1. **Go** · recommended\n  2. **Stop**'
check "three lines glued under the last option are an ending of their own" fire "$human" false "$go"$'\nline one\nline two\nline three'
check "apart: quote right under an item" quiet "$human" false $'1. a\n2. b\n> note\n\n'"$go"
check "apart: bullet right under an item" quiet "$human" false $'1. a\n2. b\n- note\n\n'"$go"
check "apart: lone dash right under an item" quiet "$human" false $'1. a\n2. b\n-\n\n'"$go"
check "apart: rule right under an item" quiet "$human" false $'1. a\n2. b\n***\n\n'"$go"
check "apart: code block right under an item" quiet "$human" false $'1. a\n2. b\n```\n1. x\n```\n\n'"$go"
check "apart: HTML comment right under an item" quiet "$human" false $'1. a\n2. b\n<!-- note -->\n\n'"$go"
check "apart: plain line under a step's code block" quiet "$human" false $'1. a\n   ```\n   x\n   ```\nPlain line\n\n'"$go"
check "apart: plain line under a step's indented code" quiet "$human" false $'1. a\n\n       code\nunindented\n\n'"$go"
check "apart: 4-space 1. is code, not a list" quiet "$human" false $'1. a\n\nText\n\n    1. code\n\n'"$go"
check "apart: 3-space paragraph under a 1.+3 spaces item" quiet "$human" false $'1.   a\n\n   para\n\n'"$go"
check "apart: 4. 5. under a sentence are its text" quiet "$human" false $'Done with steps 1-3. Left:\n4. tag the release\n5. publish\n\n1. **Go** · recommended\n   build it\n2. **Change something**\n   say what'
check "apart: 3. under a sentence is its text" quiet "$human" false $'Done up to\n3. Next is the tag\n\n'"$go"
check "one list: a numbered note after the choices" quiet "$human" false $'Done.\n\n1. **Go** · recommended\n2. **Stop**\n\n1. note'
check "one list: starting at 5" quiet "$human" false $'Done.\n\n5. **Go** · recommended\n6. **Stop**'
check "one list: 1, 1, marker on the second" quiet "$human" false $'Done.\n\n1. **Wait**\n1. **Go** · recommended'
check "one list: choices numbered 1, 1" quiet "$human" false $'Next:\n\n1. **Go** · recommended\n   why\n1. **Stop**'
check "one list: numbered steps nested under a choice" quiet "$human" false "$close"$'\n\n1. **Go** · recommended\n   1. dev first\n   2. then prod\n2. **Change something**'
check "one list: a code block inside a choice" quiet "$human" false $'Done.\n\n1. **Go** · recommended\n   ```\n   make it\n   ```\n2. **Stop**'
# The node path: what only marked can tell, and every way it can fail, which gives the awk's answer.
if [ "$OPT_MODE" = node ]; then
  check "node: plan and choices all numbered 1" fire "$human" false $'1. a\n1. b\n\n1. **Go** · recommended\n1. **Stop**'
  check "node: a fence indented 4 spaces is code" fire "$human" false $'Text\n\n    ```\n1. a\n2. b\n\n1. **Go** · recommended\n2. **Stop**'
  check "node: tab-indented step text, then the choices" fire "$human" false $'1. a\n\tmore\n\n1. **Go** · recommended\n2. **Stop**'
  MAKEIT_NPM_PREFIX="$work/empty" check "node: marked missing, the awk answers" fire "$human" false "$renum"
  mkdir -p "$work/node-fail" "$work/node-hang" "$work/node-odd"
  printf '#!/bin/sh\nexit 1\n' >"$work/node-fail/node"
  printf '#!/bin/sh\nexec sleep 30\n' >"$work/node-hang/node"
  printf '#!/bin/sh\necho maybe\n' >"$work/node-odd/node"
  chmod +x "$work"/node-*/node
  PATH="$work/node-fail:$PATH" check "node: node fails, the awk answers" fire "$human" false "$renum"
  PATH="$work/node-odd:$PATH" check "node: odd output, the awk answers" fire "$human" false "$renum"
  # Choices swallowed by an HTML block are no list on screen: not "renumbered".
  payload=$(jq -n --arg t "$human" --arg m $'<div>x</div>\n3. Tag it:\nPick:\n1. **Go** · recommended\n  ships\n2. **Wait**\n  keeps' '{hook_event_name: "Stop", stop_hook_active: false, prompt_id: "p1", transcript_path: $t, last_assistant_message: $m}')
  if bash "$hook" <<<"$payload" | grep -q 'belong, for the terminal'; then
    echo "FAIL  node: choices inside an HTML block are not called renumbered [$OPT_MODE]"
    fail=1
  else echo "ok    node: choices inside an HTML block are not called renumbered [$OPT_MODE]"; fi
  # A half-written marked (package.json, no lib): node errors, the awk answers.
  mkdir -p "$work/half/node_modules/marked"
  printf '{"name":"marked","module":"./lib/marked.esm.js"}' >"$work/half/node_modules/marked/package.json"
  MAKEIT_NPM_PREFIX="$work/half" check "node: a half-written marked, the awk answers" fire "$human" false "$renum"
  # marked found through a marked program on PATH (no prefix): a reply only marked reads right.
  mkdir -p "$work/mbin"
  ln -sf "$MARKED_PREFIX/node_modules/marked/bin/marked.js" "$work/mbin/marked"
  MAKEIT_NPM_PREFIX='' PATH="$work/mbin:$PATH" check "node: marked through a program on PATH" fire "$human" false $'Text\n\n    ```\n1. a\n2. b\n\n1. **Go** · recommended\n2. **Stop**'
  start=$(date +%s)
  PATH="$work/node-hang:$PATH" check "node: node hangs, the awk answers" fire "$human" false "$renum"
  if [ $(($(date +%s) - start)) -lt 5 ]; then echo "ok    node: a hang costs under 5 s [$OPT_MODE]"; else
    echo "FAIL  node: a hang took $(($(date +%s) - start)) s [$OPT_MODE]"
    fail=1
  fi
fi
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
