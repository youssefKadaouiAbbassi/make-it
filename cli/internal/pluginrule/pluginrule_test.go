package pluginrule

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"makeit/internal/catalog"
	"makeit/internal/claudecli"
	"makeit/internal/execx"
	"makeit/internal/jsonx"
)

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBundleIDs(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "plugins/makeit-bundle/.claude-plugin/plugin.json"),
		`{"name": "makeit-bundle", "dependencies": ["superpowers", "typescript-lsp@claude-plugins-official"]}`)
	req := catalog.Requires{Marketplace: "makeit", Bundle: "makeit-bundle@makeit"}
	ids, err := BundleIDs(catalog.Dir(dir), req)
	want := []string{"makeit-bundle@makeit", "superpowers@makeit", "typescript-lsp@claude-plugins-official"}
	if err != nil || !slices.Equal(ids, want) {
		t.Errorf("BundleIDs = %v, %v; want %v", ids, err, want)
	}
	if _, err := BundleIDs(catalog.Dir(t.TempDir()), req); err == nil {
		t.Error("a catalog without the bundle manifest gave no error")
	}
}

func TestReadMarketplace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marketplace.json")
	write(t, path, `{"name": "makeit", "plugins": [
		{"name": "makeit", "source": "./plugins/makeit"},
		{"name": "superpowers", "source": {"source": "github", "repo": "obra/superpowers", "sha": "2ce8aa15d05bd57213e6f746f953aafb7ded7a8b"}},
		{"name": "deja-vu", "source": {"source": "git-subdir", "url": "https://github.com/vshulcz/deja-vu.git", "path": "claude-plugin", "sha": "9ce7c980b9b805c7c4a4f07ffea544a423690eda"}},
		{"name": "floating", "source": {"source": "github", "repo": "o/r"}}]}`)
	got, err := ReadMarketplace(path)
	if err != nil {
		t.Fatal(err)
	}
	for name, sha := range map[string]string{"makeit": "", "superpowers": "2ce8aa15d05bd57213e6f746f953aafb7ded7a8b",
		"deja-vu": "9ce7c980b9b805c7c4a4f07ffea544a423690eda", "floating": ""} {
		if e, ok := got[name]; !ok || e.SHA != sha {
			t.Errorf("%s = %+v, %v; want sha %q", name, e, ok, sha)
		}
	}
	if _, err := ReadMarketplace(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Error("a missing marketplace file gave no error")
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	write(t, bad, `{"plugins": [`)
	if _, err := ReadMarketplace(bad); err == nil {
		t.Error("an invalid marketplace file gave no error")
	}
}

func TestMarketplaceFile(t *testing.T) {
	for _, c := range []struct {
		m    claudecli.Marketplace
		want string
		ok   bool
	}{
		{claudecli.Marketplace{Source: "directory", Path: "/repo"}, filepath.Join("/repo", ".claude-plugin", "marketplace.json"), true},
		{claudecli.Marketplace{Source: "file", Path: "/x/marketplace.json"}, "/x/marketplace.json", true},
		{claudecli.Marketplace{Source: "github", Repo: "anthropics/claude-plugins-official"}, "", false},
	} {
		if got, ok := MarketplaceFile(c.m); got != c.want || ok != c.ok {
			t.Errorf("MarketplaceFile(%+v) = %q, %v; want %q, %v", c.m, got, ok, c.want, c.ok)
		}
	}
}

