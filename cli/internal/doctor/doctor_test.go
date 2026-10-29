package doctor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"makeit/internal/catalog"
	"makeit/internal/claudecli"
	"makeit/internal/execx"
	"makeit/internal/platform"
)

// answer is the fake claude's reply to one argv.
type answer func(execx.Cmd) execx.Result

// fakeRunner answers each argv (joined by spaces, without the binary).
type fakeRunner struct{ answers map[string]answer }

func (f *fakeRunner) run(ctx context.Context, c execx.Cmd) (execx.Result, error) {
	if err := ctx.Err(); err != nil {
		return execx.Result{Code: -1}, err
	}
	if a, ok := f.answers[strings.Join(c.Argv[1:], " ")]; ok {
		return a(c), nil
	}
	return execx.Result{Code: 1, Stderr: "unexpected " + strings.Join(c.Argv[1:], " ")}, nil
}

func text(stdout string) answer {
	return func(execx.Cmd) execx.Result { return execx.Result{Stdout: stdout} }
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fixtureCatalog is a catalog directory with the files doctor reads.
func fixtureCatalog(t *testing.T, baseline string) catalog.Dir {
	t.Helper()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "makeit.json"), `{"schemaVersion":1,"requires":{"claudeCode":{"hard":"2.1.287","soft":"2.1.310"}},
		"marketplace":"makeit","bundle":"makeit-bundle@makeit","projectBundle":"makeit-ui@makeit"}`)
	write(t, filepath.Join(dir, "settings.baseline.json"), baseline)
	write(t, filepath.Join(dir, "plugins", "makeit-bundle", ".claude-plugin", "plugin.json"),
		`{"name":"makeit-bundle","dependencies":["makeit","superpowers","typescript-lsp@claude-plugins-official"]}`)
	return catalog.Dir(dir)
}

const nativePath = "/home/u/.local/bin/claude"

// healthyPlugins is `claude plugin list --json` with the fixture bundle and its dependencies.
const healthyPlugins = `[
  {"id": "makeit-bundle@makeit", "version": "3.10.0", "scope": "user", "enabled": true},
  {"id": "makeit@makeit", "version": "3.12.1", "scope": "user", "enabled": true},
  {"id": "superpowers@makeit", "version": "7.0.1", "scope": "user", "enabled": true},
  {"id": "typescript-lsp@claude-plugins-official", "version": "1.0.0", "scope": "user", "enabled": true}
]`

// healthy is a machine on which every check passes (no settings.json and no state.json are
// info, not problems); tests change one thing.
func healthy(t *testing.T) (Machine, *fakeRunner) {
	t.Helper()
	f := &fakeRunner{answers: map[string]answer{
		"--version":                      text("2.1.311 (Claude Code)\n"),
		"doctor":                         text(""),
		"plugin marketplace list --json": text(`[{"name": "makeit", "source": "directory", "path": "/repo"}]`),
		"plugin list --json":             text(healthyPlugins),
	}}
	dir := t.TempDir()
	return Machine{
		Version: "dev", Target: "linux-x64", CatalogDigest: "sha256:0123456789abcdef0123456789abcdef",
		Platform: platform.Platform{OS: "linux", Arch: "amd64", PM: platform.APT},
		Prereqs: []platform.State{{Prereq: platform.Prereq{ID: "git", Required: true,
			Pkgs: map[platform.PM][]string{platform.APT: {"git"}}}, Present: true}},
		Routes:       []platform.Route{{Name: "native", Argv: []string{"bash", "-c", "curl -fsSL https://claude.ai/install.sh | bash"}}},
		Installs:     []claudecli.Install{{Path: nativePath, Source: "native", OnPath: true}},
		Claude:       &claudecli.CLI{Path: nativePath, Run: f.run},
		Catalog:      fixtureCatalog(t, `{}`),
		SettingsPath: filepath.Join(dir, "claude", "settings.json"),
		StatePath:    filepath.Join(dir, "makeit", "state.json"),
	}, f
}

func byID(t *testing.T, checks []Check, id string) Check {
	t.Helper()
	for _, c := range checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no check %s in %v", id, checks)
	return Check{}
}

func TestRunOrder(t *testing.T) {
	m, _ := healthy(t)
	checks := Run(context.Background(), m)
	want := []string{"makeit.build", "platform", "prereq.git", "claude.installed", "claude.on-path", "claude.other-installs", "claude.version",
		"settings.parse", "settings.valid", "settings.stack", "marketplace", "plugin.makeit-bundle@makeit", "plugin.makeit@makeit",
		"plugin.superpowers@makeit", "plugin.typescript-lsp@claude-plugins-official", "state"}
	if len(checks) != len(want) {
		t.Fatalf("Run = %v, want the ids %v", checks, want)
	}
	for i, id := range want {
		if checks[i].ID != id {
			t.Errorf("check %d is %s, want %s", i, checks[i].ID, id)
		}
	}
	if e, w := Count(checks); e != 0 || w != 0 {
		t.Errorf("healthy machine: %d errors, %d warnings: %v", e, w, checks)
	}
	if b := byID(t, checks, "makeit.build"); b.Level != Info || !strings.Contains(b.Message, "sha256:0123456789ab") || strings.Contains(b.Message, "0123456789abc") {
		t.Errorf("makeit.build = %+v, want the digest cut to 12 hex characters", b)
	}
}

