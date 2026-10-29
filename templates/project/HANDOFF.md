# Handoff

Rewritten by the agent before a context reset and at the end of a session that changed files. Under 30 lines. Committed with the work. A SessionStart hook prints it; a PreCompact hook and a Stop hook remind the agent to rewrite it.

- **Goal:** <the task in one sentence, as the owner stated it>
- **Branch and commit:** `<branch>` at `<short sha>`
- **Done:** <what is finished and verified, with the command that proved it>
- **Next step:** <the single next action>
- **Open questions:** <what needs the owner's answer, or "none">
- **Blocked on:** <what stops progress, or "nothing">
- **Verify with:** `<runner> check:task`; `<the one command that shows the change working>`
- **Do not touch:** <files or areas the owner said to leave alone, or "none">
