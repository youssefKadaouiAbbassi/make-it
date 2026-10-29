package setup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"makeit/internal/catalog"
	"makeit/internal/fsx"
	"makeit/internal/plan"
	"makeit/internal/state"
)

// updateEnv is globalEnv run as update.
func updateEnv(t *testing.T) *env {
	t.Helper()
	e := globalEnv(t)
	e.command = "update"
	return e
}

// keptReason is the reason recorded for item, or "".
func keptReason(e *env, item string) string {
	for _, k := range e.kept {
		if k.Item == item {
			return k.Reason
		}
	}
	return ""
}

func catalogText(t *testing.T, rel string) string {
	t.Helper()
	b, err := catalog.Embedded.ReadFile("templates/global/" + rel)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func owned(content string) *state.File {
	return &state.File{Class: "owned", Sha256: fsx.Sha256([]byte(content))}
}

func TestUpdateGlobalWritesNew(t *testing.T) {
	e := updateEnv(t)
	s := step(t, e.updateGlobalSteps(), "global.rules/go.md")
	if done, err := s.Already(context.Background()); done || err != nil {
		t.Fatalf("already = %v, %v; a new catalog file must be written", done, err)
	}
	if _, err := s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r := e.st.Files["claude:rules/go.md"]; r == nil || r.PreExisted || r.Sha256 != fsx.Sha256([]byte(catalogText(t, "rules/go.md"))) {
		t.Errorf("record %+v, want owned with the catalog's sha256", r)
	}
}

func TestUpdateGlobalReplacesUnchanged(t *testing.T) {
	e := updateEnv(t)
	path := filepath.Join(e.Roots.Claude, "rules", "go.md")
	writeFile(t, path, "old rules\n")
	e.st.Files = map[string]*state.File{"claude:rules/go.md": owned("old rules\n")}
	s := step(t, e.updateGlobalSteps(), "global.rules/go.md")
	if done, _ := s.Already(context.Background()); done {
		t.Fatal("an unchanged owned file with a newer catalog version counts as current")
	}
	if _, err := s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); got != catalogText(t, "rules/go.md") {
		t.Errorf("file not replaced with the catalog's version")
	}
	ops := e.gen.Manifest.Ops
	if len(ops) != 1 || ops[0].PreImage == "" || read(t, filepath.Join(e.gen.Dir, ops[0].PreImage)) != "old rules\n" {
		t.Errorf("ops %+v, want the old file kept as a pre-image", ops)
	}
	if r := e.st.Files["claude:rules/go.md"]; r.Sha256 != fsx.Sha256([]byte(catalogText(t, "rules/go.md"))) {
		t.Errorf("record %+v, want the new sha256", r)
	}
}

func TestUpdateGlobalKeepsChanged(t *testing.T) {
	e := updateEnv(t)
	path := filepath.Join(e.Roots.Claude, "rules", "go.md")
	writeFile(t, path, "my rules\n")
	e.st.Files = map[string]*state.File{"claude:rules/go.md": owned("old rules\n")}
	if done, err := step(t, e.updateGlobalSteps(), "global.rules/go.md").Already(context.Background()); !done || err != nil {
		t.Fatalf("already = %v, %v; a file the user changed is kept", done, err)
	}
	if read(t, path) != "my rules\n" || keptReason(e, "claude:rules/go.md") != "changed since makeit wrote it" {
		t.Errorf("file %q, kept %q", read(t, path), keptReason(e, "claude:rules/go.md"))
	}
}

func TestUpdateGlobalKeepsAdopted(t *testing.T) {
	e := updateEnv(t)
	path := filepath.Join(e.Roots.Claude, "rules", "go.md")
	writeFile(t, path, "my rules\n")
	writeFile(t, filepath.Join(e.Roots.Claude, "CLAUDE.md"), catalogText(t, "CLAUDE.md"))
	e.st.Files = map[string]*state.File{
		"claude:rules/go.md": {Class: "owned", PreExisted: true},
		"claude:CLAUDE.md":   {Class: "owned", PreExisted: true},
	}
	steps := e.updateGlobalSteps()
	for _, id := range []string{"global.rules/go.md", "global.CLAUDE.md"} {
		if done, err := step(t, steps, id).Already(context.Background()); !done || err != nil {
			t.Errorf("%s already = %v, %v; an adopted file is kept", id, done, err)
		}
	}
	if read(t, path) != "my rules\n" || !strings.Contains(keptReason(e, "claude:rules/go.md"), "yours") {
		t.Errorf("file %q, kept %q", read(t, path), keptReason(e, "claude:rules/go.md"))
	}
	if r := keptReason(e, "claude:CLAUDE.md"); r != "" {
		t.Errorf("an adopted file equal to the catalog was reported: %q", r)
	}
}

func TestUpdateGlobalRemovedByYou(t *testing.T) {
	e := updateEnv(t)
	e.st.Files = map[string]*state.File{"claude:rules/go.md": owned(catalogText(t, "rules/go.md"))}
	if done, err := step(t, e.updateGlobalSteps(), "global.rules/go.md").Already(context.Background()); !done || err != nil {
		t.Fatalf("already = %v, %v; a file the user removed stays removed", done, err)
	}
	if _, err := os.Stat(filepath.Join(e.Roots.Claude, "rules", "go.md")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("removed file came back: %v", err)
	}
	if r := keptReason(e, "claude:rules/go.md"); r != "removed by you; makeit setup puts it back" {
		t.Errorf("kept %q", r)
	}
}

