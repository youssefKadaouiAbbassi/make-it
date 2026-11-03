package setup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"makeit/internal/catalog"
	"makeit/internal/fsx"
	"makeit/internal/plan"
	"makeit/internal/state"
)

// genWithWrite records a generation that wrote "new" over "old" at path.
func genWithWrite(t *testing.T, e *env, path string) *state.Generation {
	t.Helper()
	g, err := state.NewGeneration(e.Roots.Stack, 0, "update", nil)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := g.PreImage([]byte("old"))
	if err != nil {
		t.Fatal(err)
	}
	pre, post := fsx.Sha256([]byte("old")), fsx.Sha256([]byte("new"))
	if err := g.Record(state.Op{ID: "settings", Kind: "write", Path: e.Roots.Portable(path), PreSha256: &pre, PostSha256: &post, PreImage: rel}); err != nil {
		t.Fatal(err)
	}
	return g
}

func runAll(t *testing.T, steps []plan.Step) plan.Report {
	t.Helper()
	return plan.Execute(context.Background(), steps, plan.Options{})
}

func TestRollbackRestoresFile(t *testing.T) {
	e, _ := newEnv(t)
	path := filepath.Join(e.Roots.Claude, "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	g := genWithWrite(t, e, path)
	rep := runAll(t, e.rollbackSteps(g))
	if rep.Failed() {
		t.Fatalf("rollback failed: %+v", rep.Outcomes)
	}
	if got, _ := os.ReadFile(path); string(got) != "old" {
		t.Errorf("file %q, want the pre-image", got)
	}
}

func TestRollbackKeepsEditedFile(t *testing.T) {
	e, _ := newEnv(t)
	path := filepath.Join(e.Roots.Claude, "settings.json")
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, []byte("edited by hand"), 0o600)
	g := genWithWrite(t, e, path)
	runAll(t, e.rollbackSteps(g))
	if got, _ := os.ReadFile(path); string(got) != "edited by hand" {
		t.Errorf("an edited file was replaced: %q", got)
	}
	if !slices.ContainsFunc(e.kept, func(k Kept) bool { return strings.Contains(k.Reason, "changed since") }) {
		t.Errorf("kept %+v, want the edited file listed", e.kept)
	}
}

func TestRollbackRestoresPins(t *testing.T) {
	e, f := stackEnv(t, `[{"name":"makeit","source":"directory"}]`, "[]")
	dir := filepath.Join(e.Roots.Stack, "catalog")
	cat := filepath.Join(dir, ".claude-plugin", "marketplace.json")
	tr, err := catalog.TreeOf(map[string][]byte{".claude-plugin/marketplace.json": []byte(`{"name":"makeit","plugins":[{"name":"superpowers","source":{"source":"url","url":"https://github.com/obra/superpowers.git","ref":"v7.1.0","sha":"bbb"}}]}`)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.SyncTree(dir, tr); err != nil {
		t.Fatal(err)
	}
	f.answers["claude plugin marketplace update makeit --json"] = text(ok)
	f.answers["claude plugin update superpowers@makeit --scope user --json"] = text(ok)
	g, _ := state.NewGeneration(e.Roots.Stack, 0, "update", nil)
	g.Manifest.Pins = []state.PinMove{{ID: "superpowers@makeit", FromTag: "v7.0.1", FromSHA: "aaa", ToTag: "v7.1.0", ToSHA: "bbb"}}
	rep := runAll(t, e.rollbackSteps(g))
	if rep.Failed() {
		t.Fatalf("rollback failed: %+v", rep.Outcomes)
	}
	got, _ := os.ReadFile(cat)
	if !strings.Contains(string(got), `"ref":"v7.0.1"`) || !strings.Contains(string(got), `"sha":"aaa"`) {
		t.Errorf("catalog %s, want v7.0.1 at aaa", got)
	}
	// doctor compares the plugin set with makeit's marker: a rollback is makeit's own write.
	if now, err := catalog.DirTree(dir); err != nil || catalog.Digest(now.Manifest) != catalog.ReadMarker(dir).Digest {
		t.Errorf("the plugin set no longer matches its marker after rollback (%v): doctor would warn", err)
	}
	if s := e.st.PluginSkips["superpowers@makeit"]; !slices.Contains(s, "v7.1.0") {
		t.Errorf("skips %v, want v7.1.0", s)
	}
	if !slices.ContainsFunc(f.argvs(), func(a string) bool { return strings.Contains(a, "plugin update superpowers@makeit") }) {
		t.Errorf("calls %v, want the plugin updated to the restored pin", f.argvs())
	}
}

func TestRollbackPicksNewestNotRolledBack(t *testing.T) {
	e, _ := newEnv(t)
	g1, _ := state.NewGeneration(e.Roots.Stack, 0, "update", nil)
	g2, _ := state.NewGeneration(e.Roots.Stack, 1, "update", nil)
	g2.Manifest.RolledBack = true
	_ = g2.Save()
	g3, _ := state.NewGeneration(e.Roots.Stack, 2, "rollback", nil)
	_ = g3
	got, err := lastUndoable(e.Roots.Stack)
	if err != nil || got.Manifest.Generation != g1.Manifest.Generation {
		t.Fatalf("picked %+v, %v; want generation %d", got, err, g1.Manifest.Generation)
	}
}

func TestRollbackNothing(t *testing.T) {
	e, _ := newEnv(t)
	if _, err := lastUndoable(e.Roots.Stack); !errors.Is(err, ErrNothingToRollBack) {
		t.Errorf("err %v, want ErrNothingToRollBack", err)
	}
}

// A profile that is a symlink into a dotfiles repository: setup wrote through the link, so rollback
// restores the target and keeps the link and the file's mode.
func TestRollbackRestoresThroughSymlink(t *testing.T) {
	e, _ := newEnv(t)
	target := filepath.Join(e.Roots.Home, "dotfiles", "bashrc")
	link := filepath.Join(e.Roots.Home, ".bashrc")
	writeFile(t, target, "export A=1\n")
	if err := os.Chmod(target, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	g, err := state.NewGeneration(e.Roots.Stack, 0, "setup", nil)
	if err != nil {
		t.Fatal(err)
	}
	e.gen = g
	if err := e.write("path.profile", link, []byte("export A=1\n# >>> makeit >>>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := g.Save(); err != nil {
		t.Fatal(err)
	}
	runAll(t, e.rollbackSteps(g))
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the link is gone or replaced: %v %v", fi, err)
	}
	b, _ := os.ReadFile(target)
	fi, _ := os.Stat(target)
	if string(b) != "export A=1\n" || fi.Mode().Perm() != 0o644 {
		t.Errorf("target %q mode %v, want the old content at 0644", b, fi.Mode())
	}
}
