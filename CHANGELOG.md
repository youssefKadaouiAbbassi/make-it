# Changelog

All notable changes to makeit. Versions follow 0.x while makeit is in beta: a breaking change
raises the minor version.

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
