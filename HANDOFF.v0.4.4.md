# HANDOFF v0.4.4

Goal: two global CLAUDE.md rules for UI changes, after the IGESA session hid the app's designed
contact cards and only screenshotted the back office (m12 report in
claude-code-build-stack-redteam/eval/setup-review/m12-interactive/REPORT.md).

Branch v0.4.4 from main (132a244), commit 7140a84; gate green on it.

Done:
- templates/global/CLAUDE.md: Scope gains "A change to a screen that exists keeps its look";
  Verification gains before/after screenshots of "every screen the change reaches".
- catalog_test.go TestGlobalRulesUIChanges: RED before the edit, then regenerated catalog.
- CHANGELOG Unreleased entry. globalhistory_gen.go regenerated.
- The earlier skills.sh reword branch was deleted (no measured effect, m12 arm C).

Next: release 0.4.4 when the owner says (template
change = release). Owner's ~/.claude/CLAUDE.md equals the 0.4.3 template, so update applies it.

Open: the rules' effect is unmeasured (sealed replays have no emulator).

Verify with: cd cli && scripts/go.sh sh -c 'gofmt -l internal cmd tools; go vet ./... &&
CGO_ENABLED=1 go test -race ./... -count=1 -timeout 300s'

Do not touch: ~/.claude (until release + owner yes), ~/.config/claude-eval, ~/.minisign; never
push from claude-stack.
