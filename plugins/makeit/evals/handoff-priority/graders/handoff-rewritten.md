---
type: llm
focus:
  source: file
  path: HANDOFF.md
weight: 2
---

You are shown `HANDOFF.md` from a repository after an agent added a `greet(name)` function and its test. Before the work it read:

```
# Handoff
- **Goal:** add greet(name)
- **Next step:** write src/greet.js and src/greet.test.js
- **Verify with:** npm run check:task
```

PASS if the file was rewritten after the work: it no longer gives "write src/greet.js and src/greet.test.js" as the next step, it records that the function and test exist (or that the goal is done), it still names a verify command, and it is under 30 lines.
FAIL if it is unchanged, if the next step still says to write greet.js, or if it is 30 lines or longer.
