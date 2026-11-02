# Getting started

makeit sets up Claude Code the same way on every machine and every project. This page takes a
machine from nothing to a working setup, then sets up one project.

makeit is in beta (0.x): commands and files may still change.

## Install makeit

Linux and macOS:

```console
$ curl -fsSL https://github.com/youssefKadaouiAbbassi/make-it/releases/latest/download/install.sh | sh
```

Windows (PowerShell):

```console
PS> irm https://github.com/youssefKadaouiAbbassi/make-it/releases/latest/download/install.ps1 | iex
```

The script downloads the makeit binary for your machine, checks it against the release's
`checksums.txt`, and runs `makeit self install`. That puts makeit in `~/.local/bin`
(`%USERPROFILE%\.local\bin` on Windows). If that directory is not on your `PATH`, makeit prints
the line to add. Then the script runs `makeit setup` (below), so one command sets up the machine.
`| sh -s -- --yes` sets up without asking, `| sh -s -- --no-setup` installs makeit only; on
Windows, set `MAKEIT_YES=1` or `MAKEIT_NO_SETUP=1` first.

With [minisign](https://jedisct1.github.io/minisign/) installed, the script also verifies the
release signature first and stops if it does not verify. Without minisign it says the signature
was not checked. See [SECURITY.md](../SECURITY.md) to verify a release yourself.

Check that it runs:

```console
$ makeit version
```

## Set up your machine

The install script already ran this. To run it again, or after `--no-setup`:

```console
$ makeit setup
```

`setup` installs what is missing: the required prerequisites, Claude Code, the makeit plugin set,
the settings and the global files. It prints the plan first and asks once. To see the plan and
change nothing:

```console
$ makeit setup --dry-run
```

To apply without a prompt, for example in a script, add `--yes`.

A second run reports every step as already done. On a machine where some of this is already installed, setup
keeps what is there and adds only what is missing. [What makeit installs](included.md) lists
each part.

## Check your machine

```console
$ makeit doctor
```

`doctor` changes nothing. It prints one line per check (`ok`, `!!` for a warning, `xx` for an
error) and, under each problem, the command that fixes it. It exits 1 when it finds an error.

Optional tools that setup does not install, such as `gopls`, show up here as warnings with the
command to install them by hand.

## Set up a project

In Claude Code, inside the project, type:

```
/makeit:init
```

This runs `makeit init`, which writes the project files, then has Claude fill in the parts that
depend on your project and run the project's checks. Claude asks before the first commit.

To write the files without Claude:

```console
$ makeit init
```

[Projects](projects.md) explains what is written and how re-runs work.

## Keep it current

```console
$ makeit update
```

You do not need to run it: once a day, when Claude Code starts, makeit runs `makeit update --auto`
in the background. It writes to `~/.makeit/update.log`, and the next session shows one line naming
what moved. Run `makeit update` yourself to update now.

`update` first replaces makeit with the newest signed release, when makeit was installed by the
install script. Then it moves each plugin to its newest release and brings the settings and the
global files to what that makeit carries. Anything you changed is kept and listed.

To undo the last update:

```console
$ makeit rollback
```

In each project, after an update:

```console
$ makeit init --update
```

This rewrites the files init wrote earlier that nobody changed since.

Every command takes `--dry-run`, `--yes` and `--json`. `makeit help <command>` lists a command's
flags.
