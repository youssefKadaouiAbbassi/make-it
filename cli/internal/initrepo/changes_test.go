package initrepo

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"makeit/internal/catalog"
	"makeit/internal/latest"
	"makeit/internal/plan"
	"makeit/internal/state"
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
		if !latest.Newer("0.0.0", c.Version) {
			t.Errorf("entry %q: version %q is not x.y.z", c.ID, c.Version)
		}
		if i > 0 && latest.Newer(c.Version, all[i-1].Version) {
			t.Errorf("entry %q (%s) comes after %s: keep the list in version order", c.ID, c.Version, all[i-1].Version)
		}
	}
}

// The newest entry is never past the release being built: --applied would mark projects past
// every later entry.
func TestChangesNotPastTheRelease(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "plugins", "makeit", ".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pl struct{ Version string }
	if err := json.Unmarshal(b, &pl); err != nil {
		t.Fatal(err)
	}
	_, all, err := LoadChanges(catalog.Embedded)
	if err != nil {
		t.Fatal(err)
	}
	if n := Newest(all); latest.Newer(pl.Version, n) {
		t.Errorf("the newest init change is %s, past the release %s", n, pl.Version)
	}
}

// The list must follow every change to the templates or the init skill.
func TestChangesSourcesCurrent(t *testing.T) {
	repo := filepath.Join("..", "..", "..")
	sources, _, err := LoadChanges(catalog.Dir(repo))
	if err != nil {
		t.Fatal(err)
	}
	// The files git tracks or would track, as synccatalog ships them: an ignored scratch file never counts.
	out, err := exec.Command("git", "-C", repo, "ls-files", "-z", "--cached", "--others", "--exclude-standard", "templates/project").Output()
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, p := range strings.Split(string(out), "\x00") {
		if p == "" {
			continue
		}
		if files[p], err = os.ReadFile(filepath.Join(repo, filepath.FromSlash(p))); err != nil {
			t.Fatal(err)
		}
	}
	if files[SkillPath], err = os.ReadFile(filepath.Join(repo, filepath.FromSlash(SkillPath))); err != nil {
		t.Fatal(err)
	}
	if got := SourcesHash(files); got != sources {
		t.Fatalf("templates or the init skill changed: add an entry to %s, or set sources to %s when no project needs a change", ChangesPath, got)
	}
}

