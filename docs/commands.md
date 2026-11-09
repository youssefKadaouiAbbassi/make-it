# Commands

Every command and flag, as `makeit help` prints them in makeit 0.4.0. Run `makeit help <command>` for the same text in your terminal.

```
makeit 0.4.0: a tested Claude Code setup for serious coding.

Usage: makeit [command] [flags]   (no command: status and what to do next)

Commands:
  doctor                 check this machine against what makeit sets up; changes nothing
  help [command|exit-codes|environment] show help for a command or a topic
  init [dir]             set up a project: CLAUDE.md and its docs, check tasks, hooks, settings, CI
  rollback               undo the last update that changed something
  setup                  set up this machine: prerequisites, Claude Code, the plugin set, settings and global files
  uninstall              remove what makeit put on this machine and nobody changed since
  update                 update everything: makeit itself, the plugins, settings and global files (also runs daily on its own)
  version                print the version

Flags for every command:
  --color <auto|always|never>        colour output (default auto; NO_COLOR is honoured)
  --dry-run, -n                      print the plan and the diffs; write nothing, ask nothing
  --help, -h                         show help for the command
  --json                             print one JSON report on stdout (implies --no-input); progress stays on stderr
  --no-input                         never ask; a missing answer exits 2 naming its flag
  --offline                          makeit itself makes no network request
  --plain                            plain lines: no spinner, progress bars or menus (also when CI is set or output is not a terminal)
  --quiet, -q                        print only warnings and errors
  --verbose                          print the commands run and their output
  --yes, -y                          apply the printed plan and take the recommended answer to every question; the only way to apply without a terminal

Run 'makeit help <command>' for its flags, 'makeit help exit-codes' or 'makeit help environment'.
```

## makeit setup

```
Usage: makeit setup [flags]

set up this machine: prerequisites, Claude Code, the plugin set, settings and global files

Flags:
  --allow-branch                     let --catalog follow a #<branch>: each update takes its newest commit
  --catalog <src>                    where the plugin set comes from: embedded (the one in this makeit), a directory, owner/repo or a git URL, with #<tag or branch> or @<commit>; kept for later runs
  --reinstall <tool>                 install this tool again although you removed it (comma-separated for several)

Flags for every command:
  --color <auto|always|never>        colour output (default auto; NO_COLOR is honoured)
  --dry-run, -n                      print the plan and the diffs; write nothing, ask nothing
  --help, -h                         show help for the command
  --json                             print one JSON report on stdout (implies --no-input); progress stays on stderr
  --no-input                         never ask; a missing answer exits 2 naming its flag
  --offline                          makeit itself makes no network request
  --plain                            plain lines: no spinner, progress bars or menus (also when CI is set or output is not a terminal)
  --quiet, -q                        print only warnings and errors
  --verbose                          print the commands run and their output
  --yes, -y                          apply the printed plan and take the recommended answer to every question; the only way to apply without a terminal
```

## makeit init

```
Usage: makeit init [dir] [flags]

set up a project: CLAUDE.md and its docs, check tasks, hooks, settings, CI

Flags:
  --applied <version>                record that /makeit:init applied the listed init changes up to version (the initTarget init --json reports); writes only .makeit.json
  --keep <line>                      with --applied: a difference from the current way the user kept, never raised again
  --rulesets                         also send makeit's GitHub rulesets to the repository (needs gh and a GitHub remote)
  --update                           also update files init wrote earlier that nobody changed since

Flags for every command:
  --color <auto|always|never>        colour output (default auto; NO_COLOR is honoured)
  --dry-run, -n                      print the plan and the diffs; write nothing, ask nothing
  --help, -h                         show help for the command
  --json                             print one JSON report on stdout (implies --no-input); progress stays on stderr
  --no-input                         never ask; a missing answer exits 2 naming its flag
  --offline                          makeit itself makes no network request
  --plain                            plain lines: no spinner, progress bars or menus (also when CI is set or output is not a terminal)
  --quiet, -q                        print only warnings and errors
  --verbose                          print the commands run and their output
  --yes, -y                          apply the printed plan and take the recommended answer to every question; the only way to apply without a terminal
```

## makeit update

```
Usage: makeit update [flags]

update everything: makeit itself, the plugins, settings and global files (also runs daily on its own)

Flags:
  --allow-branch                     let --catalog follow a #<branch>: each update takes its newest commit
  --allow-downgrade                  allow --to an older version
  --auto                             the daily run Claude Code starts: at most once a day, no questions, output to ~/.makeit/update.log
  --catalog <src>                    where the plugin set comes from: embedded (the one in this makeit), a directory, owner/repo or a git URL, with #<tag or branch> or @<commit>; kept for later runs
  --to <version>                     move makeit to this version instead of the latest; clears a skip

Flags for every command:
  --color <auto|always|never>        colour output (default auto; NO_COLOR is honoured)
  --dry-run, -n                      print the plan and the diffs; write nothing, ask nothing
  --help, -h                         show help for the command
  --json                             print one JSON report on stdout (implies --no-input); progress stays on stderr
  --no-input                         never ask; a missing answer exits 2 naming its flag
  --offline                          makeit itself makes no network request
  --plain                            plain lines: no spinner, progress bars or menus (also when CI is set or output is not a terminal)
  --quiet, -q                        print only warnings and errors
  --verbose                          print the commands run and their output
  --yes, -y                          apply the printed plan and take the recommended answer to every question; the only way to apply without a terminal
```

## makeit doctor

