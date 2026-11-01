<!-- The #gh-*-mode-only fragments follow GitHub's theme setting; <picture> follows the OS and showed
     the white logo on GitHub's light page. -->
<p align="center">
  <img alt="makeit" src="docs/assets/logo-light.svg#gh-light-mode-only" width="300">
  <img alt="makeit" src="docs/assets/logo-dark.svg#gh-dark-mode-only" width="300">
</p>

<p align="center">A tested Claude Code setup for serious coding.</p>

The core plugins in makeit were each tested against Claude Code without them and against their
alternatives; the ones that lost were dropped. One command installs it, on every machine and every
project. [What was tested](docs/included.md#the-plugin-set).

<p align="center">
  <a href="https://github.com/youssefKadaouiAbbassi/make-it/releases/latest"><img alt="release" src="https://img.shields.io/github/v/release/youssefKadaouiAbbassi/make-it?color=555"></a>
  <a href="LICENSE"><img alt="license" src="https://img.shields.io/github/license/youssefKadaouiAbbassi/make-it?color=555"></a>
  <a href="https://github.com/youssefKadaouiAbbassi/make-it/actions/workflows/ci.yml"><img alt="ci" src="https://img.shields.io/github/actions/workflow/status/youssefKadaouiAbbassi/make-it/ci.yml?branch=main&label=ci"></a>
  <img alt="status: beta" src="https://img.shields.io/badge/status-beta-6e7681">
</p>

```console
$ makeit setup
  will: Install git
  will: Install node
  will: Install Claude Code
  will: Write the plugin set to /home/dev/.makeit/catalog
  will: Install makeit-bundle@makeit
  will: Merge makeit's settings into /home/dev/.claude/settings.json
  will: Write /home/dev/.claude/CLAUDE.md
? Apply this plan? [Y/n]
ok Install git
ok Install node
ok Install Claude Code
ok Install makeit-bundle@makeit
ok Merge makeit's settings into /home/dev/.claude/settings.json
17 done
```

<p align="center"><sub>makeit setup on a fresh Ubuntu 24.04: the plan, one question, the result. Lines cut from the real output; nothing retyped.</sub></p>

> makeit is in beta (0.x): commands and files may still change between versions.

## Highlights

- A tested set of plugins, settings and global rules for Claude Code, installed in one step.
- Sets up any project for Claude: CLAUDE.md, check commands, git hooks and CI, with `/makeit:init`
  inside Claude Code.
- Every change is shown as a plan first; `--dry-run` writes nothing.
- Never overwrites your edits: a file you changed is kept and reported.
- Signed releases; makeit updates itself and rolls back.
- `makeit uninstall` removes what makeit added and you did not change.
- Builds for Linux, macOS and Windows (tested on Linux).

## Installation

macOS and Linux:

```sh
curl -fsSL https://github.com/youssefKadaouiAbbassi/make-it/releases/latest/download/install.sh | sh
```

Windows (PowerShell):

```powershell
irm https://github.com/youssefKadaouiAbbassi/make-it/releases/latest/download/install.ps1 | iex
```

Then run `makeit setup`. The installer checks the release's checksums, and its signature too when
[minisign](https://jedisct1.github.io/minisign/) is installed; see [Security](#security).

## Set up a project

Inside Claude Code, in your project:

```
/makeit:init
```

makeit writes the project files, then Claude fills in the project's facts, wires the check tasks to
your real linter and tests, runs them, and asks before the first commit. The same files without
Claude: `makeit init`. A dry run on a new Node project:

```console
$ makeit init --dry-run
  will: Write CLAUDE.md
  will: Write CONSTRAINTS.md
  will: Write HANDOFF.md
  will: Add makeit's entries to package.json
  will: Add makeit's entries to .gitignore
  will: Add makeit's entries to renovate.json
  will: Write .pre-commit-config.yaml
  will: Write .env.example
  will: Write .grype.yaml
  will: Write .github/workflows/gate.yml
  will: Write ci/floor-guard.mjs
  will: Record what init wrote in .makeit.json
12 would-change
```

See [Projects](docs/projects.md) for what each file is and how re-runs behave.

## Keep it current

`makeit update` brings the plugin set, plugins, settings and global files to what your makeit
carries. `makeit self update` installs the newest signed makeit, and `makeit self rollback` goes back
one version.

## Check your machine

`makeit doctor` changes nothing; it checks this machine and prints a fix for each problem:

```console
$ makeit doctor
ok git found
ok node found
!! pyright is missing: the pyright-lsp plugin runs it for .py files
   fix: npm install -g pyright
ok Claude Code 2.1.316
ok settings.json has every makeit value
ok makeit-bundle@makeit 0.1.0
0 errors, 7 warnings
```

## Commands

| Command | What it changes |
|---|---|
| `makeit setup` | installs the missing prerequisites, Claude Code, the plugin set, settings and global files |
| `makeit init [dir]` | writes a project's CLAUDE.md, CONSTRAINTS.md, check tasks, hook config and CI caller |
| `makeit doctor` | nothing; it reports each problem with a fix |
| `makeit update` | brings the plugin set, plugins, settings and global files to what this makeit carries |
| `makeit self update` | replaces makeit with the newest signed release (`makeit self rollback` undoes it) |
| `makeit uninstall` | removes what makeit added and you did not change since |

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