// A first init writes the current templates: the project is born with every change.
func TestRunFreshProjectIsCurrent(t *testing.T) {
	dir := nodeRepo(t)
	e, _ := testEnv(t, dir)
	rep, err := Run(context.Background(), e, plan.Apply, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, all, _ := LoadChanges(catalog.Embedded)
	if len(rep.Changes) != 0 || loadRepo(t, dir).InitVersion != Newest(all) {
		t.Errorf("fresh init: changes %v, initVersion %q, want none and %q", rep.Changes, loadRepo(t, dir).InitVersion, Newest(all))
	}
}

// A record an older makeit wrote has no initVersion and gets every change.
func TestRunReportsPendingChanges(t *testing.T) {
	dir := nodeRepo(t)
	e, _ := testEnv(t, dir)
	if _, err := Run(context.Background(), e, plan.Apply, nil, Options{}); err != nil {
		t.Fatal(err)
	}
	old := loadRepo(t, dir)
	old.InitVersion = ""
	if err := state.SaveRepo(filepath.Join(dir, RecordName), old); err != nil {
		t.Fatal(err)
	}
	rep, err := Run(context.Background(), e, plan.Apply, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, all, _ := LoadChanges(catalog.Embedded)
	if loadRepo(t, dir).InitVersion != "" {
		t.Error("a rerun set initVersion before the changes were applied")
	}
	if len(rep.Changes) != len(all) {
		t.Fatalf("a project with no initVersion gets %d changes, want all %d", len(rep.Changes), len(all))
	}
	if _, err := Run(context.Background(), e, plan.Apply, nil, Options{Applied: newestChange(t)}); err != nil {
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

// --applied writes the record only, and a keep line already there is not added again.
func TestRunApplied(t *testing.T) {
	dir := nodeRepo(t)
	e, _ := testEnv(t, dir)
	if _, err := Run(context.Background(), e, plan.Apply, nil, Options{}); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, dir)
	prev := loadRepo(t, dir)
	for range 2 {
		rep, err := Run(context.Background(), e, plan.Apply, nil, Options{Applied: newestChange(t), Keep: "CONSTRAINTS.md: Performance row"})
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

// No record, nothing written.
func TestRunAppliedNoRecord(t *testing.T) {
	dir := nodeRepo(t)
	e, _ := testEnv(t, dir)
	before := snapshot(t, dir)
	_, err := Run(context.Background(), e, plan.Apply, nil, Options{Applied: newestChange(t)})
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

func newestChange(t *testing.T) string {
	t.Helper()
	_, all, err := LoadChanges(catalog.Embedded)
	if err != nil {
		t.Fatal(err)
	}
	return Newest(all)
}

// A first init in a folder whose docs are already there keeps them as they are: they may be
// older than the templates, so the project gets every change.
func TestRunFirstInitWithOwnDocs(t *testing.T) {
	dir := nodeRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "CONSTRAINTS.md"), []byte("# Constraints\n\n| Migrations | squawk |\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e, _ := testEnv(t, dir)
	rep, err := Run(context.Background(), e, plan.Apply, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, all, _ := LoadChanges(catalog.Embedded)
	if len(rep.Changes) != len(all) || loadRepo(t, dir).InitVersion != "" {
		t.Errorf("first init over own docs: %d changes (want %d), initVersion %q (want empty)", len(rep.Changes), len(all), loadRepo(t, dir).InitVersion)
	}
	if !slices.ContainsFunc(rep.Notices, func(n string) bool { return strings.Contains(n, "/makeit:init") && strings.Contains(n, "init changes") }) {
		t.Errorf("no notice names the pending init changes: %q", rep.Notices)
	}
}

// --applied records the version the skill was given, the one it applied, never a newer one the
// plugin set gained meanwhile.
func TestRunAppliedVersion(t *testing.T) {
	dir := nodeRepo(t)
	e, _ := testEnv(t, dir)
	if _, err := Run(context.Background(), e, plan.Apply, nil, Options{}); err != nil {
		t.Fatal(err)
	}
	_, all, _ := LoadChanges(catalog.Embedded)
	if _, err := Run(context.Background(), e, plan.Apply, nil, Options{Applied: all[0].Version}); err != nil {
		t.Fatal(err)
	}
	if got := loadRepo(t, dir).InitVersion; got != all[0].Version {
		t.Errorf("initVersion %q, want %q", got, all[0].Version)
	}
	for _, v := range []string{"v0.3.7", "9.9.9", "latest"} {
		if _, err := Run(context.Background(), e, plan.Apply, nil, Options{Applied: v}); err == nil || !strings.Contains(err.Error(), "no init change at "+v) {
			t.Errorf("--applied %s: err %v", v, err)
		}
	}
	for _, o := range []Options{{Applied: all[0].Version, Update: true}, {Applied: all[0].Version, Rulesets: true}} {
		if _, err := Run(context.Background(), e, plan.Apply, nil, o); err == nil || !strings.Contains(err.Error(), "--applied runs alone") {
			t.Errorf("%+v: err %v", o, err)
		}
	}
}

// --applied never writes through a symlinked record.
func TestRunAppliedSymlinkedRecord(t *testing.T) {
	dir := nodeRepo(t)
	e, _ := testEnv(t, dir)
	if _, err := Run(context.Background(), e, plan.Apply, nil, Options{}); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "record.json")
	rec := filepath.Join(dir, RecordName)
	if err := os.Rename(rec, target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, rec); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, filepath.Dir(target))
	_, err := Run(context.Background(), e, plan.Apply, nil, Options{Applied: newestChange(t)})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("err %v", err)
	}
	if st, err := os.Lstat(rec); err != nil || st.Mode()&os.ModeSymlink == 0 || !reflect.DeepEqual(before, snapshot(t, filepath.Dir(target))) {
		t.Error("--applied replaced the link or wrote its target")
	}
}

// The setup review's three init changes: deploy ask rules, the verify skill, the task section.
func TestChangesSetupReview(t *testing.T) {
	_, all, err := LoadChanges(catalog.Dir(filepath.Join("..", "..", "..")))
	if err != nil {
		t.Fatal(err)
	}
	at := map[string]string{}
	for _, c := range all {
		at[c.ID] = c.Version
		if c.Version == "0.4.0" && (c.Prompt == "" || len(c.Files) == 0) {
			t.Errorf("init change %q lacks a prompt or files", c.ID)
		}
	}
	for _, id := range []string{"deploy-ask-rules", "verify-skill", "task-start-end"} {
		if at[id] != "0.4.0" {
			t.Errorf("init change %q at %q, want 0.4.0", id, at[id])
		}
	}
}
