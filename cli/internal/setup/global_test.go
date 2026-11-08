package setup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"makeit/internal/catalog"
	"makeit/internal/fsx"
	"makeit/internal/plan"
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
	// fsx.WriteAtomic sets the mode exactly (File.Chmod ignores umask), so a new file is created
	// owner-only: never more open than the user's umask would allow.
	if fi, err := os.Stat(filepath.Join(e.Roots.Claude, "rules", "go.md")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("new global file mode %v (%v), want 0600", fi.Mode().Perm(), err)
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

// A global file that was there before makeit but is byte-identical to makeit's version is makeit's
// from then on, in setup and in update, so the next release reaches it. One byte off stays yours.
func TestGlobalEqualToMakeitsIsTakenOver(t *testing.T) {
	want, err := catalog.Embedded.ReadFile("templates/global/CLAUDE.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		steps func(e *env) []plan.Step
		rec   *state.File
	}{
		{"setup, no record yet", func(e *env) []plan.Step { return e.globalSteps() }, nil},
		{"update, recorded as yours", func(e *env) []plan.Step { return e.updateGlobalSteps() }, &state.File{Class: "owned", Source: "templates/global/CLAUDE.md", PreExisted: true}},
	} {
		e := globalEnv(t)
		path := filepath.Join(e.Roots.Claude, "CLAUDE.md")
		writeFile(t, path, string(want))
		key := e.Roots.Portable(path)
		if tc.rec != nil {
			e.st.Files = map[string]*state.File{key: tc.rec}
		}
		if done, err := step(t, tc.steps(e), "global.CLAUDE.md").Already(context.Background()); !done || err != nil {
			t.Fatalf("%s: already %v %v", tc.name, done, err)
		}
		// Still there-before-makeit (uninstall leaves it), and now with makeit's sha (updates reach it).
		if r := e.st.Files[key]; r == nil || !r.PreExisted || r.Sha256 != fsx.Sha256(want) {
			t.Errorf("%s: record %+v, want preExisted with makeit's sha", tc.name, r)
		}
		if r := keptReason(e, key); r != "" {
			t.Errorf("%s: kept %q", tc.name, r)
		}
	}
	// The next release: the file is still the text makeit recorded, makeit's version moved on.
	e := globalEnv(t)
	path := filepath.Join(e.Roots.Claude, "CLAUDE.md")
	writeFile(t, path, "makeit's previous text\n")
	key := e.Roots.Portable(path)
	e.st.Files = map[string]*state.File{key: {Class: "owned", Source: "templates/global/CLAUDE.md", PreExisted: true, Sha256: fsx.Sha256([]byte("makeit's previous text\n"))}}
	s := step(t, e.updateGlobalSteps(), "global.CLAUDE.md")
	if done, err := s.Already(context.Background()); done || err != nil {
		t.Fatalf("already %v %v, want the new version written", done, err)
	}
	if _, err := s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != string(want) {
		t.Error("the new version was not written")
	}
	if r := e.st.Files[key]; !r.PreExisted {
		t.Errorf("record %+v lost preExisted: uninstall would delete a file the user had before makeit", r)
	}
}

func TestGlobalDifferentStaysYours(t *testing.T) {
	want, err := catalog.Embedded.ReadFile("templates/global/CLAUDE.md")
	if err != nil {
		t.Fatal(err)
	}
	e := globalEnv(t)
	path := filepath.Join(e.Roots.Claude, "CLAUDE.md")
	writeFile(t, path, string(want)+"\n# mine\n")
	key := e.Roots.Portable(path)
	if done, _ := step(t, e.globalSteps(), "global.CLAUDE.md").Already(context.Background()); !done {
		t.Fatal("setup planned to overwrite a file of yours")
	}
	if r := e.st.Files[key]; r == nil || !r.PreExisted {
		t.Errorf("record %+v, want yours", r)
	}
	if done, _ := step(t, e.updateGlobalSteps(), "global.CLAUDE.md").Already(context.Background()); !done {
		t.Fatal("update planned to overwrite a file of yours")
	}
	if r := keptReason(e, key); r != "yours; the plugin set's version differs" {
		t.Errorf("kept %q", r)
	}
}

// An update with nothing else to do still saves a takeover, or the next release finds the record
// unchanged and leaves the file stuck.
func TestQuietUpdateSavesTakeover(t *testing.T) {
	m := converged(t)
	want, err := catalog.Embedded.ReadFile("templates/global/CLAUDE.md")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.env.Roots.Claude, "CLAUDE.md")
	writeFile(t, path, string(want))
	statePath := filepath.Join(m.env.Roots.Stack, "state.json")
	st, _, err := state.LoadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	key := m.env.Roots.Portable(path)
	st.Files[key] = &state.File{Class: "owned", Source: "templates/global/CLAUDE.md", PreExisted: true}
	if err := state.SaveState(statePath, st); err != nil {
		t.Fatal(err)
	}
	rep, err := Update(context.Background(), m.env, plan.Apply, nil, nil)
	if err != nil || rep.Generation != 0 {
		t.Fatalf("generation %d err %v, want a quiet run", rep.Generation, err)
	}
	after, _, _ := state.LoadState(statePath)
	if r := after.Files[key]; r == nil || r.Sha256 != fsx.Sha256(want) {
		t.Errorf("record after a quiet update %+v: the takeover was not saved", r)
	}
}

