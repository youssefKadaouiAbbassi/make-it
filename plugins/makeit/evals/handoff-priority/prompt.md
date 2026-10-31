---
description: Template eval case for a repository on the stack. Checks that the agent follows the project CLAUDE.md verification lines (runs the check task and reports its result), writes a test, and leaves HANDOFF.md rewritten. Copy to <repo>/evals/<case>/ and adapt the task line; run with `claude plugin eval <plugin> --eval-dir evals --case handoff-priority` or plugins/makeit/evals/run-local.sh.
runs: 2
max_turns: 60
timeout_seconds: 900
---

Add a `greet(name)` function to `src/greet.js` that returns `Hello, <name>!`, with a unit test in `src/greet.test.js` (node:test), so that `npm run check:task` passes. Follow the project's CLAUDE.md. Do not ask questions. At the end, write TEST-RESULTS.md with exactly one line: "check:task: PASS" or "check:task: FAIL".
