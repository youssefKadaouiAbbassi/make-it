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
| install | `<runner> install` |
| run | `<runner> dev` (see `/run-skill-generator` output in `.claude/skills/run/`) |
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
7. UI repos: after every UI change, open the page with the browser tools
   (chrome-devtools) at 1280 px and 375 px, read the screenshots, the console and
   any overflow, and fix what is wrong before finishing. Delete this item in a
   repository without a UI.

## Gotchas

- <known trap 1>
- <known trap 2>
