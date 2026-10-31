package doctor

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"makeit/internal/execx"
)

func TestSettingsMissing(t *testing.T) {
	m, _ := healthy(t)
	m.Catalog = fixtureCatalog(t, `{"model": "claude-opus-5-5"}`)
	checks := Run(context.Background(), m)
	if got := byID(t, checks, "settings.parse"); got.Level != Info {
		t.Errorf("settings.parse = %+v, want info for a missing file", got)
	}
	if got := byID(t, checks, "settings.valid"); got.Level != Skipped {
		t.Errorf("settings.valid = %+v, want skipped", got)
	}
	if got := byID(t, checks, "settings.stack:/model"); got.Level != Warn || !strings.Contains(got.Message, `"claude-opus-5-5"`) || got.Fix != "makeit setup" {
		t.Errorf("settings.stack:/model = %+v", got)
	}
}

func TestSettingsInvalidJSON(t *testing.T) {
	m, _ := healthy(t)
	write(t, m.SettingsPath, "{")
	checks := Run(context.Background(), m)
	if got := byID(t, checks, "settings.parse"); got.Level != Error || !strings.Contains(got.Message, m.SettingsPath) {
		t.Errorf("settings.parse = %+v, want error naming the file", got)
	}
	for _, id := range []string{"settings.valid", "settings.stack"} {
		if got := byID(t, checks, id); got.Level != Skipped {
			t.Errorf("%s = %+v, want skipped", id, got)
		}
	}
}

func TestSettingsValidate(t *testing.T) {
	m, f := healthy(t)
	write(t, m.SettingsPath, `{"permissions": {"allow": "Bash"}}`)
	// claude doctor names the file inside the throwaway CLAUDE_CONFIG_DIR it was given.
	f.answers["doctor"] = func(c execx.Cmd) execx.Result {
		var dir string
		for _, e := range c.Env {
			if v, ok := strings.CutPrefix(e, "CLAUDE_CONFIG_DIR="); ok {
				dir = v
			}
		}
		return execx.Result{Stdout: "Invalid settings\n- " + filepath.Join(dir, "settings.json") +
			" › permissions.allow: Expected array, but received string\n  Suggested fix: Permission rules must be in an array.\n"}
	}
	got := byID(t, Run(context.Background(), m), "settings.valid:permissions.allow")
	if got.Level != Error || got.Fix != "Expected array, but received string" {
		t.Errorf("settings.valid:permissions.allow = %+v", got)
	}
}

func TestSettingsStack(t *testing.T) {
	m, _ := healthy(t)
	m.Catalog = fixtureCatalog(t, `{"model": "claude-opus-5-5", "permissions": {"ask": ["Bash(git push *)"]}}`)
	write(t, m.SettingsPath, `{"model": "claude-sonnet-5-5", "permissions": {"ask": []}}`)
	checks := Run(context.Background(), m)
	if got := byID(t, checks, "settings.stack:/model"); got.Level != Info {
		t.Errorf("settings.stack:/model = %+v, want info: your value is kept", got)
	}
	if got := byID(t, checks, "settings.stack:/permissions/ask"); got.Level != Warn || !strings.Contains(got.Message, "Bash(git push *)") {
		t.Errorf("settings.stack:/permissions/ask = %+v, want warn naming the missing rule", got)
	}
}

func TestSettingsMatchTheStack(t *testing.T) {
	m, _ := healthy(t)
	m.Catalog = fixtureCatalog(t, `{"model": "claude-opus-5-5"}`)
	write(t, m.SettingsPath, `{"model": "claude-opus-5-5"}`)
	if got := byID(t, Run(context.Background(), m), "settings.stack"); got.Level != OK {
		t.Errorf("settings.stack = %+v, want ok", got)
	}
}

// The baseline's entry for the plugin set's own marketplace is for a git-hosted catalog; the
// embedded catalog is a directory marketplace, which never auto-updates (tools/applysettings).
func TestStackSkipsTheCatalogsOwnMarketplace(t *testing.T) {
	m, _ := healthy(t)
	m.Catalog = fixtureCatalog(t, `{"extraKnownMarketplaces": {
	  "makeit": {"source": {"source": "github", "repo": "<org>/makeit"}, "autoUpdate": true},
	  "claude-plugins-official": {"source": {"source": "github", "repo": "anthropics/claude-plugins-official"}, "autoUpdate": true}}}`)
	write(t, m.SettingsPath, `{"extraKnownMarketplaces": {"makeit": {"source": {"source": "directory", "path": "/repo"}}}}`)
	checks := Run(context.Background(), m)
	official := false
	for _, c := range checks {
		if strings.HasPrefix(c.ID, "settings.stack:/extraKnownMarketplaces/makeit") {
			t.Errorf("reported the plugin set's own marketplace entry: %+v", c)
		}
		official = official || strings.HasPrefix(c.ID, "settings.stack:/extraKnownMarketplaces/claude-plugins-official")
	}
	if !official {
		t.Errorf("the other marketplace entries are no longer checked: %v", checks)
	}
}

func TestStackSecretNotPrinted(t *testing.T) {
	m, _ := healthy(t)
	m.Catalog = fixtureCatalog(t, `{"env": {"API_KEY": "s3cret"}}`)
	write(t, m.SettingsPath, `{}`)
	checks := Run(context.Background(), m)
	if got := byID(t, checks, "settings.stack:/env/API_KEY"); got.Level != Warn {
		t.Errorf("settings.stack:/env/API_KEY = %+v, want warn", got)
	}
	for _, c := range checks {
		if strings.Contains(c.Message, "s3cret") || strings.Contains(c.Fix, "s3cret") {
			t.Errorf("%s prints the secret: %+v", c.ID, c)
		}
	}
}
