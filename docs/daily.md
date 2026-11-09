# Working with makeit every day

Once makeit is set up, you mostly forget it is there. You open Claude Code, give it work, and
review what comes back. This page walks through a normal day and shows what makeit does at each
point, and what you can do when you want something different.

## Opening a session

Open Claude Code in a project, as usual (`claude`, or `claude --resume` to continue a
conversation). Three things happen before you type anything:

- **makeit stays current.** The first session of the day starts `makeit update --auto` in the
  background. It moves makeit, Claude Code, the plugins and their tools to their newest releases
  and writes to `~/.makeit/update.log`. If something moved, the next session tells you in one
  line, for example `makeit updated: superpowers 7.0.1 → 7.0.2 · undo: makeit rollback`.
- **Claude picks up where the last session stopped.** It reads the project's `HANDOFF.md`: what
  is done, what is next, what is open. You do not need to re-explain yesterday's work.
- **Claude knows your dependencies.** It gets the list of the project's direct dependencies and
  a reminder to search their docs. The docs index is rebuilt in the background whenever a
  manifest or lockfile changes, so the docs match the versions you actually use.

## Giving Claude work

Ask for what you want in plain words. The plugin set shapes how Claude works on it:

- **New features start with questions.** For anything new, Claude first asks what you want and
  why, lays out two or three ways to build it, and writes a short design for you to approve.
  Then it writes a plan, and builds it test-first. For a quick, clear request it skips straight
  to the work.
- **Bugs start with the cause.** Claude reproduces the bug and finds the root cause before
  changing code, then adds a test that fails without the fix.
- **Changes stay small.** Claude makes the smallest change that fully solves the task, and says
  in one line what it left out.
- **Nothing is "done" without proof.** Before Claude says a task is done, it runs the project's
  `check:task` (format, lint, types and the tests related to the change) and shows the output.
  For a web app it opens the page in Chrome and looks at it; for an API or a CLI it calls it.

Claude runs commands without asking, except the risky ones on makeit's ask list, such as merging a
pull request, pushing to `main` or `master`, force pushes, `git reset --hard`, publishing a package or
a release, and deleting through the GitHub API. Those always stop for your yes. The full list is
in [What makeit installs](included.md#settings).

## Answering Claude

Claude asks in two ways, and both take one keystroke:

- **A decision the work waits on** (a choice, a go-ahead) comes as Claude Code's question dialog.
  Pick an option or type your own answer.
- **Suggestions after finished work** come as a short numbered list at the end of the reply,
  with one marked as recommended:

  ```
  1. **Add a test for refunds** · recommended
     catches the next rounding bug before it ships
  2. **Clean up the old price helper**
     removes dead code; no change for users
  ```

  Reply with the digit. Claude does that option without asking again.

When a decision needs reading first (a plan, a release step), Claude puts the full text in the
reply, then a short summary with its picks as lettered lines, then the numbered choices. Reply
with a digit to accept, or `b: keep the old name` to change a pick.

If Claude ends a turn on a question in plain prose instead, makeit's reply check catches it and
has Claude ask properly.

## Looking things up

- **Library docs:** ask about a dependency ("how does zod handle optional fields in v4?"). Claude
  searches the docs of the version your project uses, not what it remembers.
- **Past sessions:** ask "what did we decide about the cache?" or "didn't we fix this before?".
  Claude searches your earlier Claude Code sessions on this machine.
- **The project's rules:** `CLAUDE.md` holds the project facts and commands, `CONSTRAINTS.md` the
  quality rules (zero lint warnings, coverage on changed lines, and so on). Claude reads both;
  you can edit both.

## Reviewing work

- `/code-review` reviews the current changes for bugs.
- `/ponytail-review` reviews a change for logic, safety and code nobody needed.
- `/security-review` checks the pending changes for security problems; ask for a "security
  audit" when you want the whole codebase looked at.
- Run `check:full` yourself before a push (`make check:full`, `npm run check:full`, or whatever
  runner the project uses). It runs everything CI runs.

What to say to get the most out of each part, by task: [Getting the most out of it](using.md).

## Ending a session

Just stop. When files changed, Claude rewrites `HANDOFF.md` before the turn ends, and again
before Claude Code compacts a long conversation. Tomorrow's session starts from it.

To be told when Claude finishes or needs you while you are away, set `NTFY_TOPIC` to an
[ntfy](https://ntfy.sh) topic in the `env` block of `~/.claude/settings.json`. It sends only the
repository name and the event.

## A new project, or an older one

In Claude Code, inside the project, type `/makeit:init`. Claude writes `CLAUDE.md`,
`CONSTRAINTS.md` and `HANDOFF.md`, wires the check tasks to your real linter and tests, writes a
recipe for starting the app, runs everything, and asks before committing. Details:
[Projects](projects.md).

Run `/makeit:init` again after makeit updates. When a newer makeit sets projects up differently
(a new rule, a better template row), the rerun brings this project up to date, keeps everything
you filled in, and asks about anything else that looks out of date. What you choose to keep is
remembered and never asked again.

## When something goes wrong

| Situation | What to do |
|---|---|
| Something feels broken | `makeit doctor` checks the machine, lists each problem with its fix, and offers to run the fixes it can. |
| The last update broke something | `makeit rollback` puts the previous files, plugin versions and makeit back. |
| You want to see what the daily run did | Read `~/.makeit/update.log`. |
| You want updates now | `makeit update`. |
| You want no daily updates | Add `"autoUpdate": false` to `~/.makeit/config.json`. |
| You changed a setting makeit also sets | Nothing: your value is kept, and every run lists it as yours. |
| You want makeit gone | `makeit uninstall` removes what makeit added and you did not change. |

`makeit` on its own prints the machine's status in four lines: version, health, last update and
whether the daily run is on.
