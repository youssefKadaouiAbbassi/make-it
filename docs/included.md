# What makeit installs

`makeit setup` puts five things on a machine: a plugin set for Claude Code, a few settings, global
rules, the tools those need, and Claude Code itself when it is missing. This page says what each
one does for you while you work, and what you can change. `makeit setup --dry-run` shows the exact
plan for your machine.

Anything that was already there is yours: setup adds only what is missing, update changes only
what makeit wrote and you never touched, and uninstall removes only that. The rules are in the
[FAQ](faq.md#will-makeit-overwrite-my-changes).

## The plugins

The plugin set is a Claude Code marketplace named `makeit`. setup copies it to `~/.makeit/catalog`,
registers it, and installs one plugin from it, `makeit-bundle`, which has no content of its own:
installing it installs the plugins below. Each one was kept because it changed what Claude
produced in a measured comparison; the ones that lost (Caveman, Laya, hallmark among them) are
not here.

| plugin | what it does in a session | why it is in |
|---|---|---|
| [makeit](../plugins/makeit) | the hooks, MCP servers and skills described below: handoffs between sessions, a docs reminder, the reply check, notifications, `/makeit:init`, the `draft` skill | makeit's own; its own test suite |
| [superpowers](https://github.com/obra/superpowers) | how Claude works: it asks before building something new, writes a design and a plan, builds test-first, debugs from the root cause, reviews before merging | on 29 hard tasks run twice, 43 of 58 resolved against 38 without it, at lower cost |
| [ponytail](https://github.com/DietrichGebert/ponytail) | the smallest change that solves the task, no code nobody asked for; each reply ends with what was skipped | the cheapest and fastest option on the UI tasks; chosen over Caveman |
| [chrome-devtools-mcp](https://github.com/ChromeDevTools/chrome-devtools-mcp) | Claude opens your web app in Chrome, reads the console and the network, takes screenshots, runs Lighthouse | the only browser tool with traces, Lighthouse and device emulation |
| [deja-vu](https://github.com/vshulcz/deja-vu) | Claude searches your past Claude Code sessions on this machine: decisions, fixes, things you already tried | recalled an earlier decision every time, never without it; makeit ships it without the hooks that injected recall on every prompt and tool call (at most 20% of what they added was relevant), and with `DEJA_REPO=off` so it never writes past-session text into your repositories on its own |
| [security-audit](https://github.com/cloudflare/security-audit-skill) | a full security audit of a codebase when you ask for one, beside Claude Code's `/security-review` | not measured |
| typescript-lsp, pyright-lsp, rust-analyzer-lsp, gopls-lsp | code intelligence (definitions, references, errors) for TypeScript, Python, Rust and Go; from Anthropic's [`claude-plugins-official`](https://github.com/anthropics/claude-plugins-official) marketplace | not measured |

The versions makeit was tested with are pinned in the plugin set
(`~/.makeit/catalog/.claude-plugin/marketplace.json`), but nothing is held there: every setup,
`makeit update` and the daily run move each plugin to its newest release, and security-audit,
which has no releases, to the newest commit. `claude plugin list` shows what your machine has;
`makeit rollback` puts the previous versions back. The language server plugins need their server
on `PATH`; setup installs those ([Tools](#tools-on-path)).

One more bundle is turned on per project, not per machine: `makeit-ui`, which installs
**[impeccable](https://github.com/pbakaus/impeccable)**, design skills for user interfaces (it won every blind comparison against seven
other design skills). `makeit init` enables it in a project's `.claude/settings.json` when it
finds a web frontend.

### What the makeit plugin does

One MCP server, with its telemetry off:

- `chrome-devtools`: chrome-devtools-mcp, the browser, in its own profile (`--isolated`, so a
  second session never fights over Chrome's profile lock). setup installs it once under
  `~/.makeit/mcp` and every update keeps it at the pinned version, so a session starts it with
  `node` directly (about 340 ms instead of 760 ms, and no npm download after an update); if the
  local copy is missing it falls back to `npx`.

Hooks, by moment:

- **When a session starts:** Claude reads the project's `HANDOFF.md`, so it knows what the last
  session finished and left open. It gets the list of the project's direct dependencies with a
  reminder to fetch the docs of the version in use before relying on its API. On a branch other
  than the default one the handoff is `HANDOFF.<branch>.md` beside it; after a compaction Claude
  is also pointed at the active plan file. The first start of the day also starts `makeit update --auto`
  in the background, and a start after a daily run that moved something shows one line saying
  what moved. In a project whose setup is behind what `/makeit:init` does today, the first start
  of the day says so in one line.
- **With each prompt:** a one-line reminder to check the available skills, so the working method
  above is applied to continuations, not only to the first task, and, from the deja-vu plugin, one
  line asking Claude to search your past sessions when the prompt may touch something decided,
  fixed or tried before. A one-word prompt gets no skill reminder, and one under four words no search line.
  Neither fires on a finished background job or on a message from another session.
- **Before an edit or a shell command:** when it uses a library whose locked version came out after
  the model's training or has no known date, and nothing this session looked up that library's docs,
  Claude is stopped once, told the library, the version and where its docs live; the retry always
  passes. Works from `package.json`, `go.mod`, Python and Cargo lockfiles, Helm charts and
  Terraform lockfiles. A lookup of a release date asks the public registry once (3 s at most) and
  never sends your code.
- **Before `git push`:** a push that names no branch, from `main`, `master` or a deploy branch of
  the project, asks first and names the branch. If the check itself fails, the push is stopped
  rather than let through, and without `jq` every push asks.
- **When Claude stops:** three checks. If files changed and `HANDOFF.md` is older than them,
  Claude is told once to rewrite it; while a background job still runs, that reminder waits, for
  30 minutes at most. If the reply ends on a question in plain prose, Claude is
  told once to ask properly: a blocking decision in Claude Code's question dialog, suggestions as
  numbered options ([how that looks](daily.md#answering-claude)). And a notification goes to your
  desktop, and to your [ntfy](https://ntfy.sh) topic when one is set; the same when Claude asks
  you something or waits for a permission, with no delay. It sends only the repository name and
  the event, and names how many background tasks still run. [Setting up the phone](using.md#be-told-when-claude-needs-you).
- **Before a long conversation is compacted:** Claude rewrites the handoff first, and the summary
  is asked to keep the open tasks, the plan's path and the last decision.

Every hook of every plugin in the set, with what it does to a session, is listed in
[Hooks](hooks.md). The hooks need `jq` and do nothing without it (the deja-vu hint has a fallback). Four more hooks ship under `hooks/optional/` and
are not turned on; each file's header has the settings block to enable it.

Skills: `/makeit:init` sets up a project ([Projects](projects.md)); only you can start it.
`draft` is used by Claude when it writes text someone else will read (a message, an e-mail, a
comment): plain text ready to paste, in the reader's language, with no file paths or Markdown.
`expand-contract` is used by Claude before a database schema change that live code reads:
additive changes first, destructive ones last and alone, backfills in batches.

## Settings

setup merges these into `~/.claude/settings.json`. A value you already have is kept, even when it
differs. `--dry-run` prints each path with its value.

| setting | value | why |
|---|---|---|
| `permissions.defaultMode` | `bypassPermissions` | Claude runs tools without asking, except for the commands below |
| `skipDangerousModePermissionPrompt` | `true` | skips Claude Code's one-time confirmation for that mode |
| `permissions.ask` | a list of commands | these always ask first: merging pull requests, `DELETE` calls through `gh api`, pushing to `main` or `master`, force pushes, `git reset --hard`, publishing packages and releases (npm, cargo, uv, twine, gh, eas), `gh pr comment`, `rm -rf` on home, root or parent paths, and the DBHub write tools |
| `model` | `opus` | the newest Opus, resolved by Claude Code |
| `cleanupPeriodDays` | `3650` | keeps session transcripts for ten years (deja-vu searches them) |
| `env.PONYTAIL_DEFAULT_MODE` | `full` | ponytail's level |
| `crossSessionInbound` | `accept` | lets sessions receive messages from other sessions |
| `env.CLAUDE_CODE_ENABLE_TODO_TOOLS`, `env.CLAUDE_CODE_SUBAGENT_EFFORT` | `1`, `medium` | the todo tools on, subagents at medium effort |
| `env.CLAUDE_CODE_SUBAGENT_MODEL` | `sonnet` | subagents run on Sonnet unless Claude names another model for one (it does for a design, a plan or a review) |
| `env.CHROME_DEVTOOLS_MCP_NO_USAGE_STATISTICS`, `env.REDOCLY_TELEMETRY`, `env.FASTLANE_OPT_OUT_USAGE`, `env.MAESTRO_CLI_NO_ANALYTICS` | off | telemetry off for the tools the plugins and the CI checks run |
| `extraKnownMarketplaces.claude-plugins-official` | GitHub `anthropics/claude-plugins-official`, `autoUpdate: true` | the marketplace of the four language server plugins |

Read the ask list before you run setup: with `bypassPermissions`, everything not on it runs
without a prompt. To change any of this, edit `settings.json`; makeit never puts a value back that
you changed.

## Global rules

setup writes these into `~/.claude/` when they do not exist, and never over a file of yours:

- `CLAUDE.md`: the rules every session follows. Do what was asked at the scope asked; report a
  problem, fix only when asked; nothing is "done" without the command run and its output; test
  edges and errors; small commits; a question or a small urgent fix skips the design and plan steps; write `HANDOFF.md` at the end of a session; a second session that writes code in the same repository runs in its own worktree (`claude -w <name>`); a wait longer than a minute runs in the background; chrome-devtools for your own local pages and the Chrome extension for sites that need your login; how to end a
  reply so you can answer in one keystroke.
- `rules/go.md`, `python.md`, `rust.md`, `typescript.md`, `shell.md`, `github-actions.md`: the
  tools and conventions per language, loaded only when Claude touches a matching file.

Edit them to taste: they are yours once written, and `makeit update` leaves a changed file alone
(it says so). A project's own `CLAUDE.md` adds to them, it does not replace them.

## Claude Code

setup installs Claude Code when it is missing or older than 2.1.319, by Homebrew on macOS, winget
on Windows, and on Linux Homebrew when present, otherwise Anthropic's signed apt, dnf or apk
repository (the repository key's fingerprint is checked first), or Anthropic's native installer
when none of those can be used. An existing Claude Code is kept, however it was installed.

Every setup and update then brings it to the newest release of its channel (`latest`, or `stable`
when you set `autoUpdatesChannel`). The daily run never asks for a password: where the upgrade
needs sudo, it leaves a line saying `makeit update` will do it. `makeit doctor` warns when Claude
Code is older than 2.1.319, the version this release was tested with.

## Tools on PATH

setup installs the tools the plugins and hooks run, each by the first route your machine has: your
package manager, a toolchain already on `PATH` (npm, go, rustup), the vendor's own package, or the
tool's signed GitHub release, checksum verified, into `~/.local/bin`. The plan shows every command,
`sudo` included, before anything runs. The release binaries and npm tools move to their newest
release on every update.

| tool | how setup installs it | what it is for |
|---|---|---|
| CA certificates | your package manager (Linux only) | TLS for makeit, git and Claude Code's plugin installs |
| git | your package manager | every repository, the HANDOFF hook, the commit hooks |
| [Node.js](https://nodejs.org/) | Homebrew, winget, apk on Alpine; on other Linux the newest LTS from nodejs.org (into `~/.local/share/makeit/node`) when yours is missing or older than 20.19 / 22.12 | installs and runs the chrome-devtools MCP server (`npx` is the fallback) |
| [jq](https://jqlang.github.io/jq/) | your package manager | the hooks read Claude Code's JSON with it |
| [gh](https://cli.github.com/) | your package manager | `makeit init --rulesets` sends GitHub rulesets through the GitHub API |
| [Chrome](https://www.google.com/chrome/) | Homebrew, winget, or Google's own `.deb`/`.rpm` on apt or dnf (x86-64); left to you elsewhere | chrome-devtools-mcp drives an installed Chrome (Chrome or Chrome for Testing, not Chromium) |
| [prek](https://github.com/j178/prek) | Homebrew, winget, or the newest release binary from github.com/j178/prek | runs the commit hooks in `.pre-commit-config.yaml` |
| [deja](https://github.com/vshulcz/deja-vu) | Homebrew, or the newest release binary from github.com/vshulcz/deja-vu | the search binary of the deja-vu plugin |
| [grype](https://github.com/anchore/grype) | Homebrew, winget, or the newest release binary from github.com/anchore/grype | the vulnerability scan, locally and in the CI checks |
| [typescript-language-server](https://github.com/typescript-language-server/typescript-language-server) | Homebrew, or `npm install -g --prefix ~/.local` | the typescript-lsp plugin |
| [pyright](https://github.com/microsoft/pyright) | Homebrew, or `npm install -g --prefix ~/.local` | the pyright-lsp plugin |
| [marked](https://github.com/markedjs/marked) | `npm install -g --prefix ~/.local` | the reply check numbers lists the way Claude Code shows them |
| [rust-analyzer](https://rust-analyzer.github.io/) | only when `cargo` or `rustup` is on `PATH`: Homebrew, apk, dnf, or `rustup component add` | the rust-analyzer-lsp plugin |
| [gopls](https://pkg.go.dev/golang.org/x/tools/gopls) | only when `go` is on `PATH`: Homebrew, apt, apk, or `go install` into `~/.local/bin` | the gopls-lsp plugin |

A tool you remove afterwards stays removed; `makeit setup --reinstall <tool>` brings it back. A
tool with no route on your machine (Chrome where neither your package manager nor Google ships
one) is listed with the one line to install it by hand, and `makeit doctor` keeps reminding you.

setup keeps the folders it installs into on `PATH`: `~/.makeit/env` lists them, and one block
between `# >>> makeit >>>` and `# <<< makeit <<<` in your shell profile loads it. Your other lines
stay; uninstall removes exactly that block. A run that just added it says to open a new terminal.

## makeit's own folder

`~/.makeit` (or `MAKEIT_DIR`) holds makeit's copy of the plugin set (`catalog/`), its record of
what it installed and wrote (`state.json`), a copy of every file before each change (`generations/`,
the last five runs: this is what `makeit rollback` restores), the daily run's `update.log`, the
`env` file for `PATH`, and `config.json` when you create it to turn the daily run off.
