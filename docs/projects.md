# Projects

makeit sets up each project the same way: the files Claude reads at session start, check tasks,
commit hooks, settings, and an index of the docs of the project's dependencies.

There are two ways to run it:

- `/makeit:init` in Claude Code: makeit writes the files, then Claude fills in the parts that
  depend on your project.
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
   linter, type checker and tests.
4. Apply the lines makeit reported for files it does not edit itself (YAML, TOML, Makefile,
   justfile, Taskfile).
5. With a web frontend, add Playwright with axe and a `test:e2e` task. With an OpenAPI spec, add
   schemathesis to `check:api`.
6. Write a run recipe, `.claude/skills/run-<project>/SKILL.md`, so Claude can start and use the
   app. A project with nothing to start yet gets a `HANDOFF.md` item instead.
7. Run `check:full` and fix what fails.
8. Ask before the first commit. On a yes, a repository with no commits gets its first commit on
   the default branch; otherwise Claude commits on a new branch. On a no, nothing is committed.

Claude never runs `makeit init --rulesets` unless you ask for it.

Only you can start `/makeit:init`: Claude does not invoke it by itself.

## makeit init

```console
$ makeit init [dir]
```

`dir` defaults to the current directory. init prints its plan and asks once. `--dry-run` prints
the plan and writes nothing; `--yes` applies it without asking.

- An empty or missing directory gets `git init` first.
- A directory with files but no git is refused: every change init makes should be reviewable with
  `git diff`. Run `git init` first.

At the end, init prints what it wrote, what it kept and why, the lines to add by hand to files it
does not edit, and notices such as "enable the git hooks once per clone: prek install".

## What init writes

init looks at the project's files to decide what applies: the language (`go.mod`, `package.json`,
`pyproject.toml`, `Cargo.toml`), a web frontend, an OpenAPI spec, a database, calls to a model
API, and a mobile app.

In every project:

| file | what it is |
|---|---|
| `CLAUDE.md` | project instructions for Claude; init fills in the project name, the task runner, the package manager and the date, and leaves the other `<...>` placeholders for Claude or you |
| `CONSTRAINTS.md` | the quality rules: what each change must pass, with the command behind each rule |
| `HANDOFF.md` | what is done, what is next and what is open, read at the start of each session |
| check tasks | `check:fast`, `check:task` and `check:full` in `package.json` scripts for a Node project; each one fails on purpose until it is wired to real tools |
| `.pre-commit-config.yaml` | commit hooks for [prek](https://github.com/j178/prek): the language's formatter and linter, plus a secret scan (betterleaks) |
| `.gitignore` | adds `.env`, `.env.*` and `!.env.example` |
| `.env.example` | a template for `.env`, which stays uncommitted |
| `renovate.json` | Renovate rules that keep the pinned versions current |
| `.grype.yaml` | the vulnerability scan's configuration |
| `.makeit.json` | init's record of what it wrote; commit it with the rest |

For a Makefile or a `pyproject.toml` with poe tasks, init does not edit the file. It prints the
exact lines for the check tasks instead. For another task runner it says which tasks to add.

When it finds more:

| found | init adds |
|---|---|
| a web frontend | `makeit-ui` enabled in `.claude/settings.json`; a browser check in `CLAUDE.md`'s "How to verify a change" |
| an OpenAPI spec | a `check:api` task: Redocly lint, and oasdiff against the default branch for breaking changes (oasdiff runs in Docker) |
| a database | the DBHub MCP server in `.mcp.json`, a `.dbhub.toml` with no secret in it, `DBHUB_DSN=` in `.env.example`, and a `check:drift` task |
| calls to a model API | `TYPED-DECISIONS.md` |
| a mobile app | a Mobile section in `CLAUDE.md` |

DBHub reads its connection string from `DBHUB_DSN`, set in the shell that starts `claude` or in
the project's `.env`. Start `claude` from the project root. Claude gets two read-only tools and one
write tool, which asks first through the ask rule in makeit's settings.

### CI checks

init adds a GitHub Actions workflow that runs makeit's CI checks, in
`.github/workflows/gate.yml` (or `makeit-gate.yml` when you already have a `gate.yml`). It calls the
shared workflow in makeit's repository at a pinned commit, and Renovate keeps that pin current. It
also adds `ci/floor-guard.mjs`, which fails a pull request that weakens a quality rule (a skipped
test, a silenced checker) or changes a watched file (a checker config, a check task, a workflow, the
hook config, `pyproject.toml` or a `package.json` script line, `CONSTRAINTS.md`) unless its
description gives a reason in a line `floor-guard: <reason>`.

### Docs index

init indexes the docs of each direct dependency at the version the project uses, for the `docs`
MCP server. A library makeit cannot resolve, or one that takes too long, is listed and skipped, not
failed. The makeit plugin's hook re-runs the index in the background when a manifest or lockfile
changes. To run it yourself:

```console
$ makeit docs sync [dir]
```

### GitHub rulesets

```console
$ makeit init --rulesets
```

This sends makeit's two rulesets (`default-branch` and `no-force-push`) to the GitHub repository.
It needs `gh`, logged in with admin rights, and a GitHub remote. Each ruleset is its own step in
the plan, and a ruleset that already matches is left alone. `default-branch` is refused until each check it requires has passed once on the
default branch, and the refusal names the missing ones.

## Running init again

A second run with nothing new reports every step as already done. These rules decide what a re-run touches:

- A file that was there before init's first run is yours, and init never rewrites it. For your
  own `CLAUDE.md` and `CONSTRAINTS.md`, `/makeit:init` has Claude add only the sections they lack,
  and only missing items to your `HANDOFF.md`. The merged files below are the exception.
- A file init wrote or merged into is left as it is on a plain re-run. When makeit has a newer version of it,
  init says so and names `makeit init --update`.
- `makeit init --update` rewrites a file init wrote only while it is exactly as init wrote it. A
  file you changed is kept and listed.
- A file you deleted is not written again.
- Files init merges into (`package.json`, `.gitignore`, `renovate.json`, `.claude/settings.json`,
  `.mcp.json`) only gain entries. init never removes an entry or changes a value you have. When
  your value differs from makeit's, yours is kept and listed. An entry you removed after init added
  it is not added again.
- A symlink is never followed.

After `makeit update`, run `makeit init --update` in each project to take the new files.
