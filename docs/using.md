# Getting the most out of it

makeit installs a working method, a brevity skill, a browser, docs on demand, a memory and a set of
review tools. Most of them act on their own; a few are better when you call them. This page goes
by what you want to do, and says what to say and what happens.

## Start a feature

Say what you want in plain words: "Let's build an export of invoices to CSV." Do not start with
code or with a plan of your own.

1. **Claude asks before building** ([superpowers](https://github.com/obra/superpowers)' brainstorming). It asks what the feature is for
   and who uses it, one question at a time, and plays back what it understood. Answer in your own
   words; it is finding out what you want, not offering a menu.
2. **It lays out two or three ways to build it** and recommends one. Pick.
3. **It writes a design** for you to approve: a short one for a small change, a document in
   `docs/superpowers/specs/` for a project. Read it; this is the cheapest moment to change your
   mind.
4. **It writes a plan** of small tasks with their tests, then asks how to run it: a fresh subagent
   per task with a reviewer after each (thorough, costs more), or inline in this session with one
   review at the end (cheaper). Say "go".
5. **It builds test-first** in a worktree on its own branch, task by task, and ends with a review
   of the whole branch and the choice: merge, open a pull request, keep the branch, or discard it.

Tips. Approve the design, not the idea: "sounds good" to a sketch is not approval of a document
you have not read. Say "just dive in" when the request is small and clear and you do not want
the questions. When the questions come anyway for a one-line change, that is worth a
[diagnosis](#when-something-feels-off).

## Fix a bug

Describe what you saw: "The total is off by one cent when a refund has shipping." Claude reproduces
it and finds the cause before touching code (superpowers' systematic debugging), then writes a
test that fails without the fix and passes with it. If it proposes a fix before it has shown you
the cause, say so: "find the cause first".

## Keep changes small

[ponytail](https://github.com/DietrichGebert/ponytail) is on in every session at level `full`: the smallest change that fully solves the task,
nothing nobody asked for, and a last line naming what it skipped and any risk. Change the level
for a task:

- `/ponytail lite`: build what was asked, name the smaller option in one line.
- `/ponytail ultra`: also question the request and push back before building. Good for a vague
  "build me X".
- "stop ponytail" or `/ponytail off` turns it off for the session; `/ponytail` brings it back.

`/ponytail-gain` shows what it has saved. The default level is `PONYTAIL_DEFAULT_MODE` in the
`env` block of `~/.claude/settings.json`, where makeit set `full`.

## Review before you merge

Four reviews, each for a different question:

| You want to know | Type |
|---|---|
| Is this change correct? (bugs, high confidence first) | `/code-review` (add `high` or `max` for a deeper pass) |
| Is every line needed, is it safe under load, is risky code tested? | `/ponytail-review` |
| Does the pending change open a security hole? | `/security-review` |
| Is the whole codebase sound? (ranked: bugs, security, load, debt) | `/ponytail-audit`, or "run a security audit of this codebase" for the [security-audit](https://github.com/cloudflare/security-audit-skill) skill |

`/code-review`, `/security-review` and `/simplify` are [Claude Code](https://code.claude.com/docs/en/overview)'s own commands.
After a plan, superpowers runs its own whole-branch review before offering the merge. `/simplify`
cleans the changed code without hunting bugs. `/ponytail-debt` lists the shortcuts Claude marked
with `shortcut:` comments, so a deferral does not become permanent.

A plugin's commands can also be typed with their plugin name, for example
`/ponytail:ponytail-review`, when another plugin has a command of the same name.

## Check web work in a real browser

Ask, in words: "open the app and check the console", "take a screenshot of the checkout at phone
width", "run a Lighthouse audit on the landing page", "why is the largest contentful paint slow",
"check this page for accessibility problems", "is there a memory leak when I open and close the
modal twenty times". Claude drives an installed Chrome through [chrome-devtools-mcp](https://github.com/ChromeDevTools/chrome-devtools-mcp): it reads the
console and the network, emulates devices, records traces. The global rule already asks Claude
to open a web change in the browser before calling it done; the run recipe `/makeit:init` wrote
(`.claude/skills/run-<project>/SKILL.md`) tells it how to start the app.

## Design, in web projects

In a project with a web frontend, `/makeit:init` turned on `makeit-ui`, which brings [impeccable](https://github.com/pbakaus/impeccable).
Its commands take a target: `/impeccable critique the pricing cards` gives a scored review of
what to fix first, `/impeccable audit` a technical report (accessibility, performance, theming,
responsive), `/impeccable polish` the pass before shipping, `/impeccable bolder` or `quieter`
steers a design that feels timid or busy, `/impeccable live` lets you pick an element in the
running app and cycle through variants. Run `/impeccable init` once per project: it writes
`PRODUCT.md`, which the other commands read. `/impeccable` alone lists all of them.

## Look things up

- **A library:** ask the question. "How does zod 4 handle optional fields?" Claude fetches the
  docs of the version your project uses when it is about to use a library newer than its training;
  a hook stops the first edit that does so and names the library, the version and where its docs
  live. Nothing to type; if it answers from memory, say "read the docs of the version we use".
- **Your own past:** "what did we decide about the cache key?", "didn't we fix this before?",
  "which tools did we already try for this?". [deja-vu](https://github.com/vshulcz/deja-vu) searches your earlier Claude Code sessions
  on this machine; every session starts with a short summary, and a one-line hint on each prompt
  of four words or more asks Claude to search before re-debugging an error or recommending a tool. At the end of a day, `/deja-vu:deja-reflect` reviews what was decided and
  records the durable rules, so the next session starts with them.

## Answer in one keystroke

Claude asks two ways. A decision the work waits on comes as Claude Code's question dialog: pick
an option or type your own. Suggestions after finished work, and anything that needs reading
first (a plan, a release step), end the reply with numbered choices, one marked as recommended:
reply with the digit. When the reply lists its picks as lettered lines, `b: keep the old name`
changes one pick; Claude shows the changed lines and asks again.

If a reply ends on a question in prose instead, makeit's reply check has Claude ask again
properly. You should not have to type a phrase Claude handed you.

## Write for other people

Ask Claude to draft a message, an e-mail or a comment and it uses the `draft` skill: plain text
you can paste, complete, in the language of the thread you answer, with no file paths, code
references or Markdown.

## Hand off between sessions

`HANDOFF.md` is how one session talks to the next. Claude writes it when a session changed files
and before a long conversation is compacted, under 30 lines: done, next, open, how to verify. On a
branch other than the default (and in a worktree) the file is `HANDOFF.<branch>.md`, so parallel
sessions do not overwrite each other; run the second session with `claude -w <name>`.
Read it when you come back; edit it to redirect the next session ("Next: ship the CSV export,
skip the PDF one"). When Claude stops a turn saying `HANDOFF.md` must be rewritten, let it.

## Keep the project honest

`CONSTRAINTS.md` is the floor every change must pass, and Claude reads it before writing code.
Raise a bar by editing a row; Claude never lowers one to make a change pass. A pull request that
weakens a rule or touches a watched file (a checker config, a check task, a workflow) needs one
line in its description, `floor-guard: <reason>`, or the CI check fails it. Run `check:task`
before you call something done and `check:full` before a push, the same as Claude does.

## Change a database schema

Ask for the change: "rename `user.name` to `user.full_name`". For a rename, drop, retype or
split that live code reads, Claude follows the [expand-contract](../plugins/makeit/skills/expand-contract/SKILL.md) skill: additive changes first, the
destructive step last and alone, backfills in batches. In a project with a database, [DBHub](https://github.com/bytebase/dbhub) lets
Claude read the schema and run queries; the one write tool always asks first.

## When something feels off

- A skill fired when it should not have, or stayed silent, or Claude ignored its plan or repeated
  work: say "figure out what went wrong with superpowers in this session". It reads the transcript
  and reports what happened, with evidence; for an earlier session, name its id.
- Claude seems to have lost the project's context: check `HANDOFF.md` and `CLAUDE.md`; a stale
  line there costs a wrong assumption per session.
- Something about the machine: `makeit doctor`. The last update broke something: `makeit
  rollback`.
- `/ponytail-help` is ponytail's reference card.

## Be told when Claude needs you

Claude finishing a turn, asking you something, or waiting on a permission prompt shows a desktop
notification at once (notify-send on Linux, osascript on macOS, a toast through powershell.exe on
Windows), with the repository name and the event, nothing else. When a background task is still
running the line says so. To get the same line on your phone, run `makeit notify` (setup asks the
same two questions at its end). It sets an [ntfy](https://ntfy.sh) topic in the `env` block of
`~/.claude/settings.json`, a private random one that you subscribe to in the ntfy app, and
optionally turns on Claude Code's own phone push (`remoteControlAtStartup`,
`inputNeededNotifEnabled`, `agentPushNotifEnabled`, written to `~/.claude/settings.json` too;
it needs a claude.ai login). With both on, the phone may show two notices for one event. Use your
own server with `NTFY_URL` and `NTFY_TOKEN` in the same `env` block.
