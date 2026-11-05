---
description: Set up this project the makeit way. Runs makeit init, then does the parts that need judgment, runs check:full, and asks before the first commit.
disable-model-invocation: true
allowed-tools: Bash(makeit init --yes --json)
---

# /makeit:init

The user typed /makeit:init: that is their consent for `makeit init --yes`. makeit does the
deterministic part (files, merges, the docs index, the record in `.makeit.json`); you do the parts
that need judgment, in this order, and stop at the end of each step only when it says so.

## The order

1. Run makeit init (step 1 below). If it fails, stop there.
2. Fill the project facts and the sections the docs lack.
3. Wire the check tasks to the real tools.
4. Apply the lines makeit reported for files it does not edit.
5. Web frontend: Playwright + axe. API: schemathesis. Only when makeit detected them.
6. Write the run recipe, or a HANDOFF item when nothing can start yet.
7. Run check:full and fix what it finds.
8. Ask before the first commit, with AskUserQuestion, and commit only on a yes.

Never run `makeit init --rulesets` unless the user asks for it in so many words: it sends
rulesets to GitHub, which is outward-facing and needs its own consent.

## 1. Run makeit init

Run it with the Bash tool from the project root, as a command on its own (the skill pre-approves
exactly this command, so do not chain it with others), with `timeout: 600000` (the docs index can
take minutes on a repository with many dependencies):

    makeit init --yes --json

- Exit 0: read the JSON on stdout: `plan` (what each step did), `kept` (what makeit left as the
  user had it, and why), `manual` (exact text for files makeit does not edit), `notices` (what the
  user should know: prerequisites such as Docker for check:api, `prek install` per clone) and
  `docs` (the libraries indexed, and those that could not be). A `notices` line "no git
  repository here" means a folder with files and no git, set up without git on purpose: leave
  the commit hooks out, skip step 8 (no commit, no `git init`) and say so in one line.
- `makeit: command not found`: makeit is not installed. Stop and say so in one line; do not set
  the project up another way.
- Exit 1: a step failed, or init refused before changing anything; the JSON's `error` says why.
  Show it in one line and stop. Exit 2 should not happen with `--yes`; if it does, show the
  error and stop.

Search the docs index (search_docs, or the docs-search skill) before you rely on a library's API
in the steps below; makeit indexed the project's dependencies at their versions.

## 2. Project facts and missing sections

- CLAUDE.md: replace each remaining `<...>` placeholder with what the code shows (the one-sentence
  description, the layout lines, the known traps you find). The **Product priority** line must be
  in the user's words: ask once, with AskUserQuestion, and write their answer as given.
- A CLAUDE.md that `kept` lists as "yours: kept as it is" is rebuilt, with the user's yes. Not a
  CLAUDE.md that is a symlink (`kept` says so; leave it and the file it points to alone), and not
  when `CLAUDE.md.before-makeit` already exists (it was rebuilt before: add only the template's
  missing sections). Ask the **Product priority** question first, so the draft carries the answer:
  1. Read it, and `AGENTS.md`, `.cursorrules` and `CONTRIBUTING.md` when they exist, and the code
     they describe.
  2. Draft a new CLAUDE.md in the structure of `templates/project/CLAUDE.md` (in the makeit plugin
     set, `~/.makeit/catalog`). Carry over what is still relevant and true of the code; cut what is
     outdated (a path that no longer exists, a command that no longer runs), redundant with the
     template or with another line, or not about this project. Write nothing yet.
  3. Ask in the reply, in the global CLAUDE.md's reading shape: the new file in full, then every
     cut line quoted with its reason, then a TL;DR, a plain line, and the choices: write it,
     change something first, or keep mine and add only the missing sections.
  4. On a yes, rename the old file to `CLAUDE.md.before-makeit` (never over an existing one) and
     write the new one; the backup stays out of the first commit (step 8), and the user deletes it
     when satisfied. On "keep mine", add only the template's missing sections, as for
     CONSTRAINTS.md. The other files are read, never changed.
  5. After the answer, go on with step 3.
- A CONSTRAINTS.md that `kept` lists as the user's: add the sections of
  `templates/project/CONSTRAINTS.md` that it lacks, and keep every section it has. Never rewrite
  it.
- CONSTRAINTS.md: fill each `<...>` with the command that produces the verdict. Delete only the
  rows whose dimension the project does not have (Accessibility and Performance without a UI,
  Migrations without a database). Never delete the Coverage or Mutation row: every project has
  both. Wire the language's coverage tool and a diff mutation tool; when one cannot be wired yet,
  keep its row as it is and add a HANDOFF item for it. floor-guard reads a removed row as a
  weakened constraint.
- HANDOFF.md: small numbered items, one action each.

## 3. Wire the check tasks

makeit added `check:fast`, `check:task` and `check:full` stubs that fail on purpose (or reported
them in `manual` for a Makefile, justfile, Taskfile or pyproject). Wire each to the repository's
real tools, named in the global rules for its language (`~/.claude/rules/`): formatter, linter and
type checker for check:fast; plus the related tests for check:task; everything CI runs for
check:full. Use the package manager and runner the repository already has.

## 4. Apply the manual lines

Each `manual` entry is exact text for a file makeit does not edit (YAML, TOML, Makefile, justfile,
Taskfile, a CI caller input). Apply it with the Edit tool, keeping the file's comments and
order. When an entry says a value must wait for something (has-web-ui until test:e2e exists),
apply it once that something exists.

## 5. Web and API

- Web frontend (`web` in the record's types): add Playwright with @axe-core/playwright and a
  `test:e2e` task that runs it; then set `has-web-ui: true` in the CI caller.
- OpenAPI spec (`api`): add schemathesis against the running server to `check:api`, since it
  needs a server makeit cannot start.

## 6. The run recipe

Write `.claude/skills/run-<project>/SKILL.md`, where `<project>` is the project directory name,
lowercased, with spaces as dashes. Use exactly this frontmatter, each value on one plain line:

    ---
    name: run-<project>
    description: Build, run, and drive <project>. Use when asked to start <project>, run its tests, build it, take a screenshot of its UI, or interact with the running app.
    ---

Then the sections Setup, Build, Run (agent path: start it in the background with logs to
`/tmp/<project>.log`, poll until it is ready, interact with it once for real, stop it), Test, and
Gotchas only for errors you hit. Every command in it must be one you ran in this session and saw
work. The name must start with `run-`: a skill named exactly `run` would replace Claude Code's
built-in run skill, which finds this recipe by its description.

If nothing can be started yet (a fresh project), write no recipe; add a HANDOFF item instead.

## 7. Run check:full

Run the repository's `check:full` and fix what fails, until it passes or a failure needs the user
(say which, and why, in one line).

## 8. The first commit

Skip this step in a folder makeit set up without git (its `notices` say "no git repository
here"): make no commit and run no `git init`; say in one line that the folder has no git.

Ask with AskUserQuestion before committing: name the branch and what the commit holds. On a yes:

- A repository with no commit yet (`git rev-parse --verify HEAD` fails, as after makeit's own git
  init): make this first commit on the default branch (`main`, or the branch origin/HEAD names),
  so the repository has a base for later pull requests.
- Otherwise create a branch from the current one and commit there; never on the default branch.

On a no, leave everything uncommitted.

End the commit message body with this line, so it is there for the PR description when the
branch is pushed: floor-guard, in the CI checks, fails a PR that changes workflows, check tasks, hook
and checker configs or floor-guard itself unless its description gives the reason.

    floor-guard: makeit init adds the CI checks, the check tasks and the hook config
