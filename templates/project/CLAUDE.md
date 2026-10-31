# <project name>

<One sentence: what this software is and who uses it.>

**Product priority:** <what the user must get first, in the owner's words, one
sentence. Example for a tracker: "status and assignee at a glance first; scan
speed over refinement."> Every UI or UX decision defers to it.

Read `CONSTRAINTS.md` before writing code. Do not weaken it to make a change pass.
Read `HANDOFF.md` at the start of a session; rewrite it (under 30 lines) before
a context reset and at the end of a session that changed files.

## Commands

| task | command |
|---|---|
| install | `<install command>` |
| run | `<run command>` (the run recipe: `.claude/skills/run-<project>/SKILL.md`) |
| check:fast | `<runner> check:fast` (format, lint, types; under 10 s) |
| check:task | `<runner> check:task` (fast plus the related tests; under 90 s) |
| check:full | `<runner> check:full` (everything CI runs) |

Run `check:task` before saying a task is done; `check:full` before a push.

## Layout

- `<dir>`: <what lives there>
- `<dir>`: <what lives there>
- Tests sit next to the code as `*.test.<ext>`; fixtures under `tests/fixtures/`.

## Repository rules

- Package manager: `<pm>`. Do not switch it. Pin exact versions.
- Database changes go through migrations (expand/contract); never edit a
  migration that has been merged.
- Secrets come from the environment; `.env.example` lists every variable and
  the app refuses to start without the required ones.
- Branch from the default branch; feature-branch push and PR creation are free;
  merge is the owner's call.

## How to verify a change

1. No "done", "fixed" or "passing" without the command run this turn and its output.
2. Evidence names the commit it ran against.
3. A bug-fix test must fail with the fix reverted and pass with it.
4. Read the diff, not a subagent's report, before calling work complete.
5. A check that could not run is inconclusive, never a pass.
6. Start the app (`run`, `/verify`) and exercise the change before calling a
   feature done; if nothing can start it, say manual verification is required.

## Gotchas

- <known trap 1>
- <known trap 2>
