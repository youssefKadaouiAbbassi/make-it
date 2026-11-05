package setup

import (
	"strings"
	"testing"

	"makeit/internal/state"
)

// A tool a newer makeit installed (after a self rollback this binary lacks its row): uninstall
// names it and goes on, instead of panicking (the 2026-11-05 audit).
func TestUninstallUnknownTool(t *testing.T) {
	e, _ := newEnv(t)
	e.st.Prereqs = map[string]*state.Prereq{"newtool": {InstalledBy: "makeit", Route: "release", Sha256: "x"}}
	steps := e.toolSteps()
	if len(steps) != 0 {
		t.Errorf("%d steps for an unknown tool, want none", len(steps))
	}
	if r := keptReason(e, "newtool"); !strings.Contains(r, "another makeit version") {
		t.Errorf("kept %q", r)
	}
}
