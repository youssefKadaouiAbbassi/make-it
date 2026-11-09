# FAQ

## Will makeit overwrite my changes?

No. Every file and setting makeit touches follows the same rule: what was there before makeit is
yours, what makeit wrote and you changed since is yours, and makeit only ever updates or removes
what it wrote and nobody touched. `--dry-run` on any command prints every path it would change,
with the old and new value.

For `~/.claude/settings.json`, makeit compares three things: your current file, the values makeit
wants, and the values it wrote last time (recorded in `~/.makeit`). setup adds only the values you
do not have. update moves a value makeit wrote to the new release's value only while you have not
changed it. uninstall removes only the values makeit added and you did not change. Before every
write, makeit copies the file into `~/.makeit/generations` (the last five runs are kept) and checks
the new file with Claude Code; it puts the old one back when the new one does not validate.

The global files in `~/.claude` follow the same rule, and so do the files `makeit init` writes in
a project ([Running init again](projects.md#running-init-again)).

## Which versions do I have?

`makeit version` for makeit, `claude plugin list` for the plugins, `makeit doctor --verbose` for
every tool makeit knows and where it found it. The daily run moves everything to its newest
release, so your versions are usually newer than the ones a release was tested with
([What makeit installs](included.md#the-plugins)).

## What did the daily run do?

`~/.makeit/update.log` has each run's plan and result. A run that moved something also leaves one
line for the next Claude Code session (`makeit updated: superpowers 7.0.1 → 7.0.2 · undo: makeit
rollback`). `makeit` with no command shows when the last update ran and whether the daily run is
on.

## How do I undo an update?

`makeit rollback` undoes the last update that changed something: it puts back the files that update
wrote (a file you changed since is kept), moves the plugins back to their previous releases, and
puts back the previous makeit when that update installed a new one. The daily run then skips the
plugin releases you rolled back from; `makeit update` run by hand takes them. Run it again to go
one more update back. makeit never rolls itself back below 0.2.0.

## How do I turn the daily update off?

Put `{"autoUpdate": false}` in `~/.makeit/config.json`. The daily run starts when Claude Code
starts, at most once in 24 hours; it asks nothing, writes to `~/.makeit/update.log`, and leaves one
line for the next session when something moved. Run `makeit update` yourself when you want it.

## I don't want Chrome

setup installs it once, because chrome-devtools-mcp needs a real Chrome. Remove it the normal way
afterwards (`sudo apt remove google-chrome-stable`, for example) and makeit leaves it alone from
then on, the same as any tool you take away after it installs one. `makeit doctor` will list it as
a warning; Claude simply has no browser.

## I removed a tool makeit installed

It stays removed: makeit never reinstalls a tool it put on your machine once you take it away. To
get it back, `makeit setup --reinstall <tool>`, naming the tool as the
[tools table](included.md#tools-on-path) does (for example `prek`, `gopls`).

## Can I use makeit offline?

`--offline` stops makeit itself from making network requests:

- `setup` and `update` do not fetch a `--catalog` git source: a recorded one stays where it is, and
  a new one is refused. The plugin set built into makeit is still applied.
- `update` skips replacing makeit with a newer release, skips the check for one, and keeps every
  plugin at its current release.
- `init` refuses `--rulesets`.

The programs makeit runs still use the network when they need it: package managers that install
prerequisites, and Claude Code when it installs or updates plugins. `makeit doctor` makes no
network request of its own, with or without `--offline`.

## Does makeit work on Windows?

makeit ships Windows builds (x64 and arm64) and an install script for PowerShell:

```console
PS> irm https://github.com/youssefKadaouiAbbassi/make-it/releases/latest/download/install.ps1 | iex
```

On Windows, setup installs prerequisites and Claude Code with winget.

What is tested: makeit's test runs are on Linux (Ubuntu, Debian, Fedora and Alpine). `install.ps1`
has not run on Windows yet, and neither has setup on Windows or macOS. The Windows and macOS paths
are written for those systems but not tested on them. Some per-project checks that init writes,
such as `check:api`, are shell commands and need a POSIX shell.

If something fails on Windows or macOS, please [open an issue](https://github.com/youssefKadaouiAbbassi/make-it/issues)
with the output of `makeit doctor`.

## Where does the plugin set come from?

By default, from makeit itself: each release carries a plugin set, a Claude Code marketplace that
pins every plugin to a release tag and its commit. `update` moves each plugin to the newest stable
release of the same tag family, never below the pin it has. `--catalog` on `setup` and `update`
points makeit at another plugin set instead (the flag keeps its older name):

```console
$ makeit setup --catalog owner/repo
$ makeit update --catalog owner/repo#v1.2.0
$ makeit update --catalog embedded
```

The source can be `embedded` (the one in this makeit), a local directory, `owner/repo` on GitHub,
or a git URL over https or ssh. Add `#<tag>` or `@<commit>` to stay on one version, or
`#<branch> --allow-branch` to follow a branch. Without a ref, makeit takes the newest release tag
and moves to each newer one on `update`. makeit remembers the source for later runs. It stores no
token: a private repository needs a git credential helper or an SSH key, and a URL with a password
or token in it is refused.

## I already added a marketplace named makeit

makeit never changes a marketplace it did not add. If you added one named `makeit` yourself, for
example from a clone of this repository, `makeit setup` keeps it and its plugins, and its plan
says so in a `kept:` line. To use makeit's own plugin set instead, remove yours first, then run
setup:

```console
$ claude plugin marketplace remove makeit
$ makeit setup
```

Removing a marketplace uninstalls the plugins that came from it; setup installs makeit's versions.

## Why does makeit ask before every change?

Every command that changes something prints its plan first and asks once, so you see what a run
will do to your machine before it does it. Without a terminal it prints the plan and exits 2
unless you pass `--yes`. `--dry-run` prints the plan and the diffs and writes nothing.

## What does beta mean here?

makeit is at 0.x. Commands, flags and the files it writes may still change between releases. A
breaking change raises the minor version (0.1 to 0.2). Every release is a normal GitHub release,
so `makeit update` takes it.

## How do I uninstall makeit?

```console
$ makeit uninstall --dry-run
$ makeit uninstall
```

The dry run lists what would be removed and what would stay. The prompt defaults to No.

uninstall removes what makeit put on the machine and nobody changed since: the plugins it
installed, the `makeit` marketplace, the settings values it added, the global files it wrote, the
empty directories it created, and its copy of the plugin set. It keeps, and lists with the reason:

- a setting or file you changed after makeit wrote it;
- anything that was there before makeit (a plugin, a marketplace, a setting, a file);
- the `makeit` marketplace, while a plugin of yours still comes from it;
- the `claude-plugins-official` marketplace;
- Claude Code and the prerequisites.

Plugin data is kept, and copied into `~/.makeit` before the marketplace is removed. Flags:

- `--keep-settings` leaves `settings.json` as it is.
- `--purge` also deletes the data of the plugins makeit installed and every backup makeit kept.
  It cannot be undone.

uninstall does not remove makeit itself. It ends by naming the binary's path. To remove it,
delete that file (by default `~/.local/bin/makeit`) and the `~/.makeit` directory.

Projects keep the files `makeit init` wrote. Remove them with git, like any other change.
