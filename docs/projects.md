# Projects

makeit sets each project up the same way: the files Claude reads at session start, check tasks
wired to the project's real tools, commit hooks, CI, and the routines Claude follows in
the project. Afterwards a session in that project starts from what the last one left, runs the
checks before calling work done, and reads the docs of the versions you actually use.

There are two ways to run it:

- `/makeit:init` in Claude Code: makeit writes the files, then Claude fills in the parts that
  depend on your project. This is the one to use.
- `makeit init` in a terminal: makeit writes the files and stops. No Claude involved.

Run `makeit setup` on the machine first. Both need git.

## /makeit:init

In Claude Code, from the project root, type:

```
/makeit:init
```

Typing it is your consent: Claude runs `makeit init --yes --json`, then works through these steps:

1. Fill in the project facts in `CLAUDE.md` (a one-sentence description, the layout, known traps).
   It asks you once for the product priority and writes your answer as given.
2. Fill in `CONSTRAINTS.md` with the command behind each rule, and delete only the rows the
   project does not have (for example accessibility without a UI).
3. Wire the `check:fast`, `check:task` and `check:full` tasks to the project's real formatter,
   linter, type checker and tests. In a Makefile, one target per tool, so `make -k` reports every
   failure.
4. Apply the lines makeit reported for files it does not edit itself (YAML, TOML, Makefile,
   justfile, Taskfile).
5. With a web frontend, add Playwright with axe and a `test:e2e` task. With an OpenAPI spec, add
   schemathesis to `check:api`.
6. Write a run recipe, `.claude/skills/run-<project>/SKILL.md`, so Claude can start and use the
   app. A project with nothing to start yet gets a `HANDOFF.md` item instead. From the same
   recipe it writes `.claude/skills/verify/SKILL.md`, which Claude Code runs before each commit:
   start what changed and use it like a person (browser at desktop and phone width, emulator,
   or a call to the CLI or API). A `verify` skill you already have is kept.
   It also writes a "Start of a task" and "End of a task" section into `CLAUDE.md` with the
   project's real steps (ticket, branch, checks, deploy, handoff), asking for what it cannot
   find, and asks which branches besides `main` and `master` deploy, writing a `git push` ask
   rule for each into `.claude/settings.json`.
7. Run `check:full` and fix what fails.
8. Ask before the first commit. On a yes, a repository with no commits gets its first commit on
   the default branch; otherwise Claude commits on a new branch. On a no, nothing is committed.

Claude never runs `makeit init --rulesets` unless you ask for it, and only you can start
`/makeit:init`: Claude does not invoke it by itself.

## makeit init

```console
$ makeit init [dir]
```

`dir` defaults to the current directory. init prints its plan and asks once. `--dry-run` prints
the plan and writes nothing; `--yes` applies it without asking. Where you run it decides what you
get:

- A git repository: everything below.
- An empty or missing directory: `git init` first, then everything below.
- A directory with files but no git: set up without git. No repository is created, and the parts
  that only work inside one are left out (the commit-hook config, `--rulesets`), with a line
  saying so. After `git init`, `makeit init --update` adds them.
- A directory with no git that holds repositories as direct children (a workspace you open Claude
  in): set up for the folder alone, nothing inside the repositories. The files that only act
  inside a repository (`.gitignore`, `renovate.json`, `.grype.yaml`, `.env.example`, the commit
  hooks, the CI workflow) are left out. Dependencies come from each repository; worktrees (a
  child whose `.git` is a file) are skipped.

At the end, init prints what it wrote, what it kept and why, the lines to add by hand to files it
does not edit, and notices such as "enable the git hooks once per clone: prek install".

## The files, and what you do with them

init looks at the project to decide what applies: the language (`go.mod`, `package.json`,
`pyproject.toml`, `Cargo.toml`), a web frontend, an OpenAPI spec, a database, calls to a model
API, a mobile app. Every project gets these:

