package doctor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"makeit/internal/buildinfo"
	"makeit/internal/state"
)

func TestMarketplace(t *testing.T) {
	m, f := healthy(t)
	if got := byID(t, Run(context.Background(), m), "marketplace"); got.Level != OK {
		t.Errorf("listed: marketplace = %+v", got)
	}
	f.answers["plugin marketplace list --json"] = text(`[{"name": "other", "source": "github", "repo": "o/r"}]`)
	if got := byID(t, Run(context.Background(), m), "marketplace"); got.Level != Error || got.Fix != "makeit setup" {
		t.Errorf("missing: marketplace = %+v", got)
	}
}

func TestPlugins(t *testing.T) {
	m, f := healthy(t)
	f.answers["plugin list --json"] = text(`[
	  {"id": "makeit-bundle@makeit", "version": "3.10.0", "enabled": true},
	  {"id": "makeit@makeit", "version": "3.12.1", "enabled": false},
	  {"id": "typescript-lsp@claude-plugins-official", "version": "1.0.0", "enabled": true, "errors": ["dependency typescript not found"]}
	]`)
	checks := Run(context.Background(), m)
	for id, level := range map[string]Level{
		"plugin.makeit-bundle@makeit":                   OK,
		"plugin.makeit@makeit":                          Info,
		"plugin.superpowers@makeit":                     Error,
		"plugin.typescript-lsp@claude-plugins-official": Error,
	} {
		if got := byID(t, checks, id); got.Level != level {
			t.Errorf("%s = %+v, want %s", id, got, level)
		}
	}
	if got := byID(t, checks, "plugin.superpowers@makeit"); got.Fix != "makeit setup" {
		t.Errorf("missing plugin fix = %q", got.Fix)
	}
	if got := byID(t, checks, "plugin.typescript-lsp@claude-plugins-official"); !strings.Contains(got.Message, "dependency typescript not found") {
		t.Errorf("plugin error not shown: %+v", got)
	}
}

func TestListFailure(t *testing.T) {
	m, f := healthy(t)
	f.answers["plugin marketplace list --json"] = text("not json")
	f.answers["plugin list --json"] = text("not json")
	checks := Run(context.Background(), m)
	if got := byID(t, checks, "marketplace"); got.Level != Error {
		t.Errorf("marketplace = %+v, want error", got)
	}
	if got := byID(t, checks, "plugin.list"); got.Level != Error {
		t.Errorf("plugin.list = %+v, want error", got)
	}
	for _, c := range checks {
		if strings.HasPrefix(c.ID, "plugin.") && c.ID != "plugin.list" {
			t.Errorf("%s reported although the list failed", c.ID)
		}
	}
}

func TestSkippedBelowFloor(t *testing.T) {
	m, f := healthy(t)
	f.answers["--version"] = text("2.1.200 (Claude Code)\n")
	write(t, m.SettingsPath, `{}`)
	checks := Run(context.Background(), m)
	for _, id := range []string{"settings.valid", "marketplace", "plugin.makeit-bundle@makeit", "plugin.makeit@makeit",
		"plugin.superpowers@makeit", "plugin.typescript-lsp@claude-plugins-official"} {
		if got := byID(t, checks, id); got.Level != Skipped || !strings.Contains(got.Message, "2.1.200") {
			t.Errorf("%s = %+v, want skipped naming the version", id, got)
		}
	}
	if got := byID(t, checks, "settings.parse"); got.Level == Skipped {
		t.Errorf("settings.parse skipped: %+v", got)
	}
	if got := byID(t, checks, "state"); got.Level == Skipped {
		t.Errorf("state skipped: %+v", got)
	}
}

func TestState(t *testing.T) {
	m, _ := healthy(t)
	if got := byID(t, Run(context.Background(), m), "state"); got.Level != Info || got.Fix != "makeit setup" {
		t.Errorf("missing: state = %+v", got)
	}
	write(t, m.StatePath, `{"schemaVersion": 99, "cliVersion": "9.0.0"}`)
	if got := byID(t, Run(context.Background(), m), "state"); got.Level != Error || !strings.Contains(got.Message, "format 99") {
		t.Errorf("newer format: state = %+v", got)
	}
	if err := os.Remove(m.StatePath); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(m.StatePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := state.SaveState(m.StatePath, &state.State{SchemaVersion: state.SchemaVersion}); err != nil {
		t.Fatal(err)
	}
	if got := byID(t, Run(context.Background(), m), "state"); got.Level != OK || !strings.Contains(got.Message, buildinfo.Version) {
		t.Errorf("recorded: state = %+v", got)
	}
}

// A plugin Claude Code loads in place from its folder runs the folder's version; the listed version
// is a stale label until a plugin update, so doctor shows the one that runs.
func TestPluginInPlaceShowsFolderVersion(t *testing.T) {
	m, f := healthy(t)
	f.answers["plugin list --json"] = text(`[
	  {"id": "makeit-bundle@makeit", "version": "0.1.0", "enabled": true, "readFromFolder": "/c/plugins/makeit-bundle", "folderVersion": "0.2.0"},
	  {"id": "makeit@makeit", "version": "0.1.0", "enabled": true, "readFromFolder": "/c/plugins/makeit"}
	]`)
	checks := Run(context.Background(), m)
	if got := byID(t, checks, "plugin.makeit-bundle@makeit"); got.Message != "makeit-bundle@makeit 0.2.0" {
		t.Errorf("in place with a folder version: %q", got.Message)
	}
	if got := byID(t, checks, "plugin.makeit@makeit"); got.Message != "makeit@makeit 0.1.0" {
		t.Errorf("no folder version: %q, want the listed one", got.Message)
	}
}
