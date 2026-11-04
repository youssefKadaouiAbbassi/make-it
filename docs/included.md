# What makeit installs

`makeit setup` puts five things on a machine: prerequisites, Claude Code, the plugin set, settings
and global files. This page lists each part and why it is there. `makeit setup --dry-run` shows
exactly what it would change on your machine.

Anything already installed is kept as yours. setup adds only what is missing, and
`makeit uninstall` never removes what was there before.

## Prerequisites

setup installs every tool below that applies to your machine, by the first route it has: your
package manager, a toolchain already on `PATH` (npm, go, rustup), the vendor's own package
(Chrome), or the project's signed GitHub release, checksum verified, into `~/.local/bin`. The plan
shows each command, `sudo` included, before anything runs. A tool makeit installed and you remove
afterward stays removed; `makeit setup --reinstall <tool>` brings it back. Only a tool with
nothing to install from on this machine (Chrome where neither your package manager nor Google
ships one for it) is left to you, listed with the one line to do it by hand; `makeit doctor` shows
the same and, at a terminal, can run the fix for you.

| tool | how setup installs it | what it is for |
|---|---|---|
| CA certificates | your package manager (Linux only) | TLS for makeit, git and Claude Code's plugin installs |
| git | your package manager | every repository, the HANDOFF hook, the commit hooks |
| Node.js | your package manager | the two MCP servers start with `npx` (chrome-devtools-mcp, docs-mcp-server) |
| jq | your package manager | the hooks read Claude Code's JSON with it |
| gh | your package manager | `makeit init --rulesets` sends GitHub rulesets through the GitHub API |
| Chrome | Homebrew, winget, or Google's own `.deb`/`.rpm` on apt or dnf (x86-64); left to you elsewhere | chrome-devtools-mcp drives an installed Chrome (it supports Chrome and Chrome for Testing, not Chromium) |
| prek | Homebrew, winget, or the newest release binary from github.com/j178/prek | runs the commit hooks in `.pre-commit-config.yaml` |
| deja | Homebrew, or the newest release binary from github.com/vshulcz/deja-vu | the local search binary of the deja-vu plugin |
| grype | Homebrew, winget, or the newest release binary from github.com/anchore/grype | the vulnerability scan, run locally (the CI checks run it too) |
| typescript-language-server | Homebrew, or `npm install -g --prefix ~/.local` | the typescript-lsp plugin runs it for `.ts` and `.js` files |
| pyright | Homebrew, or `npm install -g --prefix ~/.local` | the pyright-lsp plugin runs it for `.py` files |
| rust-analyzer | checked only when `cargo` or `rustup` is on `PATH`: Homebrew, apk, dnf, or `rustup component add` | the rust-analyzer-lsp plugin runs it for `.rs` files |
| gopls | checked only when `go` is on `PATH`: Homebrew, apt, apk, or `go install` into `~/.local/bin` | the gopls-lsp plugin runs it for `.go` files |

### PATH

setup keeps the folders it installs into on `PATH`: `~/.makeit/env` lists them (`~/.local/bin`
always, plus `~/go/bin` or `~/.cargo/bin` when a tool landed there through a toolchain you already
had), and one block, between `# >>> makeit >>>` and `# <<< makeit <<<`, loads it from your shell's
profile (`~/.profile`, `~/.bashrc` and `~/.bash_profile` when you have them, `.zshenv` for zsh,
a fish `conf.d` file for fish). Lines you had in that file stay. `makeit uninstall` removes exactly what it added. A run
that just added the block says to open a new terminal; your current one keeps its own `PATH` until
then.

## Claude Code

setup installs Claude Code when it is missing or older than 2.1.287, and every setup and update
then brings it to the newest release of its channel (`latest`, or `stable` when you set
`autoUpdatesChannel` to it), through the install's own upgrade. The daily run never asks for a
password: where the upgrade needs sudo (apt, dnf, apk), it leaves a line saying `makeit update`
will do it. A native install also updates itself; the next run lists that move too. `makeit
doctor` warns when Claude Code is older than 2.1.310, the version this release was tested with.

