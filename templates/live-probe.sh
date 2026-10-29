#!/usr/bin/env bash
# Runs inside the eval image with HOME=/w/home (our global CLAUDE.md + rules) and cwd=/w/repo
# (our project CLAUDE.md, CONSTRAINTS.md, HANDOFF.md). One headless turn; prints what loaded.
set -uo pipefail
unset ANTHROPIC_API_KEY
cd /w/repo && git init -q && git add -A && git -c user.email=t@example.com -c user.name=t commit -qm init
claude -p --model claude-opus-5-5 --permission-mode bypassPermissions --output-format text \
  'Answer in five numbered lines, quoting exactly. 1: the product priority sentence from the project CLAUDE.md. 2: verification rule 3 from the project CLAUDE.md. 3: the first bullet under "Floor" in CONSTRAINTS.md (read the file). 4: the one-line rule about HANDOFF.md from the global instructions. 5: after reading src/index.ts, the runtime line from the TypeScript rules file if one was loaded, else "no rules file".'
echo "exit=$?"