func TestUpdateGlobalDropped(t *testing.T) {
	e := updateEnv(t)
	gone := filepath.Join(e.Roots.Claude, "rules", "old.md")
	mine := filepath.Join(e.Roots.Claude, "rules", "old2.md")
	writeFile(t, gone, "old\n")
	writeFile(t, mine, "edited\n")
	e.st.Files = map[string]*state.File{"claude:rules/old.md": owned("old\n"), "claude:rules/old2.md": owned("old\n")}
	steps := e.updateGlobalSteps()
	s := step(t, steps, "global.drop.rules/old.md")
	if done, _ := s.Already(context.Background()); done {
		t.Fatal("an unchanged owned file the catalog dropped counts as current")
	}
	if _, err := s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(gone); !errors.Is(err, os.ErrNotExist) || e.st.Files["claude:rules/old.md"] != nil {
		t.Errorf("dropped file still there (%v) or still recorded", err)
	}
	if ops := e.gen.Manifest.Ops; len(ops) != 1 || ops[0].Kind != "remove" || ops[0].PostSha256 != nil ||
		read(t, filepath.Join(e.gen.Dir, ops[0].PreImage)) != "old\n" {
		t.Errorf("ops %+v, want a remove with the pre-image", ops)
	}
	if done, _ := step(t, steps, "global.drop.rules/old2.md").Already(context.Background()); !done {
		t.Error("a changed file the catalog dropped would be removed")
	}
	if read(t, mine) != "edited\n" || keptReason(e, "claude:rules/old2.md") != "no longer in the catalog; changed since makeit wrote it" {
		t.Errorf("kept %q", keptReason(e, "claude:rules/old2.md"))
	}
}

func TestUpdateMarketplaceRemovedByYou(t *testing.T) {
	e, f := stackEnv(t, "[]", "[]")
	e.command = "update"
	e.st.Marketplaces = map[string]*state.Marketplace{"makeit": {AddedBy: "makeit", Kind: "directory"}}
	if done, err := step(t, e.stackSteps(), "marketplace").Already(context.Background()); !done || err != nil {
		t.Fatalf("already = %v, %v; a marketplace the user removed stays removed", done, err)
	}
	for _, c := range f.argvs() {
		if strings.Contains(c, "marketplace add") {
			t.Errorf("re-added: %s", c)
		}
	}
	if r := keptReason(e, "marketplace makeit"); r != "removed by you; makeit setup puts it back" {
		t.Errorf("kept %q", r)
	}
}

func TestUpdateBundleRemovedByYou(t *testing.T) {
	e, f := stackEnv(t, "[]", "[]")
	e.command = "update"
	e.st.Plugins = map[string]*state.Plugin{"makeit-bundle@makeit": {InstalledBy: "makeit"}}
	if done, err := step(t, e.stackSteps(), "bundle").Already(context.Background()); !done || err != nil {
		t.Fatalf("already = %v, %v; a bundle the user removed stays removed", done, err)
	}
	for _, c := range f.argvs() {
		if strings.Contains(c, "plugin install") {
			t.Errorf("reinstalled: %s", c)
		}
	}
	if r := keptReason(e, "makeit-bundle@makeit"); r != "removed by you; makeit setup puts it back" {
		t.Errorf("kept %q", r)
	}
}

func TestRemoveKeepsPreImage(t *testing.T) {
	e := globalEnv(t)
	path := filepath.Join(e.Roots.Claude, "x.md")
	writeFile(t, path, "x\n")
	if err := e.remove("file.x", path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("file still there: %v", err)
	}
	ops := e.gen.Manifest.Ops
	if len(ops) != 1 || ops[0].Kind != "remove" || ops[0].PreSha256 == nil || *ops[0].PreSha256 != fsx.Sha256([]byte("x\n")) ||
		ops[0].PostSha256 != nil || read(t, filepath.Join(e.gen.Dir, ops[0].PreImage)) != "x\n" {
		t.Errorf("ops %+v", ops)
	}
}

func TestRemoveRefusesSymlink(t *testing.T) {
	e := globalEnv(t)
	target := filepath.Join(t.TempDir(), "dotfiles-go.md")
	writeFile(t, target, "mine\n")
	link := filepath.Join(e.Roots.Claude, "rules", "go.md")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := e.remove("file.go", link); !errors.Is(err, errSymlink) {
		t.Fatalf("err = %v, want errSymlink", err)
	}
	if _, err := os.Lstat(link); err != nil || read(t, target) != "mine\n" {
		t.Errorf("link or target touched: %v", err)
	}
}

func TestLaterPhaseSkippedAfterFailure(t *testing.T) {
	m := newMachine(t)
	ran := false
	build := func(*env) []phase {
		return []phase{
			{steps: []plan.Step{{ID: "a", Title: "a", Run: func(context.Context) (string, error) { return "", errors.New("boom") }}}},
			{steps: []plan.Step{{ID: "b", Title: "b", Run: func(context.Context) (string, error) { ran = true; return "", nil }}}, keepGoing: true},
		}
	}
	rep, _, err := execute(context.Background(), m.env, "test", plan.Apply, nil, nil, build)
	if err != nil {
		t.Fatal(err)
	}
	if ran || !rep.Plan.Failed() {
		t.Fatalf("ran = %v, failed = %v; a later phase must not start after a failure", ran, rep.Plan.Failed())
	}
	last := rep.Plan.Outcomes[len(rep.Plan.Outcomes)-1]
	if last.ID != "b" || last.Status != plan.Skipped || !strings.Contains(last.Detail, "an earlier step failed") {
		t.Errorf("b = %+v", last)
	}
}
