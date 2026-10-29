#!/usr/bin/env bash
# Scaffold: a tiny JavaScript repo (no dependencies, no network) carrying the stack's project
# templates. The eval runs in the directory this script prepares (cwd). Needs node 22+ and git.
set -euo pipefail
mkdir -p src .git
printf 'handoff-priority\n' >.git/eval-case
printf '%s\n' '{' '  "name": "eval-handoff-priority",' '  "private": true,' '  "type": "module",' '  "scripts": {' '    "check:fast": "node --check src/index.js",' '    "check:task": "node --check src/index.js && node --test src/*.test.js",' '    "check:full": "npm run check:task"' '  }' '}' >package.json
printf 'export {};\n' >src/index.js
tpl=${STACK_TEMPLATES:-"$(cd "$(dirname "$0")/../../../.." && pwd)/templates/project"}
if [ -d "$tpl" ]; then
  sed -e 's/<project name>/Greeter/' -e 's/<pm>/npm/' -e 's/<runner>/npm run/g' "$tpl/CLAUDE.md" \
    | awk 'BEGIN{s=0} /^\*\*Product priority:\*\*/{print "**Product priority:** the greeting must be correct for every name first; brevity over flourish."; s=1; next} s && /defers to it\.$/{s=0; next} !s' >CLAUDE.md
  cp "$tpl/CONSTRAINTS.md" CONSTRAINTS.md
else
  printf '# Greeter\n\nRun `npm run check:task` before saying a task is done and report its output.\n' >CLAUDE.md
fi
printf '# Handoff\n- **Goal:** add greet(name)\n- **Next step:** write src/greet.js and src/greet.test.js\n- **Verify with:** npm run check:task\n' >HANDOFF.md
git init -q 2>/dev/null || true
git -c user.email=eval@example.com -c user.name=eval add -A >/dev/null 2>&1 || true
git -c user.email=eval@example.com -c user.name=eval commit -qm scaffold >/dev/null 2>&1 || true
