package setup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"makeit/internal/catalog"
	"makeit/internal/state"
)

func globalEnv(t *testing.T) *env {
	t.Helper()
	e, _ := newEnv(t)
	e.Catalog = catalog.Embedded
	g, err := state.NewGeneration(e.Roots.Stack, 0, "setup", nil)
	if err != nil {
		t.Fatal(err)
	}
	e.gen = g
	return e
}

func TestGlobalStepsCoverTheTemplates(t *testing.T) {
	steps := globalEnv(t).globalSteps()
	var ids []string
	for _, s := range steps {
		ids = append(ids, s.ID)
	}
	if len(ids) != 7 || ids[0] != "global.CLAUDE.md" || ids[1] != "global.rules/github-actions.md" {
		t.Errorf("steps %v, want CLAUDE.md and the six rules", ids)
	}
}

func TestGlobalWritesMissing(t *testing.T) {
	e := globalEnv(t)
	s := step(t, e.globalSteps(), "global.rules/go.md")
	if done, _ := s.Already(context.Background()); done {
		t.Fatal("missing file counts as present")
	}
	if !strings.HasPrefix(s.Detail, "+ ") {
		t.Errorf("detail %q, want the template as + lines", s.Detail[:min(len(s.Detail), 40)])
	}
	if _, err := s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	want, _ := catalog.Embedded.ReadFile("templates/global/rules/go.md")
	if read(t, filepath.Join(e.Roots.Claude, "rules", "go.md")) != string(want) {
		t.Error("written file differs from the template")
	}
	if f := e.st.Files["claude:rules/go.md"]; f == nil || f.PreExisted || f.Class != "owned" || f.Sha256 == "" {
		t.Errorf("state %+v", f)
	}
}

func TestGlobalAdoptsDifferent(t *testing.T) {
	e := globalEnv(t)
	path := filepath.Join(e.Roots.Claude, "CLAUDE.md")
	if err := os.MkdirAll(e.Roots.Claude, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if done, err := step(t, e.globalSteps(), "global.CLAUDE.md").Already(context.Background()); !done || err != nil {
		t.Fatalf("already = %v, %v; an existing file is the user's", done, err)
	}
	if read(t, path) != "# mine\n" {
		t.Error("the user's file changed")
	}
	if f := e.st.Files["claude:CLAUDE.md"]; f == nil || !f.PreExisted {
		t.Errorf("state %+v, want adopted (preExisted)", f)
	}
}
