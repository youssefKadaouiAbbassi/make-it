# Changelog

All notable changes to makeit. Versions follow 0.x while makeit is in beta: a breaking change
raises the minor version.

## 0.4.3

- deja no longer writes past-session text into your repositories on its own. Since deja 0.24 its
  session-start summary and its MCP server added a `.deja/memory.jsonl` (up to 12 lines quoted
  from past sessions) to the git repository you work in, uncommitted and not ignored, so
  `git add -A` and a push would publish it. makeit now sets `DEJA_REPO=off` in Claude Code's
  settings; `makeit update` adds it unless you set your own value. Writing a note into the
  repository on purpose (`deja repo add`, or asking Claude to remember something for the repo)
  still works. If a `.deja/` folder already appeared in one of your repositories, look at it and
  delete it before your next commit.

## 0.4.2

- On Linux, setup installs the newest Node LTS from nodejs.org when node is missing or older than
  20.19 / 22.12 (the browser server, chrome-devtools-mcp, needs that; Ubuntu 24.04 and Debian 12
  ship node 18, where it could not start). The checksum from nodejs.org is verified, node goes to
  `~/.local/share/makeit/node/<version>` and `node`, `npm` and `npx` are linked into
  `~/.local/bin`; your system node stays. Alpine, Homebrew and winget keep their own routes.
  The daily update never installs tools, so on Ubuntu 24.04, Debian 12 and other machines with an
  old node, run `makeit setup` once to get the new node. If the nodejs.org download fails, an old
  node stays and a missing one comes from the package manager as before.
- `makeit update` moves that node to the next LTS and keeps the previous version until the update
  after; `makeit rollback` brings it back; `makeit uninstall` removes the folder and the links
  that are makeit's.
- `makeit doctor` warns when another node comes first on PATH than the one makeit installed.
- makeit no longer turns on the advisor (`advisorModel`): measured, it added no quality on tasks
  Opus solved alone and a consultation cost 4.4 times more; update removes the setting unless you
  changed it, and `/advisor` still turns it on.
- The global CLAUDE.md asks for: commands that may run past two minutes in the background, the
  quick check while iterating and the full suite once, Sonnet for research and mechanical
  subagents, the reviewer's criteria in a builder's brief, and parallel worktrees for plan tasks
  that touch different files.

## 0.4.1

- `makeit update` now updates the plugins from the makeit marketplace that you installed yourself
  and that load from makeit's folder, when their version changed. In 0.4.0 they kept their old
  version until `claude plugin update`: deja-vu, moved to makeit's folder in 0.4.0, stayed at
  0.24.0 on those machines.
- Update no longer prints a "loads in place" note for those plugins on every run.

## 0.4.0

A review of a week of real sessions: fewer lost messages, fewer surprises, less noise. Needs
Claude Code 2.1.319 or newer; setup and update bring it.

Parallel work:

- A second session on the same repository runs in its own worktree (`claude -w <name>`), and on
  any branch other than the default one the handoff is its own file, `HANDOFF.<branch>.md`, so
  sessions stop overwriting each other. Sessions can receive messages from one another.
- The browser server runs in its own Chrome profile, so two sessions no longer fight over it.

Asking first:

- Merging through `gh api` asks, whatever the form of the call.
- A `git push` that names no branch asks when it would push `main`, `master` or a deploy branch of
  the project. `/makeit:init` asks which branches deploy and writes a rule for each.

Notifications:

- A notification reaches the desktop at once when Claude asks you something, needs a permission or
  stops, and says when background tasks still run. It replaces the old ntfy-only script.
- `makeit notify` sets up your phone, and so do setup's closing questions and, once, the first
  interactive `makeit update` (`update --yes` and the daily run never ask): an ntfy topic, and
  optionally Claude Code's own push (skipped when you use an API key; it needs a claude.ai login). makeit writes only its own keys to
  `~/.claude/settings.json` and never opens `~/.claude.json`.
- Long waits (CI, a deploy) run in the background instead of a foreground sleep.

Less noise:

- deja-vu keeps its session summary, search, PreCompact and SessionEnd, and loses the hooks that
  added recall to every prompt and tool call; one line per prompt asks Claude to search when it
  may touch something decided before. The skill-check reminder skips one-word prompts, the search line prompts under four words, and
  the reply check leaves drafts for other people and `Sources:` sections alone.
