# Changelog

All notable changes to makeit. Versions follow 0.x while makeit is in beta: a breaking change
raises the minor version.

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
