# HANDOFF — makeit 0.4.2 (branch v0.4.2)

## Goal
- Linux machines whose Node is too old for chrome-devtools-mcp (Ubuntu 24.04: 18.19, Debian 12: 18.20)
  get the official Node LTS from nodejs.org in ~/.local (checked, updated, rolled back, uninstalled).
- Speed rules from eval/setup-review/m8-speed in the global CLAUDE.md template (commit 838d32f).

## Done
- Node route built (37a2ece..a15b980); review found 4 Important + 9 Minor
  (.superpowers/sdd/2026-11-10-node-route/findings.md); fix round in progress, suites running.
- Template lines: background for commands past 2 min, check:task while iterating, Sonnet for
  research/mechanical subagents, reviewer criteria in builder briefs, parallel disjoint tasks.

## Next
- Re-review the fix round (scoped), read its diff, run hooks/ci/Go gate/distro suite myself.
- Advisor A/B running (eval/setup-review/m9-advisor): decide keep fable / opus / off.
- Release 0.4.2 (same flow as 0.4.1; owner signs). Owner tries /fast once before any doc line.

## Open
- Owner said answers must stay tied to makeit (2026-11-10).

## Verify
- bash plugins/makeit/hooks/test.sh · bash ci/test.sh · cli/scripts/go.sh sh
  /src/.superpowers/sdd/2026-11-02-makeit-0.2/gate.sh · bash cli/scripts/test-setup-distros.sh

## Do not touch
- ~/.claude, ~/.claude.json, ~/.makeit, ~/.local on the host; never push from claude-stack.
