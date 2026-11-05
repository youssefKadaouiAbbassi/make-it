# Changelog

All notable changes to makeit. Versions follow 0.x while makeit is in beta: a breaking change
raises the minor version.

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
