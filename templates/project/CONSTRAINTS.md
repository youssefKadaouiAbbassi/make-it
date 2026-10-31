# Constraints

Last reviewed: <YYYY-MM-DD> by @<owner>

Format from agent-skills constraint-driven-development (MIT, addyosmani/agent-skills 0.6.12). The floor-guard CI job reads this file: a weakened row, a removed bullet or a new Exceptions row turns the check red.

## Floor (quality rules always enforced, no setup required)

- No new suppression comments: `@ts-ignore`, `@ts-nocheck`, `eslint-disable`, `biome-ignore`, `# noqa`, `# type: ignore`, `nosemgrep`, `gitleaks:allow`, `betterleaks:allow`
- No unimplemented stubs: `throw new Error("Not implemented")`, `todo!()`, `NotImplementedError`, empty `catch {}`
- No skipped or deleted tests without a reason in the commit message
- No secrets in source
- No change to a checker config, a check task, a workflow or this file without a reason in the PR description (the floor-guard watch list)
- This file does not get weakened to make a change pass

## Check tasks

| task | runs | budget |
|---|---|---|
| `check:fast` | format, lint, types | 10 s |
| `check:task` | `check:fast` plus the tests related to the change | 90 s |
| `check:full` | everything CI runs | unlimited, CI |

## Enforced with numbers

| Dimension | Rule | Checked by | Runs at |
|-----------|------|-----------|---------|
| Types | Zero type errors | `<type checker>` | every edit |
| Lint | Zero errors, zero warnings from our config | `<linter>` | every edit |
| Secrets | No secrets in source | `betterleaks git --pre-commit --redact --staged` (prek), the CI checks' betterleaks job | commit (prek), CI |
| Coverage | Changed lines >= 80% covered | `<coverage tool>` + git diff | task end, CI |
| Mutation | Diff mutation score >= 60% | `<mutation tool>` on the diff | CI |
| Security: code | No high findings | `semgrep scan --config p/default` | CI |
| Security: deps | Nothing at high or above | `grype dir:.` | CI |
| CI config | No findings | `zizmor .github/workflows/` | CI |
| Accessibility | Zero critical or serious | `@axe-core/playwright` in the E2E suite | CI, preview |
| Performance | Budget file passes (LCP <= 2500 ms, CLS <= 0.1, total weight) | `sitespeed.io --budget.configPath budget.json` | preview |
| Migrations | No squawk error-level rule | `squawk` on new migrations | CI |

Every row names the command that produces the verdict. A dimension with a number and no command is an aspiration, not a constraint. Delete the rows that do not apply to this project type; do not leave them with a placeholder command.

## Measured, not yet enforced

| Metric | Today | Direction |
|--------|-------|-----------|
| Project coverage | <n>% | must not fall |
| Bundle size (main) | <n> kB | must not grow |
| p95 latency (`<endpoint>`) | <n> ms | must not grow |

## Service level

| Service | SLO | Window | Alert |
|---|---|---|---|
| `<service>` | 99.9% of requests under 500 ms, non-5xx | 30 days | burn rate 14.4x over 1 h and 6x over 6 h |

## CI budget

Heavy jobs (mutation testing, browser E2E, the restore test, evals) run on <schedule or trigger>; the monthly CI-minutes budget is <n>.

## Exceptions

| ID | Rule | Path | Reason | Owner | Expires |
|----|------|------|--------|-------|---------|
