package catalog

import (
	"slices"
	"testing"
)

func TestListEmbeddedAndTree(t *testing.T) {
	emb, err := List(Embedded, "templates/global/")
	if err != nil || !slices.Contains(emb, "templates/global/CLAUDE.md") {
		t.Errorf("Embedded: %v %v", emb, err)
	}
	tree := &Tree{Files: map[string][]byte{"templates/global/b.md": nil, "templates/global/a.md": nil, "templates/project/c.md": nil}}
	if got, err := List(tree, "templates/global/"); err != nil || !slices.Equal(got, []string{"templates/global/a.md", "templates/global/b.md"}) {
		t.Errorf("Tree: %v %v", got, err)
	}
	if _, err := List(Dir("x"), "templates/global/"); err == nil {
		t.Error("a directory catalog listed, want an error naming it")
	}
}
