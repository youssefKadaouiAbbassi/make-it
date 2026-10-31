# makeit

makeit installs and maintains a Claude Code setup on your machine: Claude Code itself, a pinned set of
plugins, settings and global files, and per project the files and checks that go with them.

makeit is in beta (0.x): commands and files may still change.

## Install

Linux and macOS:

    curl -fsSL https://github.com/youssefKadaouiAbbassi/make-it/releases/download/v0.1.0/install.sh | sh

Windows (PowerShell):

    irm https://github.com/youssefKadaouiAbbassi/make-it/releases/download/v0.1.0/install.ps1 | iex

The script checks the binary's sha256 against the release's `checksums.txt`. With
[minisign](https://jedisct1.github.io/minisign/) installed it first verifies the owner's signature on
that list, and stops if it does not verify; without minisign it says the signature was not checked.
Then it runs `makeit self install`, which puts makeit in `~/.local/bin`. Then run `makeit setup`.

## Commands

| command | what it changes |
|---|---|
| `makeit setup` | installs the missing prerequisites, Claude Code, the plugin catalog, settings and global files |
| `makeit doctor` | nothing: checks this machine against what the stack expects and prints a fix per problem |
| `makeit init [dir]` | writes a project's CLAUDE.md, CONSTRAINTS.md, check tasks, hook config, CI caller and docs index |
| `makeit update` | brings the catalog, plugins, settings and global files to what this makeit carries |
| `makeit uninstall` | removes what makeit put on this machine and nobody changed since |
| `makeit self update` | replaces the makeit binary with the newest signed release (`makeit self rollback` undoes it) |

Every command that changes something prints its plan first and asks once. `--dry-run` prints the plan
and writes nothing; `--yes` applies it without asking; `--json` prints one report on stdout.
`makeit help <command>` lists a command's flags.

## What it installs

The prerequisites (git, Node.js, jq, CA certificates, and optional tools such as gh and the language
servers), Claude Code, the `makeit` plugin marketplace with its bundle, and the settings and global
files the plugins rely on. [INSTALL.md](INSTALL.md) lists each part and why it is there.

## Remove it

`makeit uninstall` removes the catalog, plugins, settings values and files makeit added. A file or
setting you changed after makeit wrote it is kept and listed. `makeit uninstall --dry-run` shows
what would go.

## License

MIT, see [LICENSE](LICENSE).

### Third-party

- `ci/floor-guard.mjs`, `ci/make-floor-guard.py`, `templates/project/CONSTRAINTS.md` and
  `plugins/makeit/skills/expand-contract` derive from
  [addyosmani/agent-skills](https://github.com/addyosmani/agent-skills), MIT
  ([plugins/makeit/LICENSES](plugins/makeit/LICENSES/agent-skills-MIT.txt),
  [templates/LICENSES](templates/LICENSES/agent-skills-MIT.txt)).
- The other plugins in the catalog are installed from their own repositories, under their own
  licenses; makeit does not copy them.