func TestPrereqMissing(t *testing.T) {
	m, _ := healthy(t)
	m.Prereqs = []platform.State{
		{Prereq: platform.Prereq{ID: "git", Required: true, Pkgs: map[platform.PM][]string{platform.APT: {"git"}}}},
		{Prereq: platform.Prereq{ID: "gh", Pkgs: map[platform.PM][]string{platform.APT: {"gh"}}}},
		{Prereq: platform.Prereq{ID: "chrome", Manual: "https://www.google.com/chrome/"}},
	}
	checks := Run(context.Background(), m)
	for id, want := range map[string]Check{
		"prereq.git":    {Level: Error, Fix: "sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y git"},
		"prereq.gh":     {Level: Warn, Fix: "sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y gh"},
		"prereq.chrome": {Level: Warn, Fix: "https://www.google.com/chrome/"},
	} {
		if got := byID(t, checks, id); got.Level != want.Level || got.Fix != want.Fix || got.Group != "prereqs" {
			t.Errorf("%s = %+v, want level %s fix %q", id, got, want.Level, want.Fix)
		}
	}
}

func TestClaudeMissing(t *testing.T) {
	m, _ := healthy(t)
	m.Installs, m.Claude = nil, nil
	checks := Run(context.Background(), m)
	if got := byID(t, checks, "claude.installed"); got.Level != Error || got.Fix != "curl -fsSL https://claude.ai/install.sh | bash" {
		t.Errorf("claude.installed = %+v", got)
	}
	for _, id := range []string{"claude.on-path", "claude.other-installs", "claude.version"} {
		if got := byID(t, checks, id); got.Level != Skipped {
			t.Errorf("%s = %+v, want skipped", id, got)
		}
	}
}

func TestClaudeMissingShowsTheRepositoryScript(t *testing.T) {
	m, _ := healthy(t)
	m.Installs, m.Claude = nil, nil
	m.Routes = platform.ClaudeRoutes(platform.Platform{OS: "linux", PM: platform.APT}, func(string) bool { return false })
	script, _ := platform.RepoScript(platform.APT)
	if got := byID(t, Run(context.Background(), m), "claude.installed"); got.Fix != script {
		t.Errorf("fix = %q, want the apt script setup runs", got.Fix)
	}
}

func TestClaudeVersion(t *testing.T) {
	for _, c := range []struct {
		name  string
		reply answer
		level Level
	}{
		{"newer than tested", text("2.1.311 (Claude Code)\n"), OK},
		{"exactly the tested version", text("2.1.310 (Claude Code)\n"), OK},
		{"below the tested version", text("2.1.300 (Claude Code)\n"), Warn},
		{"exactly the floor", text("2.1.287 (Claude Code)\n"), Warn},
		{"below the floor", text("2.1.200 (Claude Code)\n"), Error},
		{"--version fails", func(execx.Cmd) execx.Result { return execx.Result{Code: 1, Stderr: "boom"} }, Error},
	} {
		t.Run(c.name, func(t *testing.T) {
			m, f := healthy(t)
			f.answers["--version"] = c.reply
			got := byID(t, Run(context.Background(), m), "claude.version")
			if got.Level != c.level {
				t.Errorf("claude.version = %+v, want %s", got, c.level)
			}
			if strings.Contains(c.name, "below") && got.Fix != "claude update" {
				t.Errorf("fix = %q, want claude update for a native install", got.Fix)
			}
		})
	}
}

func TestVersionFixNamesTheCask(t *testing.T) {
	m, f := healthy(t)
	f.answers["--version"] = text("2.1.300 (Claude Code)\n")
	m.Installs = []claudecli.Install{{Path: "/opt/homebrew/bin/claude", Source: "brew", OnPath: true}}
	m.BrewCask = "claude-code@latest"
	if got := byID(t, Run(context.Background(), m), "claude.version"); got.Fix != "brew upgrade claude-code@latest" {
		t.Errorf("fix = %q", got.Fix)
	}
}

func TestOnPathAndOtherInstalls(t *testing.T) {
	m, _ := healthy(t)
	m.Installs = []claudecli.Install{{Path: "/opt/homebrew/bin/claude", Source: "brew"}, {Path: nativePath, Source: "native"}}
	m.Claude.Path = m.Installs[0].Path
	checks := Run(context.Background(), m)
	if got := byID(t, checks, "claude.on-path"); got.Level != Warn || got.Fix != "add /opt/homebrew/bin to PATH" {
		t.Errorf("claude.on-path = %+v", got)
	}
	got := byID(t, checks, "claude.other-installs")
	if got.Level != Warn || !strings.Contains(got.Message, "/opt/homebrew/bin/claude (brew)") || !strings.Contains(got.Message, nativePath+" (native)") ||
		got.Fix != "keep one: https://code.claude.com/docs/en/troubleshoot-install.md#check-for-conflicting-installations" {
		t.Errorf("claude.other-installs = %+v", got)
	}
}
