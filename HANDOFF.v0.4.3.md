# HANDOFF — branch v0.4.3 (makeit)

## Goal
Stop deja (>= 0.24) auto-writing `.deja/memory.jsonl` (past-session text) into git repos.

## Branch and commit
v0.4.3 off main 2394439; see git log.

## Done
- settings.baseline.json env `DEJA_REPO=off` (upstream switch, internal/sources/repo_memory.go
  RepoAutoDisabled; gates autoEvolveRepoMemory from the SessionStart digest and MCP orient).
- TestBaselineSetupReview expects it: RED ("env DEJA_REPO = \"\"") then GREEN.
- Proof in m11-arm:B (deja 0.24.1, offline, redacted history): off -> no .deja; unset -> 12 lines.
  Probe: scratchpad/dejaoff/probe2.sh.
- Versions 0.4.3 (marketplace + 4 plugin.json), CHANGELOG 0.4.3, docs/included.md (docs/hooks.md is generated; left as is).
- Interactive replay m12 (makeit 0.3.11 vs 0.4.2, TUI via pty) running as a background agent;
  report goes to redteam/eval/setup-review/m12-interactive/REPORT.md.

## Next
Suites green (hooks OK, CI OK, GATE OK). Next: distro suite, commit named files,
merge --no-ff, release flow as for 0.4.2 (owner signs).

## Open
Global gitignore rejected in favour of the switch (an ignore would hide deliberate repo notes).

## Verify with
bash plugins/makeit/hooks/test.sh · bash ci/test.sh · cli/scripts/go.sh go test ./internal/catalog

## Do not touch
~/.claude/.credentials.json, ~/.config/claude-eval/*, ~/.minisign, Uni-CE workspace root files.
