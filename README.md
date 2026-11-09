<!-- The #gh-*-mode-only fragments follow GitHub's theme setting; <picture> follows the OS and showed
     the white logo on GitHub's light page. -->
<p align="center">
  <img alt="makeit" src="docs/assets/logo-light.svg#gh-light-mode-only" width="300">
  <img alt="makeit" src="docs/assets/logo-dark.svg#gh-dark-mode-only" width="300">
</p>

<h3 align="center">Claude Code, set up the way the measurements say.</h3>

<p align="center">Every plugin in makeit had to beat Claude Code without it, and its rivals, on the same tasks.<br>
One command installs the winners on every machine and in every project, and keeps them current.</p>

<p align="center">
  <a href="https://github.com/youssefKadaouiAbbassi/make-it/releases/latest"><img alt="release" src="https://img.shields.io/github/v/release/youssefKadaouiAbbassi/make-it?color=555"></a>
  <a href="LICENSE"><img alt="license" src="https://img.shields.io/github/license/youssefKadaouiAbbassi/make-it?color=555"></a>
  <a href="https://github.com/youssefKadaouiAbbassi/make-it/actions/workflows/ci.yml"><img alt="ci" src="https://img.shields.io/github/actions/workflow/status/youssefKadaouiAbbassi/make-it/ci.yml?branch=main&label=ci"></a>
  <img alt="status: beta" src="https://img.shields.io/badge/status-beta-6e7681">
</p>

```sh
curl -fsSL https://github.com/youssefKadaouiAbbassi/make-it/releases/latest/download/install.sh | sh
```

<p align="center"><sub>macOS and Linux. Windows: <code>irm https://github.com/youssefKadaouiAbbassi/make-it/releases/latest/download/install.ps1 | iex</code></sub></p>

## Why makeit

Most Claude Code setups are a list of plugins someone liked. Each one adds instructions to every
session, and nobody checked whether the sessions got better.

makeit was built the other way round. Each candidate ran against Claude Code without it, and
against the alternatives, on the same tasks, and only the ones that changed the result stayed:

- **A working method** ([superpowers](https://github.com/obra/superpowers)): Claude asks before
  building something new, writes a design and a plan, builds test-first, debugs from the root
  cause. On 29 hard tasks run twice it resolved **43 of 58** where plain Claude Code resolved 38,
  at lower cost.
- **Small changes** ([ponytail](https://github.com/DietrichGebert/ponytail)): the smallest change
  that solves the task, nothing nobody asked for. The cheapest and fastest option on the UI
  tasks; chosen over Caveman.
- **Design that holds up** ([impeccable](https://github.com/pbakaus/impeccable), for web
  projects): won every blind comparison against seven other design skills.
- **Eyes, docs and memory**: a real Chrome to check web work, the docs of your dependencies at the
  versions you use, and your own past sessions. Claude used each one when it had it, and guessed
  when it didn't: with no index, it searched docs **0 times in 6 runs**.

Caveman, Laya, hallmark and others were tested and left out. The full table, with each measurement:
[What's included](docs/included.md#the-plugins).

Then makeit makes the winners boring to run. One command puts the same setup on every machine.
`/makeit:init` puts it in every project. Once a day it moves everything to the newest release,
and `makeit rollback` undoes it. Releases are signed and cannot be changed after they are
published.

## What changes

| Without makeit | With makeit |
|---|---|
| Every session starts from zero | Claude reads the last session's handoff first, and writes one when it stops |
| "Done" means Claude stopped typing | "Done" means `check:task` passed, with the output in the reply |
| A new feature starts with code | It starts with questions, a design you approve, and a plan |
| A bug fix starts with a guess | It starts with the cause, and a test that fails without the fix |
| Library answers come from memory | From the docs of the version your project uses |
| Decisions arrive as a wall of prose | As a dialog or numbered choices; you answer with one keystroke |
| Your setup drifts per machine and per project | One setup everywhere, updated daily, undone in one command |

```
ask → design → plan → build test-first → check → hand off → remember
```

## How it works

makeit works at three levels:

- **Your machine.** `makeit setup` installs Claude Code, the plugin set, the settings and the
  global rules every session follows: verify before saying "done", test edges and errors, keep
  changes small, ask before anything irreversible. It shows its plan and asks once:

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

- **Each project.** `/makeit:init` in Claude Code writes the files Claude reads at session start
  (`CLAUDE.md`, `CONSTRAINTS.md`, `HANDOFF.md`), wires `check:fast`, `check:task` and
  `check:full` to your real linter and tests, adds commit hooks and a CI workflow, and indexes the
  docs of your dependencies. Claude fills in the project's facts, runs the checks, and asks before
  the first commit.
- **Every session.** Claude reads the handoff, searches your dependencies' docs instead of
  guessing, runs the checks before calling work done, and asks you in one keystroke when it needs
  a decision. Once a day makeit moves everything to its newest release.

[Working with makeit every day](docs/daily.md) walks through a full day: sessions, reviews,
lookups, notifications, and what to do when something goes wrong.

## Built to be trusted

- Every change is shown as a plan first and asks once; `--dry-run` writes nothing.
- A file or setting you changed is never overwritten. makeit updates only what it wrote and you
  never touched, and says what it kept.
- Releases are signed with the maintainer's [minisign](https://jedisct1.github.io/minisign/) key
  and are immutable once published; the installer checks the checksums, and the signature when
  minisign is installed. The key is in [minisign.pub](minisign.pub) and in [Security](#security).
- `makeit rollback` undoes the last update: files, plugin versions and makeit itself.
- `makeit uninstall` removes what makeit added and you did not change.
- Each release passes its own suite on Ubuntu, Debian, Fedora and Alpine before it ships.

## What's inside

| Part | What it does |
|---|---|
| makeit plugin | session hooks (handoff, docs index, reply check, daily update), the `/makeit:init` skill |
| superpowers | the working method: brainstorming, design, plans, test-driven development, debugging, review |
| ponytail | keeps changes small |
| chrome-devtools-mcp, docs-mcp-server | a browser to check web work, and search over your dependencies' docs |
| deja-vu, security-audit | memory across sessions, and security review |
| four LSP plugins | code intelligence for TypeScript, Python, Rust and Go |
| impeccable (web projects) | design skills for user interfaces |

Each part, what it does in a session, and the measurement that kept it: [What's included](docs/included.md).

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

> makeit is in beta (0.x): commands and files may still change between versions.

## Security

Releases are signed with the maintainer's minisign key. Published releases cannot be changed, and
only the maintainer can create version tags. The release key, also in [minisign.pub](minisign.pub):

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
