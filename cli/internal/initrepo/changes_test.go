package initrepo

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"makeit/internal/catalog"
	"makeit/internal/latest"
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
