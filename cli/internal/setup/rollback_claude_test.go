package setup

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"makeit/internal/claudecli"
	"makeit/internal/plan"
)

// An undo never installs software: with Claude Code removed, rollback's plan finds it or says it
// is not there, and runs no install (the 2026-11-05 audit).
func TestRollbackDoesNotInstallClaude(t *testing.T) {
	var none []claudecli.Install
	e, f := claudeEnv(t, &none, "apt-get", "sudo", "curl", "bash")
	g := genWithWrite(t, e, filepath.Join(e.Roots.Claude, "settings.json"))
	steps := e.rollbackPlan(g)
	plan.Execute(context.Background(), steps[:1], plan.Options{})
	for _, c := range f.argvs() {
		if strings.Contains(c, "install") || strings.Contains(c, "curl") {
			t.Errorf("rollback ran %q", c)
		}
	}
	if r := keptReason(e, "claude"); !strings.Contains(r, "makeit rollback") {
		t.Errorf("kept %q, want a line naming makeit rollback", r)
	}
}
