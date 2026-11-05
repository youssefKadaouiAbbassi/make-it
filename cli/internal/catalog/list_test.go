package catalog

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestListDirAndEmbedded(t *testing.T) {
	d := t.TempDir()
	for _, p := range []string{"templates/global/CLAUDE.md", "templates/global/rules/go.md", "templates/project/CLAUDE.md"} {
		if err := os.MkdirAll(filepath.Join(d, filepath.Dir(p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, p), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := List(Dir(d), "templates/global/")
	if want := []string{"templates/global/CLAUDE.md", "templates/global/rules/go.md"}; err != nil || !slices.Equal(got, want) {
		t.Errorf("Dir: %v %v, want %v", got, err, want)
	}
	if got, err := List(Dir(d), "templates/none/"); err != nil || len(got) != 0 {
		t.Errorf("missing prefix: %v %v, want nothing", got, err)
	}
	emb, err := List(Embedded, "templates/global/")
	if err != nil || !slices.Contains(emb, "templates/global/CLAUDE.md") {
		t.Errorf("Embedded: %v %v", emb, err)
	}
}