- The hooks of every bundled plugin are listed in [docs/hooks.md](docs/hooks.md), checked in CI.

Docs on demand:

- The docs index and `makeit docs` are gone, and so are docs-mcp-server and its server. When Claude
  is about to use a library released after its training, the first such edit is stopped once with
  the library, the version and where the docs live. `makeit update` removes the old index: its own
  `docs` folder under the makeit folder, and docs-mcp-server's default store (`~/.local/share/docs-mcp-server`
  on Linux, `~/Library/Application Support/docs-mcp-server` on macOS, `%LOCALAPPDATA%\docs-mcp-server\Data`
  on Windows), but only when makeit's own docs record shows makeit ran the syncs that filled it. A store that is a
  link, or any store when `DOCS_MCP_STORE_PATH` is set, stays and is reported. The
  update reports what it removed and its size. `makeit update` also uninstalls the docs-mcp-server
  plugin when makeit installed it; one you installed yourself stays, with the command to remove it.

Finishing work properly:

- `/makeit:init` writes a `verify` skill from the project's run recipe (start what changed and use
  it like a person) and a "Start of a task / End of a task" section with the project's real steps.
  Rerun it on older projects to get both.
- New `draft` skill for text someone else will read: plain, complete, in their language.
- After a compaction Claude is pointed at the plan file and the open tasks; the todo tools are on.
- A question or a small urgent fix skips the design and plan steps; subagents run at medium effort.

Speed:

- chrome-devtools-mcp runs from a local copy that makeit keeps current: a session starts it in
  about 340 ms instead of 760 ms. The reply check reads the transcript only when needed. The docs
  check reads a project's lock file once per edit and spends at most 3 s on registry lookups, so
  edits in a project with many dependencies are no longer slow.

## 0.3.11

Docs:

- The README says what makeit does, at the machine, project and session level, and how to start.
- New guide, [Working with makeit every day](docs/daily.md): opening a session, how Claude works
  on a task, answering it, looking things up, reviews, and what to do when something goes wrong.
- The code comments lost their project history (dates, review labels, decision notes); one wrong
  doc comment on init in a folder without git is corrected. No behaviour changes.

## 0.3.10

Fixes:

- The reply check's fallback (used where `marked` is missing) no longer hides the rest of a reply
  after a code fence indented four spaces at the margin, and flags choices numbered "1." on every
  item when they sit under a plan numbered the same way. It now finds the first choice as the
  `marked` path does.
- The release workflow runs the test that fails when an old state migration is past its removal
  version; it used to skip on every build.

## 0.3.9

New:

- `/makeit:init` brings a project an older makeit set up up to the current way. Each change to
  how init works is listed per version in the plugin set, and a rerun applies the ones newer than
  the project's version to the files you filled in, keeping your values: the 0.3.7 migrations row
  and budgets line, the 0.3.8 Makefile rule. It then lists what else differs from the current
  templates and steps, and asks; a difference you keep is recorded in `.makeit.json` and not
  raised again. `makeit init --applied` records it.

## 0.3.8

Fixes:

- `/makeit:init` wires the check tasks in a Makefile with one target per tool, so
  `make -k check:full` runs every check and reports every failure; a recipe that chained the
  tools stopped at the first one that failed and hid the rest. Measured: with the old wording 0 of
  5 runs wrote such a Makefile, with the new one 5 of 5.

## 0.3.7

New:

- `makeit init` in a folder that is not a repository but holds repositories (a workspace you
  open Claude in) sets up the folder alone: CLAUDE.md, CONSTRAINTS.md, HANDOFF.md, the Claude
  settings and the check tasks, nothing inside the repositories. The files that only act inside
  a repository (`.gitignore`, `renovate.json`, `.grype.yaml`, `.env.example`, the commit hooks,
  the CI workflow) are left out, and `makeit init --update` removes the ones an older makeit wrote
  there if you never changed them. The docs index reads each repository's dependencies, never a
  worktree's.

Fixes:

