package initrepo

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"makeit/internal/catalog"
	"makeit/internal/plan"
	"makeit/internal/setup"
	"makeit/internal/state"
)

// overlay is the embedded catalog with some files replaced.
type overlay map[string]string

func (o overlay) ReadFile(p string) ([]byte, error) {
	if b, ok := o[p]; ok {
		return []byte(b), nil
	}
	return catalog.Embedded.ReadFile(p)
}

// Review Focus 3: --update rewrites a file init wrote and nobody changed; an edited file is kept,
// a deleted one is not written again, and a file that was the user's from the start is never
// touched.
func TestUpdateEditedAndDeleted(t *testing.T) {
	dir := nodeRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "CONSTRAINTS.md"), []byte("theirs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e, _ := testEnv(t, dir)
	if _, err := Run(context.Background(), e, plan.Apply, nil, Options{}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "HANDOFF.md"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "TYPED-DECISIONS.md")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	e.Catalog = overlay{
		"templates/project/CLAUDE.md":      "# <project name>\nnew template\n",
		"templates/project/CONSTRAINTS.md": "new constraints\n",
		"templates/project/HANDOFF.md":     "new handoff\n",
	}
	// Without --update a changed template is left as it is.
	if rep, err := Run(context.Background(), e, plan.Apply, nil, Options{}); err != nil || rep.Plan.Failed() {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md")); strings.Contains(string(b), "new template") {
		t.Fatal("updated without --update")
	}
	// The plain run says which unchanged files have a newer version, and how to take it.
	plain, _ := Run(context.Background(), e, plan.DryRunOnly, nil, Options{})
	if !slices.Contains(plain.Notices, "1 file makeit wrote has a newer version in the plugin set (CLAUDE.md): makeit init --update rewrites it") {
		t.Errorf("notices %q", plain.Notices)
	}
	before := loadRepo(t, dir).Files["CLAUDE.md"].WrittenSha256
	rep, err := Run(context.Background(), e, plan.Apply, nil, Options{Update: true})
	if err != nil || rep.Plan.Failed() {
		t.Fatalf("%v %s", err, rep.Plan.Summary())
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md")); string(b) != "# "+filepath.Base(dir)+"\nnew template\n" {
		t.Errorf("CLAUDE.md not updated: %q", b)
	}
	r := loadRepo(t, dir)
	if got := r.Files["CLAUDE.md"].WrittenSha256; got == before || got != sha(t, filepath.Join(dir, "CLAUDE.md")) {
		t.Errorf("record not updated: %s", got)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "HANDOFF.md")); string(b) != "edited\n" {
		t.Errorf("an edited file was overwritten: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "CONSTRAINTS.md")); string(b) != "theirs\n" {
		t.Errorf("a file that was theirs from the start was touched: %q", b)
	}
	if !slices.Contains(rep.Kept, setup.Kept{Item: "HANDOFF.md", Reason: "changed by you: kept, not updated"}) {
		t.Errorf("kept %v", rep.Kept)
	}
	// Deleted after init wrote it: not written again.
	if err := os.Remove(filepath.Join(dir, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	rep, err = Run(context.Background(), e, plan.Apply, nil, Options{Update: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Error("a file the user deleted was written again")
	}
	if !slices.Contains(rep.Kept, setup.Kept{Item: "CLAUDE.md", Reason: "removed by you: not written again"}) {
		t.Errorf("kept %v", rep.Kept)
	}
}

// --update adds what is new in a merge's source, never an entry the user removed, and never
// changes a value the user set.
func TestUpdateMergesAddNew(t *testing.T) {
	dir := nodeRepo(t)
	e, _ := testEnv(t, dir)
	path := filepath.Join(dir, "x.json")
	if err := os.WriteFile(path, []byte("{\n  \"name\": \"n\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ours := `{"a": {"x": 1, "y": 2}}`
	seed := MergeSeed{Path: "x.json", Template: "test", Apply: func(theirs []byte, skip map[string]bool) (Merged, error) {
		return MergeJSON(theirs, []byte(ours), nil, skip)
	}}
	apply := func(o Options) {
		t.Helper()
		prev, _, err := state.LoadRepo(filepath.Join(dir, RecordName))
		if err != nil {
			t.Fatal(err)
		}
		r := &run{Env: e, opts: o, p: Project{Types: []string{"node"}}, prev: prev}
		r.reset()
		if rep := plan.Execute(context.Background(), []plan.Step{r.mergeStep(seed), r.recordStep()}, plan.Options{}); rep.Failed() {
			t.Fatal(rep.Summary())
		}
	}
	apply(Options{})
	if err := os.WriteFile(path, []byte("{\n  \"name\": \"n\",\n  \"a\": {\n    \"x\": 5\n  }\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ours = `{"a": {"x": 1, "y": 2, "z": 3}, "b": true}`
	apply(Options{})
	if b, _ := os.ReadFile(path); strings.Contains(string(b), `"z"`) {
		t.Fatalf("merged again without --update: %s", b)
	}
	apply(Options{Update: true})
	b, _ := os.ReadFile(path)
	want := "{\n  \"name\": \"n\",\n  \"a\": {\n    \"x\": 5,\n    \"z\": 3\n  },\n  \"b\": true\n}\n"
	if string(b) != want {
		t.Errorf("got\n%s\nwant\n%s", b, want)
	}
	if got := loadRepo(t, dir).Files["x.json"].Added; !slices.Equal(got, []string{"/a/x", "/a/y", "/a/z", "/b"}) {
		t.Errorf("record added %v", got)
	}
}

// A merge file init created and the user deleted is not created again by --update.
func TestUpdateMergeRemovedByYou(t *testing.T) {
	dir := nodeRepo(t)
	e, _ := testEnv(t, dir)
	if _, err := Run(context.Background(), e, plan.Apply, nil, Options{}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, ".gitignore")); err != nil {
		t.Fatal(err)
	}
	// As if the stack's source gained a line since: --update has something new to add.
	rec := loadRepo(t, dir)
	rec.Files[".gitignore"].Added = rec.Files[".gitignore"].Added[:2]
	if err := state.SaveRepo(filepath.Join(dir, RecordName), rec); err != nil {
		t.Fatal(err)
	}
	rep, err := Run(context.Background(), e, plan.Apply, nil, Options{Update: true})
	if err != nil || rep.Plan.Failed() {
		t.Fatalf("%v %s", err, rep.Plan.Summary())
	}
	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); !os.IsNotExist(err) {
		t.Errorf(".gitignore written again: %v", err)
	}
	if !slices.Contains(rep.Kept, setup.Kept{Item: ".gitignore", Reason: "removed by you: not written again"}) {
		t.Errorf("kept %v", rep.Kept)
	}
	if r := loadRepo(t, dir).Files[".gitignore"]; r == nil || r.PreExisted {
		t.Errorf("record %+v", r)
	}
}
