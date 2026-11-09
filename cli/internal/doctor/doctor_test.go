package doctor

import (
	"context"
	"errors"
	"makeit/internal/pathenv"
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
	"makeit/internal/platform"
	"makeit/internal/selfupdate"
	"makeit/internal/state"
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
	write(t, filepath.Join(dir, "makeit.json"), `{"schemaVersion":1,"requires":{"claudeCode":{"hard":"2.1.319","soft":"2.1.325"}},
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
		"--version":                      text("2.1.325 (Claude Code)\n"),
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
	want := []string{"makeit.build", "makeit.receipt", "platform", "prereq.git", "claude.installed", "claude.on-path", "claude.other-installs", "claude.version",
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

// A missing tool's fix is "makeit setup" whenever setup has a route for it here, with the same
// root rule setup applies; only a tool setup would leave to the user keeps its manual line.
func TestPrereqFixFollowsSetupsRoutes(t *testing.T) {
	m, _ := healthy(t)
	m.HasSudo = true
	m.Prereqs = []platform.State{
		{Prereq: findRow("git")}, {Prereq: findRow("gh")}, {Prereq: findRow("chrome")}, {Prereq: findRow("prek")},
		{Prereq: findRow("rust-analyzer"), Via: []string{"rustup", "component", "add", "rust-analyzer"}},
	}
	checks := Run(context.Background(), m)
	for _, id := range []string{"prereq.git", "prereq.gh", "prereq.chrome", "prereq.prek", "prereq.rust-analyzer"} {
		if got := byID(t, checks, id); got.Fix != "makeit setup" || got.Group != "prereqs" {
			t.Errorf("with sudo, %s = %+v, want fix makeit setup", id, got)
		}
	}
	if got := byID(t, checks, "prereq.git"); got.Level != Error {
		t.Errorf("git is required: %+v", got)
	}
	m.HasSudo = false
	checks = Run(context.Background(), m)
	for id, want := range map[string]string{
		"prereq.git":           "makeit setup", // required: setup still tries, and fails, as before
		"prereq.gh":            "https://cli.github.com",
		"prereq.chrome":        "Google Chrome or Chrome for Testing: https://www.google.com/chrome/",
		"prereq.prek":          "makeit setup",
		"prereq.rust-analyzer": "makeit setup",
	} {
		if got := byID(t, checks, id); got.Fix != want {
			t.Errorf("without sudo, %s fix = %q, want %q", id, got.Fix, want)
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
		{"newer than tested", text("2.1.326 (Claude Code)\n"), OK},
		{"exactly the tested version", text("2.1.325 (Claude Code)\n"), OK},
		{"below the tested version", text("2.1.322 (Claude Code)\n"), Warn},
		{"exactly the floor", text("2.1.319 (Claude Code)\n"), Warn},
		{"below the minimum version", text("2.1.200 (Claude Code)\n"), Error},
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

func TestSelfChecks(t *testing.T) {
	ours := selfupdate.Status{Receipt: true, BinPath: "/b/makeit", Ours: true}
	skipping := ours
	skipping.Skipped = "1.3.0"
	cases := []struct {
		name          string
		m             Machine
		level         Level
		msg, fix, skp string
	}{
		{"installed", Machine{Exe: "/b/makeit", Self: ours}, Info, "installed by its script at /b/makeit", "", ""},
		{"no receipt", Machine{Exe: "/b/makeit"}, Info, "no receipt: self update is off for this copy", "/b/makeit self install", ""},
		{"development build", Machine{Exe: "/b/makeit", Dev: true}, Info, "development build: it does not update itself", "", ""},
		{"another copy", Machine{Exe: "/b/makeit", Self: selfupdate.Status{Receipt: true, BinPath: "/o/makeit"}}, Info,
			"the receipt names /o/makeit; this makeit is /b/makeit", "", ""},
		{"unreadable", Machine{Exe: "/b/makeit", Self: selfupdate.Status{Receipt: true, Err: errors.New("bad JSON")}}, Warn,
			"receipt.json: bad JSON", "", ""},
		{"skip", Machine{Exe: "/b/makeit", Self: skipping}, Info, "installed by its script at /b/makeit", "",
			"makeit 1.3.0 is skipped: you rolled back from it"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &report{}
			r.self(c.m)
			got := byID(t, r.checks, "makeit.receipt")
			if got.Level != c.level || got.Message != c.msg || got.Fix != c.fix {
				t.Errorf("receipt check %+v", got)
			}
			skip := slices.IndexFunc(r.checks, func(k Check) bool { return k.ID == "makeit.skip" })
			switch {
			case c.skp == "" && skip >= 0:
				t.Errorf("a skip line: %+v", r.checks[skip])
			case c.skp != "" && (skip < 0 || r.checks[skip].Message != c.skp || r.checks[skip].Fix != "makeit self update --to 1.3.0"):
				t.Errorf("skip line missing or wrong: %+v", r.checks)
			}
		})
	}
}

func TestCatalogSourceCheck(t *testing.T) {
	sha := strings.Repeat("ab", 20)
	latest := "latest"
	git := state.Catalog{Kind: "git", Location: "https://h/cat.git", Requested: &latest, RefKind: "latest", Tag: "v1.0.0", Resolved: sha}
	synced := func(t *testing.T) string {
		dir := filepath.Join(t.TempDir(), "catalog")
		tr, err := catalog.TreeOf(map[string][]byte{"makeit.json": []byte("{}")}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := catalog.SyncTree(dir, tr); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	withGit := []platform.State{{Prereq: platform.Prereq{ID: "git"}, Present: true}}
	cases := []struct {
		name   string
		rec    state.Catalog
		git    bool
		change bool
		level  Level
		msg    string
	}{
		{"embedded", state.Catalog{Kind: "embedded"}, true, false, "", ""},
		{"git", git, true, false, Info, "plugin set git https://h/cat.git at v1.0.0 (latest)"},
		{"git without git", git, false, false, Error, "the plugin set https://h/cat.git needs git: install git"},
		{"changed since the sync", git, true, true, Warn, "changed since makeit wrote it"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := Machine{CatalogRecord: c.rec, CatalogDir: synced(t)}
			if c.git {
				m.Prereqs = withGit
			}
			if c.change {
				if err := os.WriteFile(filepath.Join(m.CatalogDir, "makeit.json"), []byte("{\"x\":1}"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			r := &report{}
			r.catalogSource(m)
			i := slices.IndexFunc(r.checks, func(k Check) bool { return k.ID == "catalog.source" })
			if c.level == "" {
				if i >= 0 {
					t.Errorf("a line for an embedded catalog: %+v", r.checks)
				}
				return
			}
			if i < 0 || r.checks[i].Level != c.level || !strings.Contains(r.checks[i].Message, c.msg) {
				t.Errorf("checks %+v, want %s %q", r.checks, c.level, c.msg)
			}
		})
	}
}

// On a git-catalog machine the source line follows the receipt line.
func TestRunOrderGitCatalog(t *testing.T) {
	m, _ := healthy(t)
	latest := "latest"
	m.CatalogRecord = state.Catalog{Kind: "git", Location: "https://h/cat.git", Requested: &latest, Tag: "v1.0.0", Resolved: strings.Repeat("a", 40)}
	m.Prereqs = append(m.Prereqs, platform.State{Prereq: platform.Prereq{ID: "git"}, Present: true})
	checks := Run(context.Background(), m)
	r := slices.IndexFunc(checks, func(c Check) bool { return c.ID == "makeit.receipt" })
	s := slices.IndexFunc(checks, func(c Check) bool { return c.ID == "catalog.source" })
	if r < 0 || s != r+1 {
		t.Errorf("makeit.receipt at %d, catalog.source at %d", r, s)
	}
}

// A program that go install or rustup put off PATH is not "missing": doctor says where it is and
// how to put that folder on PATH, instead of repeating an install that changes nothing.
func TestPrereqOffPath(t *testing.T) {
	m, _ := healthy(t)
	home := t.TempDir()
	for _, p := range []string{"go/bin/gopls", ".cargo/bin/rust-analyzer", ".local/bin/grype"} {
		f := filepath.Join(home, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	m.Getenv = func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	}
	m.Prereqs = []platform.State{
		{Prereq: platform.Prereq{ID: "gopls", Bins: []string{"gopls"}, Via: []platform.Via{{Bin: "go"}}}, Via: []string{"go", "install", "golang.org/x/tools/gopls@latest"}},
		{Prereq: platform.Prereq{ID: "rust-analyzer", Bins: []string{"rust-analyzer"}, Via: []platform.Via{{Bin: "rustup"}}}},
		{Prereq: platform.Prereq{ID: "grype", Bins: []string{"grype"}}},
		{Prereq: platform.Prereq{ID: "pyright", Bins: []string{"pyright"}, Via: []platform.Via{{Bin: "go"}}}, Via: []string{"npm", "install", "-g", "pyright"}},
	}
	checks := Run(context.Background(), m)
	for id, dir := range map[string]string{"prereq.gopls": filepath.Join(home, "go", "bin"), "prereq.rust-analyzer": filepath.Join(home, ".cargo", "bin"), "prereq.grype": filepath.Join(home, ".local", "bin")} {
		got := byID(t, checks, id)
		if got.Level != Warn || !strings.Contains(got.Message, "is installed in "+dir+", which is not on PATH") || got.Fix != "makeit setup" {
			t.Errorf("%s = %+v", id, got)
		}
	}
	// Not on disk anywhere, with a toolchain route: missing, and setup is the fix.
	if got := byID(t, checks, "prereq.pyright"); !strings.Contains(got.Message, "is missing") || got.Fix != "makeit setup" {
		t.Errorf("a program not on disk anywhere: %+v", got)
	}
}

// The shell profile must load ~/.makeit/env, or nothing makeit installed is on PATH.
func TestDoctorProfileCheck(t *testing.T) {
	m, _ := healthy(t)
	home := t.TempDir()
	m.Getenv = func(k string) string {
		switch k {
		case "HOME":
			return home
		case "SHELL":
			return "/bin/bash"
		}
		return ""
	}
	if got := byID(t, Run(context.Background(), m), "path.profile"); got.Level != Warn || got.Fix != "makeit setup" {
		t.Errorf("no block: %+v", got)
	}
	envPath := filepath.Join(filepath.Dir(m.StatePath), "env")
	block := strings.Join(pathenv.Block(envPath, home), "\n") + "\n"
	if err := os.WriteFile(filepath.Join(home, ".profile"), []byte(block), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := byID(t, Run(context.Background(), m), "path.profile"); got.Level != OK {
		t.Errorf("with the block: %+v", got)
	}
}

// A tool missing here but routed (its package file or release binary applies to this machine)
// gets "makeit setup" as its fix, like every other check setup resolves; one truly left to the
// user (chrome where neither works) keeps its manual line.
func TestPrereqRoutedFixIsSetup(t *testing.T) {
	m, _ := healthy(t)
	m.HasSudo = true
	m.Prereqs = []platform.State{
		{Prereq: findRow("prek")},   // release binary, no package on apt
		{Prereq: findRow("chrome")}, // Google's .deb on apt
	}
	checks := Run(context.Background(), m)
	for _, id := range []string{"prereq.prek", "prereq.chrome"} {
		if got := byID(t, checks, id); got.Fix != "makeit setup" {
			t.Errorf("%s fix = %q, want makeit setup", id, got.Fix)
		}
	}
	m.Platform.PM = platform.APK
	if got := byID(t, Run(context.Background(), m), "prereq.chrome"); got.Fix == "makeit setup" || !strings.Contains(got.Fix, "https://www.google.com/chrome/") {
		t.Errorf("chrome on apk fix = %q, want the manual line", got.Fix)
	}
}

// Right after setup, in the same terminal: a tool in a folder the env file lists, with the
// profile block in place, is on PATH in a new terminal. That is information, not a problem, and
// "makeit setup" would change nothing.
func TestPrereqOffPathLoadsInANewTerminal(t *testing.T) {
	m, _ := healthy(t)
	home := t.TempDir()
	m.StatePath = filepath.Join(home, ".makeit", "state.json")
	m.Getenv = func(k string) string {
		switch k {
		case "HOME":
			return home
		case "SHELL":
			return "/bin/bash"
		}
		return ""
	}
	gopls := filepath.Join(home, "go", "bin", "gopls")
	if err := os.MkdirAll(filepath.Dir(gopls), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gopls, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	m.Prereqs = []platform.State{{Prereq: platform.Prereq{ID: "gopls", Bins: []string{"gopls"}, Via: []platform.Via{{Bin: "go"}}}}}
	// Without the env file and the block: a warning, and setup is the fix.
	if got := byID(t, Run(context.Background(), m), "prereq.gopls"); got.Level != Warn || got.Fix != "makeit setup" {
		t.Errorf("before setup: %+v", got)
	}
	envPath := filepath.Join(home, ".makeit", "env")
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	if err := os.MkdirAll(filepath.Dir(envPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envPath, pathenv.EnvFile(pathenv.Dirs(home, m.Getenv, exists), home), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".profile"), []byte(strings.Join(pathenv.Block(envPath, home), "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := byID(t, Run(context.Background(), m), "prereq.gopls")
	if got.Level != Info || got.Fix != "" || !strings.Contains(got.Message, "on PATH in a new terminal") {
		t.Errorf("after setup, same terminal: %+v", got)
	}
}

// For fish the check wants the fish_add_path lines inside the markers; the markers alone are a
// block with nothing in it.
func TestDoctorProfileCheckFish(t *testing.T) {
	m, _ := healthy(t)
	home := t.TempDir()
	m.Getenv = func(k string) string {
		switch k {
		case "HOME":
			return home
		case "SHELL":
			return "/usr/bin/fish"
		}
		return ""
	}
	confd := filepath.Join(home, ".config", "fish", "conf.d")
	if err := os.MkdirAll(confd, 0o755); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(filepath.Dir(m.StatePath), "env")
	if err := os.WriteFile(filepath.Join(home, ".profile"), []byte(strings.Join(pathenv.Block(envPath, home), "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fishFile := filepath.Join(confd, "makeit.fish")
	if err := os.WriteFile(fishFile, []byte("# >>> makeit >>>\n# <<< makeit <<<\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := byID(t, Run(context.Background(), m), "path.profile"); got.Level != Warn || !strings.Contains(got.Message, "makeit.fish") {
		t.Errorf("markers alone: %+v", got)
	}
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	ps := pathenv.Profiles(home, envPath, "/usr/bin/fish", m.Getenv, exists)
	if err := os.WriteFile(fishFile, []byte(strings.Join(ps[len(ps)-1].Lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := byID(t, Run(context.Background(), m), "path.profile"); got.Level != OK {
		t.Errorf("with the lines: %+v", got)
	}
}

// Doctor's two claude listings run together: a runner that releases each listing only once both
// are in flight; a Run that lists one after the other hits the deadline and reports it.
func TestRunListsConcurrently(t *testing.T) {
	m, f := healthy(t)
	var arrived atomic.Int32
	open := make(chan struct{})
	var once sync.Once
	m.Claude = &claudecli.CLI{Path: nativePath, Run: func(ctx context.Context, c execx.Cmd) (execx.Result, error) {
		key := strings.Join(c.Argv[1:], " ")
		if key == "plugin list --json" || key == "plugin marketplace list --json" {
			if arrived.Add(1) == 2 {
				once.Do(func() { close(open) })
			}
			select {
			case <-open:
			case <-time.After(2 * time.Second):
				return execx.Result{Code: 1, Stderr: "deadline: the listings ran one after the other"}, nil
			}
		}
		return f.run(ctx, c)
	}}
	for _, c := range Run(context.Background(), m) {
		if (c.ID == "plugin.list" || c.ID == "marketplace") && c.Level == Error {
			t.Errorf("%s: %s", c.ID, c.Message)
		}
	}
}

// With a Claude Code below the floor, no listing runs at all: the plugin checks are skipped and
// a listing on an unusable install would only cost time (or a timeout) for nothing.
func TestUnusableClaudeListsNothing(t *testing.T) {
	m, f := healthy(t)
	f.answers["--version"] = text("2.1.100 (Claude Code)\n")
	listings := 0
	m.Claude = &claudecli.CLI{Path: nativePath, Run: func(ctx context.Context, c execx.Cmd) (execx.Result, error) {
		if strings.Contains(strings.Join(c.Argv, " "), "list --json") {
			listings++
		}
		return f.run(ctx, c)
	}}
	Run(context.Background(), m)
	if listings != 0 {
		t.Errorf("%d listings ran on an unusable Claude Code", listings)
	}
}

// findRow is the tool table's row with id.
func findRow(id string) platform.Prereq {
	return platform.Prereqs[slices.IndexFunc(platform.Prereqs, func(r platform.Prereq) bool { return r.ID == id })]
}
