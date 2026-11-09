<!-- The #gh-*-mode-only fragments follow GitHub's theme setting; <picture> follows the OS and showed
     the white logo on GitHub's light page. -->
<p align="center">
  <img alt="makeit" src="docs/assets/logo-light.svg#gh-light-mode-only" width="300">
  <img alt="makeit" src="docs/assets/logo-dark.svg#gh-dark-mode-only" width="300">
</p>

<p align="center">A tested Claude Code setup for serious coding.</p>

makeit sets up Claude Code for real software work, the same way on every machine and every
project, and keeps it current. It installs a plugin set where every core plugin was tested against
Claude Code without it and against its alternatives (the losers were dropped), then sets each
project up so Claude knows its rules, runs its checks, and remembers where the last session
stopped. [What was tested](docs/included.md#the-plugins).

<p align="center">
  <a href="https://github.com/youssefKadaouiAbbassi/make-it/releases/latest"><img alt="release" src="https://img.shields.io/github/v/release/youssefKadaouiAbbassi/make-it?color=555"></a>
  <a href="LICENSE"><img alt="license" src="https://img.shields.io/github/license/youssefKadaouiAbbassi/make-it?color=555"></a>
  <a href="https://github.com/youssefKadaouiAbbassi/make-it/actions/workflows/ci.yml"><img alt="ci" src="https://img.shields.io/github/actions/workflow/status/youssefKadaouiAbbassi/make-it/ci.yml?branch=main&label=ci"></a>
  <img alt="status: beta" src="https://img.shields.io/badge/status-beta-6e7681">
</p>

> makeit is in beta (0.x): commands and files may still change between versions.

## What it does

makeit works at three levels:

- **Your machine.** `makeit setup` installs Claude Code, the plugin set, the settings and the
  global rules every session follows: verify before saying "done", test edges and errors, keep
  changes small, ask before anything irreversible.
- **Each project.** `/makeit:init` in Claude Code writes the files Claude reads at session start
  (`CLAUDE.md`, `CONSTRAINTS.md`, `HANDOFF.md`), wires `check:fast`, `check:task` and
  `check:full` to your real linter and tests, adds commit hooks and a CI workflow, and indexes the
  docs of your dependencies at the versions you use.
- **Every session.** Claude reads the handoff the last session left, searches your dependencies'
  docs instead of guessing, runs the checks before calling work done, and asks you in one
  keystroke when it needs a decision. Once a day makeit moves everything to its newest release;
  `makeit rollback` undoes it.

Every change makeit makes is shown as a plan first, a file you edited is never overwritten, and
`makeit uninstall` removes what makeit added and you did not change.

## Install

macOS and Linux:

```sh
curl -fsSL https://github.com/youssefKadaouiAbbassi/make-it/releases/latest/download/install.sh | sh
```

Windows (PowerShell):

```powershell
irm https://github.com/youssefKadaouiAbbassi/make-it/releases/latest/download/install.ps1 | iex
```

The installer checks the release's checksums (and its signature when
[minisign](https://jedisct1.github.io/minisign/) is installed), then runs `makeit setup`, which
shows its plan and asks once:

```console
$ makeit setup

  ● Machine        4 changes
  ● Claude Code    install Claude Code
  ● Plugins        5 changes
  ● Settings       merge makeit's settings into /home/u/.claude/settings.json
  ● Files          7 changes

  Apply this plan? Yes

  ✓ Ready in 124s · 17 changed · 1 already
    Open Claude Code to start.  undo: makeit rollback
```

## Start working

1. Open Claude Code in a project and type `/makeit:init`. Claude sets the project up, runs its
   checks, and asks before the first commit.
2. Give Claude work in plain words. For something new it asks what you want, proposes a design and
   a plan, then builds it test-first; for a bug it finds the cause first; either way it shows the
   checks passing before it says "done".
3. Answer with one keystroke: a dialog for decisions, a digit for the numbered suggestions at the
   end of a reply.
4. Stop whenever you like. Claude leaves a `HANDOFF.md`, and tomorrow's session starts from it.

[Working with makeit every day](docs/daily.md) walks through a full day: sessions, reviews,
lookups, notifications, and what to do when something goes wrong.

## Commands

| Command | What it changes |
|---|---|
| `makeit setup` | installs the missing prerequisites, Claude Code, the plugin set, settings and global files |
| `makeit init [dir]` | writes a project's CLAUDE.md, CONSTRAINTS.md, check tasks, hook config and CI caller |
| `makeit doctor` | nothing; checks the machine and offers to run the fixes it can |
| `makeit update` | moves makeit, Claude Code, the plugins and their tools to their newest releases (also runs daily on its own) |
| `makeit rollback` | undoes the last update: files, plugin versions and makeit itself |
| `makeit uninstall` | removes what makeit added and you did not change since |
| `makeit` | shows this machine's status and what to do next |

Every command that changes something prints its plan first and asks once. `--dry-run` prints the
plan and writes nothing, `--yes` applies it without asking, and `--json` prints one report.
All commands and flags: [docs/commands.md](docs/commands.md).

## What's included

| Part | What it does |
|---|---|
| makeit plugin | session hooks (handoff, docs, reply checks), the `/makeit:init` skill |
| superpowers | planning, test-driven development and review skills |
| ponytail | keeps changes small |
| chrome-devtools-mcp, docs-mcp-server | a browser to check web work, and search over your dependencies' docs |
| deja-vu, security-audit | memory across sessions, and security review |
| four LSP plugins | code intelligence for TypeScript, Python, Rust and Go |

Each part, why it is there, and the optional tools: [docs/included.md](docs/included.md).

## Security

Releases are signed with the maintainer's [minisign](https://jedisct1.github.io/minisign/) key.
Published releases cannot be changed, and only the maintainer can create version tags. The release
key, also in [minisign.pub](minisign.pub):

```
RWSX6Cc3XFkIxiPHeM4IZ4jBFnOnfiMOROpHoZkjxB3p39XW6qrkkp79
```

To verify a download yourself: `minisign -V -P <key> -m checksums.txt`. To report a vulnerability,
see [SECURITY.md](SECURITY.md).

## Documentation

[Getting started](docs/getting-started.md) ·
[Every day](docs/daily.md) ·
[Commands](docs/commands.md) ·
[What's included](docs/included.md) ·
[Projects and /makeit:init](docs/projects.md) ·
[FAQ](docs/faq.md) ·
[Changelog](CHANGELOG.md) ·
[Contributing](CONTRIBUTING.md)

## License

MIT, see [LICENSE](LICENSE).

### Third-party

- `ci/floor-guard.mjs`, `ci/make-floor-guard.py`, `templates/project/CONSTRAINTS.md` and
  `plugins/makeit/skills/expand-contract` derive from
  [addyosmani/agent-skills](https://github.com/addyosmani/agent-skills), MIT
  ([notice](plugins/makeit/LICENSES/agent-skills-MIT.txt)).
- The logo is drawn from [Inter](https://github.com/rsms/inter) and
  [JetBrains Mono](https://github.com/JetBrains/JetBrainsMono), both under the SIL Open Font License
  ([notices](docs/assets/LICENSES)).
- The other plugins are installed from their own repositories, under their own licenses; makeit
  does not copy them.