```
Usage: makeit doctor [flags]

check this machine against what makeit sets up; changes nothing

Flags for every command:
  --color <auto|always|never>        colour output (default auto; NO_COLOR is honoured)
  --dry-run, -n                      print the plan and the diffs; write nothing, ask nothing
  --help, -h                         show help for the command
  --json                             print one JSON report on stdout (implies --no-input); progress stays on stderr
  --no-input                         never ask; a missing answer exits 2 naming its flag
  --offline                          makeit itself makes no network request
  --plain                            plain lines: no spinner, progress bars or menus (also when CI is set or output is not a terminal)
  --quiet, -q                        print only warnings and errors
  --verbose                          print the commands run and their output
  --yes, -y                          apply the printed plan and take the recommended answer to every question; the only way to apply without a terminal
```

## makeit rollback

```
Usage: makeit rollback [flags]

undo the last update that changed something

Flags for every command:
  --color <auto|always|never>        colour output (default auto; NO_COLOR is honoured)
  --dry-run, -n                      print the plan and the diffs; write nothing, ask nothing
  --help, -h                         show help for the command
  --json                             print one JSON report on stdout (implies --no-input); progress stays on stderr
  --no-input                         never ask; a missing answer exits 2 naming its flag
  --offline                          makeit itself makes no network request
  --plain                            plain lines: no spinner, progress bars or menus (also when CI is set or output is not a terminal)
  --quiet, -q                        print only warnings and errors
  --verbose                          print the commands run and their output
  --yes, -y                          apply the printed plan and take the recommended answer to every question; the only way to apply without a terminal
```

## makeit uninstall

```
Usage: makeit uninstall [flags]

remove what makeit put on this machine and nobody changed since

Flags:
  --keep-settings                    leave settings.json as it is
  --purge                            also delete the data of the plugins makeit installed, and the makeit directory with every backup; cannot be undone

Flags for every command:
  --color <auto|always|never>        colour output (default auto; NO_COLOR is honoured)
  --dry-run, -n                      print the plan and the diffs; write nothing, ask nothing
  --help, -h                         show help for the command
  --json                             print one JSON report on stdout (implies --no-input); progress stays on stderr
  --no-input                         never ask; a missing answer exits 2 naming its flag
  --offline                          makeit itself makes no network request
  --plain                            plain lines: no spinner, progress bars or menus (also when CI is set or output is not a terminal)
  --quiet, -q                        print only warnings and errors
  --verbose                          print the commands run and their output
  --yes, -y                          apply the printed plan and take the recommended answer to every question; the only way to apply without a terminal
```

## makeit version

```
Usage: makeit version [flags]

print the version

Flags for every command:
  --color <auto|always|never>        colour output (default auto; NO_COLOR is honoured)
  --dry-run, -n                      print the plan and the diffs; write nothing, ask nothing
  --help, -h                         show help for the command
  --json                             print one JSON report on stdout (implies --no-input); progress stays on stderr
  --no-input                         never ask; a missing answer exits 2 naming its flag
  --offline                          makeit itself makes no network request
  --plain                            plain lines: no spinner, progress bars or menus (also when CI is set or output is not a terminal)
  --quiet, -q                        print only warnings and errors
  --verbose                          print the commands run and their output
  --yes, -y                          apply the printed plan and take the recommended answer to every question; the only way to apply without a terminal
```

## exit-codes

```
makeit exit codes:
  0     ok
  1     a step failed, or doctor found an error
  2     usage error, or consent needed
  130   interrupted (SIGINT)
  143   terminated (SIGTERM)
```

## environment

```
Environment variables makeit reads:
  CLAUDE_CONFIG_DIR   Claude Code's configuration directory (default ~/.claude)
  MAKEIT_DIR          where makeit keeps its copy of the plugin set, state and generations (default ~/.makeit)
  MAKEIT_RELEASES_URL where makeit looks for releases (https only; default https://github.com)
  MAKEIT_REGISTRY_URL where makeit reads Claude Code's newest version (https only; default https://registry.npmjs.org)
  MAKEIT_REEXEC       set by update for the new makeit it runs: the version it replaced
  GIT_SSH_COMMAND     used for a --catalog git source over ssh when set; otherwise ssh runs in batch mode
  GIT_SSH             used for a --catalog git source over ssh when set, as is core.sshCommand; makeit then leaves ssh as you set it
  HOME                the home directory (USERPROFILE on Windows)
  SHELL               picks .zshenv for the PATH line when it names zsh; empty means not zsh
  ZDOTDIR             where .zshenv goes when zsh reads it from here instead of HOME
  PATH                where claude, npm, git and the package managers are looked up
  USERPROFILE         the home directory on Windows
  GOBIN               where doctor looks for gopls that go install put off PATH (then GOPATH/bin, then ~/go/bin)
  GOPATH              its first entry's bin is where doctor looks for gopls when GOBIN is not set
  CARGO_HOME          its bin is where doctor looks for rust-analyzer off PATH (default ~/.cargo/bin)
  SSL_CERT_FILE       a CA bundle read first, before the system ones, for makeit's downloads
  SSL_CERT_DIR        a folder of CA certificates read after SSL_CERT_FILE
  NO_COLOR            when set, no colour
  FORCE_COLOR         when set, colour even without a terminal (NO_COLOR, TERM=dumb and ACCESSIBLE win)
  TERM                dumb turns colour off
  CI                  when set, plain output: no spinner, progress bars or prompts
  ACCESSIBLE          when set, no colour
  WSL_DISTRO_NAME     when set, the machine is taken as WSL
  WSL_INTEROP         when set, the machine is taken as WSL
```