The route depends on the machine: Homebrew on macOS, winget on Windows, and on Linux Homebrew when
present, otherwise Anthropic's signed apt, dnf or apk repository. setup checks the repository key's
fingerprint before it adds it (apk: the key's sha256). When sudo is not available for the repository
route, or the machine has none of these package managers, setup uses Anthropic's native installer; a
failed install stops with its error. An existing Claude Code is kept, however it was installed.

## The plugin set

makeit carries a plugin set: a Claude Code plugin marketplace named `makeit` that lists each plugin
at the release (or, for one without releases, the commit) it was tested with. setup copies it to `~/.makeit/catalog`, adds it as a marketplace, adds
Anthropic's `claude-plugins-official` marketplace, and installs the `makeit-bundle` plugin. The
bundle has no content of its own: installing it installs these plugins.

| plugin | version | what it does | tested |
|---|---|---|---|
| makeit | 0.3.3 | makeit's own plugin: the MCP servers, hooks and skills below | its own test suite |
| superpowers | 7.0.1 | skills for a working method: brainstorming, plans, test-driven development, debugging, code review | yes: on 29 hard tasks run twice, 43 of 58 resolved against 38 without it, at lower cost; a tie on a second hard test |
| ponytail | 5.3.2 | keeps changes small: the smallest complete change, no code nobody asked for; set to level `full` | yes: the cheapest and fastest option on the UI tasks, chosen over Caveman |
| chrome-devtools-mcp | 1.12.0 | the browser skills of chrome-devtools-mcp (the server itself runs from the makeit plugin) | yes: compared with other browser tools; the only one with traces, Lighthouse and device emulation |
| docs-mcp-server | 3.4.1 | the docs-search and docs-manage skills (the server itself runs from the makeit plugin) | yes: agents never searched docs on their own (0 calls in 6 runs), so makeit indexes your dependencies and reminds them |
| deja-vu | 0.23.1 | searches your past coding sessions on this machine; needs the `deja` binary | yes: recalled an earlier decision every time, never without it |
| security-audit | commit `c1c8a8c` | a full security audit of a codebase on request, beside Claude Code's `/security-review` | not yet |
| typescript-lsp | from claude-plugins-official | TypeScript and JavaScript language server | not yet |
| pyright-lsp | from claude-plugins-official | Python language server | not yet |
| rust-analyzer-lsp | from claude-plugins-official | Rust language server | not yet |
| gopls-lsp | from claude-plugins-official | Go language server | not yet |

Parts that were tested and lost were left out, among them Caveman, Laya and hallmark.

The versions above are the ones this release was tested with; nothing is held there. Every `makeit
setup` and `makeit update`, and the daily run, moves each plugin to its newest stable release, and
security-audit, which has no releases, to the newest commit of its `main` branch. The two MCP
servers move to their newest npm version the same way, and prek, deja and grype to their newest
release. Each run lists what moved, old and new version side by side, and `makeit rollback` puts
the previous plugin, MCP server and tool versions back (Claude Code only moves forward; a run
that moved nothing else is not one rollback undoes). A tool release that is tagged but not yet
uploaded, or an upgrade that fails, leaves that one item where it is with a line saying why; the
rest of the run goes on. The four language server plugins come from `claude-plugins-official` and
update with it. Each
language server plugin needs its server program on `PATH` (the optional prerequisite rows above).

One more bundle is enabled per project, not per machine:

| plugin | version | what it does | tested |
|---|---|---|---|
| makeit-ui | 0.3.0 | a bundle for projects with a web frontend: it installs impeccable | |
| impeccable | 4.8.0 | design skills for user interfaces | yes: won every blind comparison against seven other design skills |

`makeit init` enables `makeit-ui` in a project's `.claude/settings.json` when it finds a web
frontend. See [Projects](projects.md).

### The makeit plugin

MCP servers, each with its telemetry off:

- `chrome-devtools`: chrome-devtools-mcp 1.12.0, with usage statistics and the CrUX lookup off.
- `docs`: docs-mcp-server 3.4.1, read-only. It serves the docs index that `makeit init` and
  `makeit docs sync` build from a project's dependencies, so Claude can search the docs of the
  versions the project uses.

Hooks:

- HANDOFF: at session start, Claude reads the project's `HANDOFF.md`. Before the context is
  compacted, it is told to rewrite it. When a turn ends after files changed and `HANDOFF.md` is
  older than them, the turn is stopped once so Claude updates it.
- Docs: at session start and after each shell command or file edit, it re-runs
  `makeit docs sync` in the background when a dependency manifest or lockfile changed. At session start it also lists the project's direct
  dependencies and tells Claude to search their docs.
- Skills reminder: a one-line reminder next to each prompt to check the available skills.
- Options: when a turn ends on a question in plain text, the turn is stopped once. A decision the
  work waits on goes to Claude Code's question dialog; suggestions after finished work go to a
  numbered list.
- ntfy: posts a short line to an [ntfy](https://ntfy.sh) topic when Claude finishes or needs you.
  It does nothing until you set `NTFY_TOPIC` (and optionally `NTFY_URL` and `NTFY_TOKEN`) in the
  `env` block of your settings or in your shell. It sends only the repository name and the event.

The hooks need `jq` and do nothing without it.

Four more hooks ship in the plugin but are not turned on: `variant-ask.sh`, `order-note.sh` with
`order-ask.sh`, and `check-task-stop.sh`, under `hooks/optional/`. Each file's header has the
settings block to turn it on by hand.

Skills:

- `/makeit:init`: sets up a project. Only you can start it. See [Projects](projects.md).
- `expand-contract`: Claude uses it before a database schema change that live code reads (rename,
  drop, retype, split): additive changes first, destructive ones last and alone, backfills in
  batches.

## Settings

setup merges these values into `~/.claude/settings.json`. A value you already have is kept, even
when it differs; setup adds only the missing ones. `--dry-run` prints each path with its value.

| setting | value | why |
|---|---|---|
| `permissions.defaultMode` | `bypassPermissions` | Claude runs tools without asking, except for the commands below |
| `skipDangerousModePermissionPrompt` | `true` | skips Claude Code's one-time confirmation for that mode |
| `permissions.ask` | a list of commands | these always ask first: merging pull requests, `DELETE` calls through `gh api`, pushing to `main` or `master`, force pushes, `git reset --hard`, publishing packages and releases (npm, cargo, uv, twine, gh, eas), `gh pr comment`, `rm -rf` on home, root or parent paths, and the DBHub write tools |
| `model` | `opus` | the newest Opus, resolved by Claude Code |
| `advisorModel` | `fable` | the newest Fable as the advisor, resolved by Claude Code |
| `cleanupPeriodDays` | `3650` | keeps session transcripts for ten years instead of deleting them |
| `env.PONYTAIL_DEFAULT_MODE` | `full` | ponytail's level |
| `env.CHROME_DEVTOOLS_MCP_NO_USAGE_STATISTICS`, `env.DOCS_MCP_TELEMETRY`, `env.REDOCLY_TELEMETRY`, `env.FASTLANE_OPT_OUT_USAGE`, `env.MAESTRO_CLI_NO_ANALYTICS` | off | telemetry off for tools the plugins and the CI checks run |
| `extraKnownMarketplaces.claude-plugins-official` | GitHub `anthropics/claude-plugins-official`, `autoUpdate: true` | the marketplace of the four language server plugins |

Read the ask list before you run setup: with `bypassPermissions`, everything not on it runs without
a prompt.

## Global files

setup writes these to `~/.claude/` when they do not exist. An existing file is yours and is kept.

- `CLAUDE.md`: global instructions for every session: do what was asked at the scope asked,
  verify before saying "done", test edges and errors, keep commits small, write `HANDOFF.md`,
  and how to end a reply.
- `rules/github-actions.md`, `rules/go.md`, `rules/python.md`, `rules/rust.md`, `rules/shell.md`,
  `rules/typescript.md`: the tools and conventions per language. Each one loads only when Claude
  touches a matching file.

## Where makeit keeps its own files

makeit keeps its copy of the plugin set, its record of what it installed and the backups of every
file it changed in `~/.makeit` (set `MAKEIT_DIR` to move it). Each run that changes something keeps
a copy of each file before the change; the last five runs are kept.