func settingsOf(t *testing.T, s string) *jsonx.Object {
	t.Helper()
	o, err := jsonx.DecodeObject([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func env(kv ...string) func(string) string {
	return func(k string) string {
		for i := 0; i+1 < len(kv); i += 2 {
			if kv[i] == k {
				return kv[i+1]
			}
		}
		return ""
	}
}

func TestAutoUpdates(t *testing.T) {
	on := `{"extraKnownMarketplaces": {"m": {"autoUpdate": true}, "claude-plugins-official": {"autoUpdate": true}}}`
	off := `{"extraKnownMarketplaces": {"m": {"autoUpdate": false}, "claude-plugins-official": {"autoUpdate": false}}}`
	for _, c := range []struct {
		name, settings, market string
		getenv                 func(string) string
		want                   bool
	}{
		{"set on", on, "m", env(), true},
		{"set off", off, "m", env(), false},
		{"other defaults off", `{}`, "m", env(), false},
		{"official defaults on", `{}`, "claude-plugins-official", env(), true},
		{"official set off", off, "claude-plugins-official", env(), false},
		{"disabled by the environment", on, "m", env("DISABLE_AUTOUPDATER", "1"), false},
		{"disabled by settings env", `{"env": {"DISABLE_UPDATES": "1"}, "extraKnownMarketplaces": {"m": {"autoUpdate": true}}}`, "m", env(), false},
		{"nonessential traffic off", `{}`, "claude-plugins-official", env("CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "1"), false},
		{"forced back on", on, "m", env("DISABLE_AUTOUPDATER", "1", "FORCE_AUTOUPDATE_PLUGINS", "1"), true},
	} {
		if got := AutoUpdates(settingsOf(t, c.settings), c.market, c.getenv); got != c.want {
			t.Errorf("%s: AutoUpdates = %v, want %v", c.name, got, c.want)
		}
	}
	if !AutoUpdates(nil, "claude-plugins-official", nil) {
		t.Error("no settings and no environment: official should default on")
	}
}

func TestClassify(t *testing.T) {
	in := Inputs{
		Plugins: []claudecli.Plugin{
			{ID: "makeit@makeit", ReadFromFolder: "/repo/plugins/makeit"},
			{ID: "superpowers@makeit"},
		},
		Entries: map[string]map[string]Entry{"makeit": {
			"makeit":      {},
			"superpowers": {SHA: "2ce8aa15d05bd57213e6f746f953aafb7ded7a8b"},
		}},
		Settings: settingsOf(t, `{"extraKnownMarketplaces": {"other": {"autoUpdate": false}}}`),
	}
	for id, want := range map[string]Verdict{
		"makeit@makeit":                          {Rule: InPlace, Text: "loads in place from /repo/plugins/makeit"},
		"superpowers@makeit":                     {Rule: Pinned, Pin: "2ce8aa15d05bd57213e6f746f953aafb7ded7a8b", Text: "claude plugin update superpowers@makeit --scope user"},
		"typescript-lsp@claude-plugins-official": {Rule: AutoUpdate, Text: "updated by Claude Code (claude-plugins-official auto-updates)"},
		"x@other":                                {Rule: Command, Text: "claude plugin update x@other --scope user"},
	} {
		if got := in.Classify(id); got != want {
			t.Errorf("Classify(%s) = %+v, want %+v", id, got, want)
		}
	}
}

func TestGather(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	write(t, filepath.Join(repo, ".claude-plugin", "marketplace.json"),
		`{"plugins": [{"name": "superpowers", "source": {"source": "github", "repo": "obra/superpowers", "sha": "abc"}}]}`)
	settingsPath := filepath.Join(home, "settings.json")
	write(t, settingsPath, `{"extraKnownMarketplaces": {"makeit": {"autoUpdate": true}}}`)
	answers := map[string]string{
		"plugin marketplace list --json": `[{"name": "makeit", "source": "directory", "path": "` + repo + `"},
			{"name": "gone", "source": "directory", "path": "` + filepath.Join(home, "missing") + `"},
			{"name": "claude-plugins-official", "source": "github", "repo": "anthropics/claude-plugins-official"}]`,
		"plugin list --json": `[{"id": "makeit@makeit", "readFromFolder": "/r/plugins/makeit"}, {"id": "superpowers@makeit"}]`,
	}
	c := &claudecli.CLI{Path: "claude", Run: func(_ context.Context, cmd execx.Cmd) (execx.Result, error) {
		if out, ok := answers[strings.Join(cmd.Argv[1:], " ")]; ok {
			return execx.Result{Stdout: out}, nil
		}
		return execx.Result{Code: 1, Stderr: "unexpected"}, nil
	}}
	in, err := Gather(context.Background(), c, settingsPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := in.Classify("superpowers@makeit"); got.Rule != Pinned || got.Pin != "abc" {
		t.Errorf("superpowers = %+v, want pinned at abc", got)
	}
	if got := in.Classify("makeit@makeit"); got.Rule != InPlace {
		t.Errorf("makeit = %+v, want in place (readFromFolder)", got)
	}
	if got := in.Classify("other@makeit"); got.Rule != AutoUpdate {
		t.Errorf("other@makeit = %+v, want auto-update from settings", got)
	}
	if _, ok := in.Entries["gone"]; ok {
		t.Error("a marketplace without a readable file got entries")
	}
	if _, err := Gather(context.Background(), c, filepath.Join(home, "absent.json"), nil); err != nil {
		t.Errorf("absent settings.json: %v", err)
	}
}

// Gather's two listings run together: a runner that releases each listing only once both are in
// flight; a Gather that lists one after the other hits the deadline and fails.
func TestGatherListsConcurrently(t *testing.T) {
	var arrived atomic.Int32
	open := make(chan struct{})
	var once sync.Once
	c := &claudecli.CLI{Path: "claude", Run: func(_ context.Context, cmd execx.Cmd) (execx.Result, error) {
		if arrived.Add(1) == 2 {
			once.Do(func() { close(open) })
		}
		select {
		case <-open:
		case <-time.After(2 * time.Second):
			return execx.Result{Code: 1, Stderr: "deadline: the listings ran one after the other"}, nil
		}
		return execx.Result{Stdout: "[]\n"}, nil
	}}
	if _, err := Gather(context.Background(), c, filepath.Join(t.TempDir(), "settings.json"), func(string) string { return "" }); err != nil {
		t.Error(err)
	}
}
