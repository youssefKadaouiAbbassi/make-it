---
name: verify
description: Verify a change in <project> before committing. Use before any commit or when asked to verify, check or test a change: runs the checks, starts the app and exercises what changed.
---

# Verify a change

1. Run `<check:task command>` and read the output; a failure stops here.
2. Start the app: `<start command>`; wait until `<ready signal>`. The full recipe is in
   `.claude/skills/run-<project>/SKILL.md`.
3. Exercise the changed behaviour once for real, on every surface the change reaches (a back-office
   field that feeds an app page: both): `<how to drive the changed path: request, click, command>`.
   Read the output, the logs and (for a UI) a screenshot of each screen, before and after.
4. Stop the app.
5. Report each command run this turn with its output, and the commit it ran against. A step
   that could not run is inconclusive, never a pass.