- The docs index works again on a large index: docs-mcp-server cuts its list at 64 KiB when it
  writes to a pipe, so every library was skipped; makeit now reads the list through a file
  (reported upstream as arabold/docs-mcp-server#609).
- The project CONSTRAINTS.md names squawk for Postgres only, and says its budgets are starting
  points to replace with measured times.

## 0.3.6

New:

- The end-of-reply check asks `marked`, the Markdown library Claude Code renders with, how the
  reply's lists are numbered, instead of imitating its rules. setup installs `marked` with npm;
  without it, or when Node fails, the check falls back to its own reading.
- The npm tools makeit installs (`marked` and the TypeScript and Python language servers) now move
  to npm's newest release on every setup, update and daily run, and each move is listed. The two
  language servers used to stay at the version first installed. `makeit rollback` leaves these
  moves in place, so a run that only moved npm tools is not the one it undoes.

Fixes:

- The end-of-reply check now catches numbered choices that a terminal shows renumbered (11, 12)
  because Markdown joins them to a numbered list above them: across empty lines, indented lines,
  or a plain line written right under the list's last item. It follows the parser Claude Code
  renders with, so a code block, quote, heading or rule between the two lists passes. The rule in
  the global CLAUDE.md now says the plain line before the choices needs an empty line above it.
- A download that a server answers with an error on its side (500, 502, 503, 504) is tried twice
  more, after 2 and 4 seconds, so one bad answer from GitHub no longer fails setup and skips every
  step after it.
- `makeit rollback` keeps what it can undo: daily runs that changed nothing no longer push the
  last undoable run out of the five kept, it stops at an unreadable newer record instead of
  undoing an older run in its place, it never installs Claude Code, and its "undo" hint appears
  only when it has something to undo.
- A git plugin set pinned to an older tag no longer blocks every update when a makeit release adds
  a global file: the global files now come from the plugin set in use, so such a set also
  removes a global file makeit wrote that it does not have (only when you never changed it;
  `makeit rollback` brings it back).
- `makeit uninstall` no longer crashes after a self rollback across a change in the tool list.
- The npm tools only move forward, to a real version, read from the registry the global install
  uses (a project's own `.npmrc` no longer stops them).
- The daily run runs once when two Claude Code sessions start together, and a run that stops
  early counts as the day's run instead of retrying at every start.
- ntfy notifications post on macOS's bash 3.2 without a token, and the Claude Code repository
  install removes its temporary key files.

## 0.3.5

New:

- `/makeit:init` in a project that already has a CLAUDE.md reads it (and `AGENTS.md`,
  `.cursorrules`, `CONTRIBUTING.md`) and drafts a new one in makeit's structure with what is still
  relevant and true of the code. It shows the new file and every line it cut, with the reason, and
  writes it only on your yes, keeping the old one as `CLAUDE.md.before-makeit`.
- Approval replies end with the TL;DR and the open decisions just above the choices, and a plain
  line before the choices, so a terminal never renumbers them.

Fixes:

- A file in `~/.claude` that was there before makeit and still holds makeit's text, of this
  release or any earlier one, now gets makeit's updates (it was skipped as yours forever, even
  when it was makeit's own text); `makeit uninstall` still leaves it, and a file with any edit of
  yours stays yours.

## 0.3.4

New:

- The global CLAUDE.md asks Claude to put a plan or a release step you have to approve in the reply
  itself: a short TL;DR, the open decisions with its picks, every line of the plan (or the exact
  commands), then numbered choices you answer with a digit; the question dialog, which hides the
  text above it, stays for short choices.

Fixes:

- The `/makeit:init` skill no longer tells Claude that a folder without git is an error; since 0.3.3
  init sets such a folder up without git.

## 0.3.3

New:

- `makeit init` in a folder that has files but no git sets it up without git instead of refusing:
  every file except the commit-hook config, which only works in a repository; `--rulesets` says
  it needs one. After `git init`, `makeit init --update` adds the hook config.

## 0.3.2

Breaking:

- Nothing is held back any more. `makeit setup` takes the newest plugin and MCP releases too, not
  the ones makeit shipped with; prek, deja and grype come at their newest release and `update`
  replaces them when a newer one ships (checked against that release's checksum file; a binary
  you changed stays); security-audit follows the newest commit of its branch; `setup` and `update`
  bring Claude Code to the newest release of its channel. `--offline` keeps what is on disk.
- makeit's settings use model aliases: `"model": "opus"`, `"advisorModel": "fable"`, so Claude Code
  picks the newest Opus and Fable itself. A value you set stays yours.
- `--json` reports of `setup` and `update` carry `changes: [{kind, item, from, to}]`.

New:

- Every run lists what moved, old and new version side by side ("superpowers 7.0.1 → 7.0.2",
  "Claude Code 2.1.317 → 2.1.320", "grype 0.121.1 → 0.122.0"), in the live view, in plain mode
  and `update.log`, and in the daily run's note; a Claude Code that updated itself since the last
  run shows too.
- `makeit rollback` also puts a release tool and the MCP servers back to their earlier versions;
  the daily run skips an undone release until a newer one ships, a run by hand takes it.

Fixes:

- On a fresh machine the newest releases are looked up after setup has installed git, so the first
  setup already lands on them (found by the distro tests before release).
- A tool release that is tagged but not uploaded yet, a failing Claude Code upgrade, or an upgrade
  that needs a password where none can be typed (`--yes`, the daily run) is a note on that one
  item, never a stopped run; the plan shows the upgrade command.
- `makeit rollback` passes over a run that left nothing to undo instead of stopping there.

## 0.3.1

Fixes:

- makeit starts faster: the home screen shows in about 0.6 s instead of 2.7 s, `makeit doctor` in
  0.7 s instead of 2.5 s, and `makeit update` with nothing to do finishes in about 1.3 s instead of
  6 s (measured on one machine, 2026-11-03). The two `claude plugin ... list` calls now run once per
  process and at the same time (they ran two to four times, one after the other), the version probes
  (`git ls-remote` per plugin source, `npm view` per MCP package) run concurrently, and the home
  prints its header and version before it checks.
- `makeit update` says when a plugin's source could not be reached ("kept at v7.0.1: could not
  read its releases"); before, git's exit code was dropped and the pin stayed without a word.
- A release move of a plugin you installed yourself (adopted) is recorded like any other: the
  daily run's note names it and `makeit rollback` can put it back. Before, the daily run moved
  such a plugin and said nothing.

## 0.3.0

Breaking:

- `makeit setup` and `update` also install prek, deja-vu, grype, the two npm language servers and
  Chrome, no longer leaving them for you to install by hand; a tool you remove afterward stays
  removed, `makeit setup --reinstall <tool>` brings it back.
- `makeit setup` and `update` add one block to your shell profile that loads `~/.makeit/env`, which
  keeps the folders they install into on `PATH`. `makeit uninstall` removes it.
- In the `--json` reports of `setup` and `update`, `skipped` (the optional tools) is gone; `needsYou`
  lists the tools makeit cannot install on this machine, each with its manual route.

New:

- `makeit doctor` at a terminal asks "Fix N problems now?" for whatever `makeit setup` or
  `makeit update` can resolve, and runs it on yes.
- `makeit update`'s result names what moved: makeit's own version change, the plugins it moved by
  name (a count past two), and a count of the rest.

Fixes:

- `makeit rollback` undoes an update the daily run made: `update` now records when it swapped
  makeit in, which rollback reads; before, it only looked back 15 minutes from the run that wrote
  files, so an update from hours earlier was never undone.
- A clean live run of `setup` or `update` shows no note rows; how an adopted plugin gets its
  updates stays in `--verbose` and the log, as it already did for plain mode.
- "gopls is missing" when it is only off `PATH` now gives the line to put it there, instead of the
  same install command that already ran once.
- "Settings.json: a value you hold" reads as the setting's name and what happens to it, not a
  JSON pointer.
- Downloads read the CA bundle when they start, so a run that installs `ca-certificates` first can
  download in the same run; `SSL_CERT_FILE` and `SSL_CERT_DIR` are honoured.
- A release archive entry past 200 MB is refused instead of cut, and a zip entry that is a symlink
  is never taken as the program.
- `makeit setup --reinstall <tool>` with a name makeit does not know says so and stops.
- A run with nothing to change but tools left to you says "1 tool needs you" in its result, not
  "everything is current"; plain mode also says to open a new terminal after adding the block.
- The profile block also goes into `~/.bash_profile` when you have one, which bash reads instead
  of `~/.profile` at login; the fish line quotes the folder.
- On Windows, the npm tools land in `~/.local/bin`, `uninstall` finds `.exe` release binaries, and
  a quote in the home path no longer breaks the `PATH` command.
- `makeit uninstall` removes only the block between makeit's markers: a copy of its line you put
  elsewhere in the profile stays, and `setup` repairs a block edited inside instead of adding one.

## 0.2.1

Fixes:

- `makeit doctor` finds gopls or rust-analyzer when `go install` or rustup put it in a folder
  that is not on PATH, says which folder, and gives the line to add instead of the install again.
- `makeit doctor` shows the version a plugin loaded from the plugin set actually runs; Claude Code
  kept listing the version it first installed.
- A setting you hold reads as its name, for example `settings.json: model`, with what happens to it.

## 0.2.0

Breaking:

- `makeit self update` is now `makeit update` (it already updated makeit first); `--to` and
  `--allow-downgrade` moved with it.
- `makeit self rollback` is now `makeit rollback`, which undoes the whole last update.
- `makeit self` and `makeit docs` are no longer listed; `makeit self update` and
  `makeit self rollback` exit 2 and name the new command.

New:

- A new look at the terminal: one header, a row per area with a status mark, a live spinner and
  progress bar while it works, one result line and the next step. Pipes, CI, `NO_COLOR`, `--json`,
  `--plain` and `--verbose` keep plain lines.
- `makeit` with no command shows this machine's status and offers what to do next.
- The install script runs `makeit setup` right away (`--yes` to skip the question, `--no-setup`
  to install makeit only).
- Daily update: when Claude Code starts, makeit updates itself and every plugin to its newest
  release, at most once a day, in the background. The next session shows one line naming what
  moved. Off with `"autoUpdate": false` in `~/.makeit/config.json`.
- `makeit update` moves each plugin pinned to a release to the newest stable release of the same
  tag family, and the MCP servers' npm packages to their newest versions.
- `makeit rollback` undoes the last update: the files it wrote and nobody changed since, the plugin
  releases, and makeit itself when that update installed it. Never below 0.2.0.
- `makeit doctor` at a terminal counts the passing checks and spells out each problem with its fix;
  `--verbose` lists every check.

Fixes:

- Plugins from other GitHub repositories (superpowers, ponytail, security-audit) are fetched over
  HTTPS, so they update on a machine with no GitHub SSH key, such as a fresh install or CI.

## 0.1.1

Fixes:

- The reply-ending and HANDOFF Stop hooks send their note as feedback, so Claude Code shows
  "Stop hook feedback" instead of "Stop hook error"; Claude still rewrites its ending.
- `makeit setup` installs plugins the bundle declares but lacks, for example after an install that
  was cut short. Before, it saw the bundle and called it done.
- `makeit setup` says when a marketplace named `makeit` is yours and names the command that hands it
  over; the FAQ explains the switch.
- The README logo follows GitHub's light or dark theme instead of the operating system's.

## 0.1.0

First public release.

Commands:

- `makeit setup`: install the prerequisites, Claude Code, the plugin set, settings and global files.
- `makeit doctor`: check this machine against what makeit sets up, and print a fix per problem.
- `makeit init [dir]`: set up a project: `CLAUDE.md` and its docs, check tasks, commit hooks,
  settings, CI files and the docs index. `--update` rewrites the files init wrote that nobody
  changed since; `--rulesets` sends makeit's GitHub rulesets.
- `makeit update`: update makeit itself, then the plugin set, plugins, settings and global files.
- `makeit uninstall`: remove what makeit put on this machine and nobody changed since.
- `makeit self install`, `makeit self update`, `makeit self rollback`: install, update or roll
  back the makeit binary.
- `makeit docs sync [dir]`: index the docs of a project's dependencies.
- `makeit help [command]`, `makeit version`.

Also:

- The `/makeit:init` skill for Claude Code.
- `install.sh` and `install.ps1`, which check the release signature (with minisign) and the sha256.
- Builds for Linux, macOS and Windows, each on x64 and arm64.
