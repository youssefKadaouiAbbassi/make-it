package initrepo

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"makeit/internal/catalog"
	"makeit/internal/latest"
	"makeit/internal/plan"
)

func TestPending(t *testing.T) {
	all := []Change{{Version: "0.3.7", ID: "a"}, {Version: "0.3.7", ID: "b"}, {Version: "0.3.8", ID: "c"}}
	ids := func(cs []Change) []string {
		out := []string{}
		for _, c := range cs {
			out = append(out, c.ID)
		}
		return out
	}
	for _, tc := range []struct {
		have string
		want []string
	}{
		{"", []string{"a", "b", "c"}},
		{"0.3.7", []string{"c"}},
		{"0.3.8", []string{}},
		{"0.4.0", []string{}},
	} {
		if got := ids(Pending(all, tc.have)); !slices.Equal(got, tc.want) {
			t.Errorf("Pending(%q) = %v, want %v", tc.have, got, tc.want)
		}
	}
	if got := Newest(all); got != "0.3.8" {
		t.Errorf("Newest = %q, want 0.3.8", got)
	}
	if got := Newest(nil); got != "" {
		t.Errorf("Newest(nil) = %q, want empty", got)
	}
}

func TestChangesListValid(t *testing.T) {
	_, all, err := LoadChanges(catalog.Embedded)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) == 0 {
		t.Fatal("the embedded list has no entries")
	}
	seen := map[string]bool{}
	for i, c := range all {
		if c.ID == "" || c.Prompt == "" || len(c.Files) == 0 || slices.Contains(c.Files, "") {
			t.Errorf("entry %d (%q) lacks an id, a prompt or its files", i, c.ID)
		}
		if seen[c.ID] {
			t.Errorf("id %q listed twice", c.ID)
		}
		seen[c.ID] = true
		if i > 0 && latest.Newer(c.Version, all[i-1].Version) {
			t.Errorf("entry %q (%s) comes after %s: keep the list in version order", c.ID, c.Version, all[i-1].Version)
		}
	}
}

// The list must follow every change to the templates or the init skill.
func TestChangesSourcesCurrent(t *testing.T) {
	repo := filepath.Join("..", "..", "..")
	sources, _, err := LoadChanges(catalog.Dir(repo))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	err = filepath.WalkDir(filepath.Join(repo, "templates", "project"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(repo, p)
		b, err := os.ReadFile(p)
		files[filepath.ToSlash(rel)] = b
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if files[SkillPath], err = os.ReadFile(filepath.Join(repo, filepath.FromSlash(SkillPath))); err != nil {
		t.Fatal(err)
	}
	if got := SourcesHash(files); got != sources {
		t.Fatalf("templates or the init skill changed: add an entry to %s, or set sources to %s when no project needs a change", ChangesPath, got)
	}
}

func TestRunReportsPendingChanges(t *testing.T) {
	dir := nodeRepo(t)
	e, _ := testEnv(t, dir)
	rep, err := Run(context.Background(), e, plan.Apply, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, all, _ := LoadChanges(catalog.Embedded)
	if len(rep.Changes) != len(all) {
		t.Fatalf("a project with no initVersion gets %d changes, want all %d", len(rep.Changes), len(all))
	}
	if _, err := Run(context.Background(), e, plan.Apply, nil, Options{Applied: true}); err != nil {
		t.Fatal(err)
	}
	rep, err = Run(context.Background(), e, plan.Apply, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Changes) != 0 || rep.Changes == nil {
		t.Errorf("after --applied: changes %v, want an empty list", rep.Changes)
	}
}

// Review Focus 2: --applied writes the record only, and a keep line already there is not added again.
func TestRunApplied(t *testing.T) {
	dir := nodeRepo(t)
	e, _ := testEnv(t, dir)
	if _, err := Run(context.Background(), e, plan.Apply, nil, Options{}); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, dir)
	prev := loadRepo(t, dir)
	for range 2 {
		rep, err := Run(context.Background(), e, plan.Apply, nil, Options{Applied: true, Keep: "CONSTRAINTS.md: Performance row"})
		if err != nil {
			t.Fatal(err)
		}
		if len(rep.Plan.Outcomes) != 1 || rep.Plan.Outcomes[0].ID != "record" {
			t.Errorf("--applied ran %v, want the record step only", ids(rep))
		}
	}
	got := loadRepo(t, dir)
	_, all, _ := LoadChanges(catalog.Embedded)
	if got.InitVersion != Newest(all) || !slices.Equal(got.Keep, []string{"CONSTRAINTS.md: Performance row"}) {
		t.Errorf("record initVersion %q keep %q", got.InitVersion, got.Keep)
	}
	if !reflect.DeepEqual(got.Files, prev.Files) {
		t.Errorf("--applied changed the files map")
	}
	after := snapshot(t, dir)
	delete(before, ".makeit.json")
	delete(after, ".makeit.json")
	if !reflect.DeepEqual(before, after) {
		t.Errorf("--applied changed a file other than .makeit.json")
	}
}

// Review Focus 1: no record, nothing written.
func TestRunAppliedNoRecord(t *testing.T) {
	dir := nodeRepo(t)
	e, _ := testEnv(t, dir)
	before := snapshot(t, dir)
	_, err := Run(context.Background(), e, plan.Apply, nil, Options{Applied: true})
	if err == nil || !strings.Contains(err.Error(), "run makeit init first") {
		t.Fatalf("err %v", err)
	}
	if !reflect.DeepEqual(before, snapshot(t, dir)) {
		t.Error("--applied without a record wrote something")
	}
}

// snapshot is every file under dir and its content.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		rel, _ := filepath.Rel(dir, p)
		out[rel] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
