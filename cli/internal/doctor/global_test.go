package doctor

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"makeit/internal/catalog"
)

func TestGlobalChecks(t *testing.T) {
	m, _ := healthy(t)
	dir := string(m.Catalog.(catalog.Dir))
	for _, rel := range []string{"CLAUDE.md", "rules/go.md", "rules/python.md"} {
		write(t, filepath.Join(dir, "templates", "global", rel), "# "+rel+"\n")
		m.Globals = append(m.Globals, "templates/global/"+rel)
	}
	m.ClaudeDir = t.TempDir()
	write(t, filepath.Join(m.ClaudeDir, "CLAUDE.md"), "# CLAUDE.md\n")
	write(t, filepath.Join(m.ClaudeDir, "rules", "go.md"), "# my own go rules\n")
	checks := Run(context.Background(), m)
	for id, level := range map[string]Level{"global.CLAUDE.md": OK, "global.rules/go.md": Info, "global.rules/python.md": Warn} {
		if got := byID(t, checks, id); got.Level != level || got.Group != "global" {
			t.Errorf("%s = %+v, want %s", id, got, level)
		}
	}
	if got := byID(t, checks, "global.rules/python.md"); got.Fix != "makeit setup" {
		t.Errorf("missing file fix = %q", got.Fix)
	}
	if got := byID(t, checks, "global.rules/go.md"); !strings.Contains(got.Message, "adopted") {
		t.Errorf("differing file message %q, want it called adopted", got.Message)
	}
	// After plugin.*, before state.
	if checks[len(checks)-1].ID != "state" || checks[len(checks)-2].Group != "global" {
		t.Errorf("order: last two are %s, %s", checks[len(checks)-2].ID, checks[len(checks)-1].ID)
	}
}

func TestMarketplaceAdoptedInfo(t *testing.T) {
	m, _ := healthy(t)
	write(t, m.StatePath, `{"schemaVersion": 1, "marketplaces": {"makeit": {"addedBy": "user"}}}`)
	got := byID(t, Run(context.Background(), m), "marketplace")
	if got.Level != Info || !strings.Contains(got.Message, "adopted") {
		t.Errorf("marketplace = %+v, want info naming it adopted", got)
	}
}