// A file holding an older makeit release's text is taken over too, so the update writes the
// current version.
func TestGlobalOlderMakeitVersionTakenOver(t *testing.T) {
	old := "makeit's CLAUDE.md as an earlier release shipped it\n"
	saved := globalHistory
	globalHistory = map[string][]string{"templates/global/CLAUDE.md": {fsx.Sha256([]byte(old))}}
	t.Cleanup(func() { globalHistory = saved })
	e := globalEnv(t)
	path := filepath.Join(e.Roots.Claude, "CLAUDE.md")
	writeFile(t, path, old)
	key := e.Roots.Portable(path)
	e.st.Files = map[string]*state.File{key: {Class: "owned", Source: "templates/global/CLAUDE.md", PreExisted: true}}
	if done, _ := step(t, e.updateGlobalSteps(), "global.CLAUDE.md").Already(context.Background()); done {
		t.Errorf("an older makeit text was kept as yours: %v", e.kept)
	}
}

// A file taken over, which the user then made identical to makeit's newer text, is makeit's again
// (the stale sha would otherwise mark it as theirs).
func TestGlobalStaleShaRetaken(t *testing.T) {
	want, err := catalog.Embedded.ReadFile("templates/global/CLAUDE.md")
	if err != nil {
		t.Fatal(err)
	}
	e := globalEnv(t)
	path := filepath.Join(e.Roots.Claude, "CLAUDE.md")
	writeFile(t, path, string(want))
	key := e.Roots.Portable(path)
	e.st.Files = map[string]*state.File{key: {Class: "owned", Source: "templates/global/CLAUDE.md", PreExisted: true, Sha256: fsx.Sha256([]byte("older"))}}
	if done, _ := step(t, e.updateGlobalSteps(), "global.CLAUDE.md").Already(context.Background()); !done {
		t.Fatal("planned a write over makeit's own text")
	}
	if r := e.st.Files[key]; r.Sha256 != fsx.Sha256(want) || keptReason(e, key) != "" {
		t.Errorf("record %+v kept %q", r, keptReason(e, key))
	}
}

// Every makeit version of each global template is known, so an older copy can be taken over.
func TestGlobalHistoryIsCurrent(t *testing.T) {
	repo, _ := filepath.Abs("../../..")
	if out, err := exec.Command("git", "-C", repo, "rev-parse", "--is-shallow-repository").Output(); err != nil || strings.TrimSpace(string(out)) != "false" {
		t.Skip("no full git history here")
	}
	got, err := exec.Command("go", "run", "../../tools/globalhistory", "-repo", repo).Output()
	if err != nil {
		t.Fatal(err)
	}
	have, err := os.ReadFile("globalhistory_gen.go")
	if err != nil || string(have) != string(got) {
		t.Fatalf("globalhistory_gen.go is stale: run go generate ./... (%v)", err)
	}
}
