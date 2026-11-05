package setup

import (
	"context"
	"slices"
	"strings"
	"testing"

	"makeit/internal/catalog"
	"makeit/internal/plan"
)

// catalogWithout is the embedded catalog minus drop, plus add.
func catalogWithout(t *testing.T, drop string, add map[string]string) *catalog.Tree {
	t.Helper()
	tree, err := catalog.EmbeddedTree()
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for k, v := range tree.Files {
		if k != drop {
			files[k] = v
		}
	}
	for k, v := range add {
		files[k] = []byte(v)
	}
	out, err := catalog.TreeOf(files, nil)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func stepIDsOf(steps []plan.Step) []string {
	var ids []string
	for _, s := range steps {
		ids = append(ids, s.ID)
	}
	return ids
}

// A git catalog pinned to a tag from before makeit added a global file lacks it: update still plans
// and applies, without that file (the 2026-11-05 audit).
func TestUpdateGlobalWithAnOlderCatalog(t *testing.T) {
	e, _ := newEnv(t)
	e.Catalog = catalogWithout(t, "templates/global/rules/rust.md", nil)
	steps := e.updateGlobalSteps()
	rep := plan.Execute(context.Background(), steps, plan.Options{DryRun: true})
	if rep.Failed() {
		for _, o := range rep.Outcomes {
			if o.Status == plan.Failed {
				t.Errorf("%s failed: %s", o.ID, o.Detail)
			}
		}
	}
	if ids := stepIDsOf(steps); slices.ContainsFunc(ids, func(id string) bool { return strings.HasSuffix(id, "rules/rust.md") }) {
		t.Errorf("a step for a file the catalog lacks: %v", ids)
	}
}

// A catalog's own extra global file is written too, by setup and by update.
func TestGlobalStepsFollowTheCatalog(t *testing.T) {
	e, _ := newEnv(t)
	e.Catalog = catalogWithout(t, "", map[string]string{"templates/global/rules/zig.md": "# Zig\n"})
	for name, steps := range map[string][]plan.Step{"setup": e.globalSteps(), "update": e.updateGlobalSteps()} {
		if ids := stepIDsOf(steps); !slices.ContainsFunc(ids, func(id string) bool { return strings.HasSuffix(id, "rules/zig.md") }) {
			t.Errorf("%s: no step for the catalog's zig.md: %v", name, ids)
		}
	}
}