| file | who writes it | what it does, and your part |
|---|---|---|
| `CLAUDE.md` | init the skeleton, Claude the facts | What Claude reads first in every session: what the project is, the commands, the layout, the traps. Edit it when a fact changes; a stale line costs you a wrong assumption per session. |
| `CONSTRAINTS.md` | init the rules, Claude the commands | What every change must pass: zero type errors and lint warnings, no secrets, 80% coverage on changed lines, a mutation score on the diff, no high security findings, clean CI config, and accessibility, performance and migration checks where they apply, each with the command that decides. Claude reads it before writing code and never weakens it to make a change pass. Raise a bar by editing a row; a PR that lowers one needs a reason ([CI checks](#ci-checks)). The "Measured, not yet enforced" table holds today's numbers for the rules you cannot enforce yet. |
| `HANDOFF.md` | Claude, at the end of each session | What is done, what is next, what is open, under 30 lines. Claude reads it when a session starts and rewrites it when files changed. Read it to catch up; edit it to redirect the next session. |
| `check:fast`, `check:task`, `check:full` | init the stubs (they fail on purpose), Claude the wiring | The three check levels: format, lint and types; plus the tests related to the change; plus everything CI runs. Claude runs `check:task` before saying a task is done and `check:full` before a push. Run them yourself the same way. In a Node project they are `package.json` scripts; for a Makefile or poe tasks init prints the lines and Claude adds them. |
| `.pre-commit-config.yaml` | init | Commit hooks for [prek](https://github.com/j178/prek): the language's formatter and linter, plus a secret scan. Run `prek install` once per clone; init's notice says so. |
| `.env.example`, `.gitignore` | init | The template for `.env`, which stays out of git: list every variable there, with no value. `.gitignore` gains `.env` and `.env.*`, and keeps `.env.example`. |
| `renovate.json`, `.grype.yaml` | init | Renovate keeps the pinned tool versions current; the vulnerability scan's configuration. Nothing to do day to day. |
| `.github/workflows/gate.yml`, `ci/floor-guard.mjs` | init | The CI checks, below. |
| `.makeit.json` | init | init's record of what it wrote, which later runs read. Commit it; do not edit it. |

When init finds more, it adds:

| found | added |
|---|---|
| a web frontend | `makeit-ui` (the impeccable design skills) enabled in `.claude/settings.json`; a browser check in `CLAUDE.md`'s "How to verify a change" |
| an OpenAPI spec | a `check:api` task: Redocly lint, and oasdiff against the default branch for breaking changes (oasdiff runs in Docker) |
| a database | the [DBHub](https://github.com/bytebase/dbhub) MCP server in `.mcp.json`, so Claude can query it: a `.dbhub.toml` with no secret in it, `DBHUB_DSN=` in `.env.example`, and a `check:drift` task. Set `DBHUB_DSN` in the shell that starts `claude` or in `.env`, and start `claude` from the project root. Reads are free; the one write tool asks first |
| calls to a model API | `TYPED-DECISIONS.md`: the rule that every place the app asks a model for a decision gets a typed schema the library validates and retries, with the library to use per stack |
| a mobile app | a Mobile section in `CLAUDE.md` |

### CI checks

init adds a GitHub Actions workflow, `.github/workflows/gate.yml` (or `makeit-gate.yml` when you
already have a `gate.yml`), that runs makeit's CI checks on every pull request: `check:full`, the
secret scan, the code and dependency security scans, the workflow audit, the pinned-action check,
and, where the project has them, the browser, performance budget, migration and API contract
checks. It calls the shared workflow in
makeit's repository at a pinned commit, and Renovate keeps that pin current.

It also adds `ci/floor-guard.mjs`, which fails a pull request that weakens a quality rule (a
skipped test, a silenced checker) or changes a watched file (a checker config, a check task, a
workflow, the hook config, `CONSTRAINTS.md`) unless the PR description gives the reason in a line
`floor-guard: <reason>`. When you do mean to change one of those, write that line; that is the
whole ceremony.

### GitHub rulesets

```console
$ makeit init --rulesets
```

This sends makeit's two rulesets (`default-branch` and `no-force-push`) to the GitHub repository.
It needs `gh`, logged in with admin rights, and a GitHub remote. Each ruleset is its own step in
the plan, and a ruleset that already matches is left alone. `default-branch` is refused until each
check it requires has passed once on the default branch, and the refusal names the missing ones.

## Running init again

After `makeit update`, type `/makeit:init` again in each project. When the new makeit sets
projects up differently (a template row, a rule for the check tasks), Claude applies each listed
change to your files, keeping the values you filled in, then lists anything else that looks out of
date and asks, one line each, whether to apply it or keep yours. A difference you keep is recorded
in `.makeit.json` and never raised again. A rerun with nothing new reports every step as already
done.

What a rerun may touch:

- A file that was there before init's first run is yours, and `makeit init` never rewrites it.
  For your own `CLAUDE.md`, `/makeit:init` has Claude read it (and `AGENTS.md`, `.cursorrules` and
  `CONTRIBUTING.md` when present) and draft a new one in makeit's structure with what is still
  relevant; it shows you the new file and every line it cut, with the reason, and writes it only
  on your yes, keeping the old one as `CLAUDE.md.before-makeit`. For your own `CONSTRAINTS.md` it
  adds only the sections it lacks, and only missing items to your `HANDOFF.md`.
- A file init wrote is updated by `makeit init --update` only while it is exactly as init wrote
  it. A file you changed is kept and listed; a file you deleted is not written again. A plain
  rerun leaves it alone and says when a newer version exists.
- Files init merges into (`package.json`, `.gitignore`, `renovate.json`, `.claude/settings.json`,
  `.mcp.json`) only gain entries. init never removes an entry or changes a value you have, and an
  entry you removed is not added again.
- A symlink is never followed.
