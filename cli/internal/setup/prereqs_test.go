package setup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"makeit/internal/plan"
	"makeit/internal/platform"
	"makeit/internal/state"
)

func stepByID(t *testing.T, e *env, id string) plan.Step {
	t.Helper()
	for _, s := range e.prereqSteps() {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("no step %s in %v", id, stepIDs(e))
	return plan.Step{}
}

// Every tool is installed by the first route the machine has.
func TestRouteOrder(t *testing.T) {
	core := []string{"apt-get", "sudo", "git", "node", "jq"}
	t.Run("package manager", func(t *testing.T) {
		e, _ := newEnv(t, core...)
		s := stepByID(t, e, "prereq.gh")
		if want := []string{"sudo", "-n", "env", "DEBIAN_FRONTEND=noninteractive", "apt-get", "install", "-y", "gh"}; !slices.Equal(s.Command, want) {
			t.Errorf("command %v", s.Command)
		}
	})
	t.Run("npm into ~/.local, no sudo", func(t *testing.T) {
		e, f := newEnv(t, append(core, "npm")...)
		s := stepByID(t, e, "prereq.typescript-language-server")
		want := []string{"npm", "install", "-g", "--prefix", filepath.Join(e.Roots.Home, ".local"), "typescript-language-server", "typescript"}
		if !slices.Equal(s.Command, want) {
			t.Errorf("command %v", s.Command)
		}
		if _, err := s.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if calls := f.argvs(); len(calls) != 1 || !strings.HasPrefix(calls[0], "npm install") {
			t.Errorf("calls %v, want the npm install alone", calls)
		}
	})
	t.Run("go install with GOBIN", func(t *testing.T) {
		// Fedora has no gopls package, so the toolchain route is the first that applies.
		e, f := newEnv(t, "dnf", "git", "node", "jq", "go")
		e.Platform.PM = platform.DNF
		s := stepByID(t, e, "prereq.gopls")
		if _, err := s.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		c := f.calls[len(f.calls)-1]
		if !slices.Equal(c.Argv, []string{"go", "install", "golang.org/x/tools/gopls@latest"}) || !slices.Contains(c.Env, "GOBIN="+filepath.Join(e.Roots.Home, ".local", "bin")) {
			t.Errorf("cmd %+v", c)
		}
		if rec := e.st.Prereqs["gopls"]; rec == nil || rec.InstalledBy != "makeit" || rec.Route != "go" {
			t.Errorf("record %+v", rec)
		}
	})
	t.Run("Google's package on apt x86-64", func(t *testing.T) {
		e, _ := newEnv(t, core...)
		if s := stepByID(t, e, "prereq.chrome"); !strings.Contains(s.Detail, "Google's .deb") {
			t.Errorf("detail %q", s.Detail)
		}
	})
	t.Run("no route: needs you", func(t *testing.T) {
		e, _ := newEnv(t, "apk", "git", "node", "jq")
		e.Platform.PM = platform.APK
		if ids := stepIDs(e); slices.Contains(ids, "prereq.chrome") {
			t.Errorf("chrome planned without a route: %v", ids)
		}
		i := slices.IndexFunc(e.needsYou, func(n NeedsYou) bool { return n.ID == "chrome" })
		if i < 0 || !strings.Contains(e.needsYou[i].Manual, "https://www.google.com/chrome/") {
			t.Errorf("needs you %+v, want chrome with its URL", e.needsYou)
		}
	})
	t.Run("release binary", func(t *testing.T) {
		e, _ := newEnv(t, core...)
		if s := stepByID(t, e, "prereq.prek"); s.Detail != "release v0.5.9 from github.com/j178/prek" {
			t.Errorf("detail %q", s.Detail)
		}
	})
}

func TestPresentToolGetsNoStep(t *testing.T) {
	e, _ := newEnv(t, "apt-get", "git", "node", "jq", "gh")
	if ids := stepIDs(e); slices.Contains(ids, "prereq.gh") {
		t.Errorf("gh is present and still planned: %v", ids)
	}
}

func tarGzWith(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "prek-dist/" + name, Mode: 0o755, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	_, _ = tw.Write(data)
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func TestReleaseRouteInstalls(t *testing.T) {
	bin := []byte("#!/bin/sh\necho prek\n")
	archive := tarGzWith(t, "prek", bin)
	sum := sha256.Sum256(archive)
	files := map[string][]byte{
		"/j178/prek/releases/download/v0.5.9/prek-x86_64-unknown-linux-musl.tar.gz":        archive,
		"/j178/prek/releases/download/v0.5.9/prek-x86_64-unknown-linux-musl.tar.gz.sha256": []byte(hex.EncodeToString(sum[:]) + "  prek-x86_64-unknown-linux-musl.tar.gz\n"),
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	e, _ := newEnv(t, "apt-get", "git", "node", "jq")
	e.Client, e.ReleaseBase = srv.Client(), srv.URL
	g, err := state.NewGeneration(e.Roots.Stack, 0, "setup", nil)
	if err != nil {
		t.Fatal(err)
	}
	e.gen = g
	s := stepByID(t, e, "prereq.prek")
	if _, err := s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Written through the generation, like every other file setup writes: rollback can undo it.
	if ops := g.Manifest.Ops; len(ops) != 1 || ops[0].Kind != "write" || ops[0].PreSha256 != nil || ops[0].PostSha256 == nil {
		t.Errorf("ops %+v, want one write of a new file", ops)
	}
	path := filepath.Join(e.Roots.Home, ".local", "bin", "prek")
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, bin) {
		t.Fatalf("%s: %v %q", path, err, got)
	}
	fi, _ := os.Stat(path)
	rec := e.st.Prereqs["prek"]
	if fi.Mode().Perm() != 0o755 || rec == nil || rec.Route != "release" || rec.InstalledBy != "makeit" || rec.Sha256 == "" {
		t.Errorf("mode %v record %+v", fi.Mode(), rec)
	}
}

// A tool makeit installed and the user removed stays removed, unless asked for again.
func TestRemovedByYouStaysRemoved(t *testing.T) {
	e, _ := newEnv(t, "apt-get", "sudo", "git", "node", "jq")
	e.st.Prereqs = map[string]*state.Prereq{"gh": {InstalledBy: "makeit", Route: "apt"}}
	if ids := stepIDs(e); slices.Contains(ids, "prereq.gh") {
		t.Errorf("gh planned again: %v", ids)
	}
	if r := keptReason(e, "gh"); r != "removed by you" {
		t.Errorf("kept %q", r)
	}
	e.kept = nil
	e.Reinstall = []string{"gh"}
	if ids := stepIDs(e); !slices.Contains(ids, "prereq.gh") {
		t.Errorf("--reinstall gh did not plan it: %v", ids)
	}
}

// The daily run never installs a tool: that needs a terminal.
func TestBackgroundRunSkipsTools(t *testing.T) {
	e, _ := newEnv(t, "apt-get", "node", "jq")
	e.Background = true
	if ids := stepIDs(e); len(ids) != 0 {
		t.Errorf("background run plans %v", ids)
	}
}

// npm arrives with node, which setup installs first: the npm route applies on a machine where
// node is still to be installed, and its step comes after node's.
func TestNpmRouteWhenNodeIsPlanned(t *testing.T) {
	e, _ := newEnv(t, "apt-get", "sudo", "git", "jq")
	ids := stepIDs(e)
	node, ts := slices.Index(ids, "prereq.node"), slices.Index(ids, "prereq.typescript-language-server")
	if node < 0 || ts < 0 || ts < node {
		t.Fatalf("steps %v: want node before typescript-language-server", ids)
	}
	s := stepByID(t, e, "prereq.typescript-language-server")
	if len(s.Command) == 0 || s.Command[0] != "npm" {
		t.Errorf("command %v, want npm", s.Command)
	}
	if slices.ContainsFunc(e.needsYou, func(n NeedsYou) bool { return n.ID == "typescript-language-server" }) {
		t.Errorf("typescript-language-server left to the user although node is planned: %+v", e.needsYou)
	}
}

// Without sudo (and not root), a tool whose only route needs the package manager is left to the
// user; the routes that need no root still run.
func TestNoSudoLeavesPackageToolsToYou(t *testing.T) {
	e, _ := newEnv(t, "apt-get", "git", "node", "jq", "npm")
	ids := stepIDs(e)
	for _, id := range []string{"prereq.gh", "prereq.chrome"} {
		if slices.Contains(ids, id) {
			t.Errorf("%s planned without sudo: %v", id, ids)
		}
	}
	for _, id := range []string{"gh", "chrome"} {
		if !slices.ContainsFunc(e.needsYou, func(n NeedsYou) bool { return n.ID == id }) {
			t.Errorf("%s not left to the user: %+v", id, e.needsYou)
		}
	}
	for _, id := range []string{"prereq.prek", "prereq.typescript-language-server"} {
		if !slices.Contains(ids, id) {
			t.Errorf("%s not planned (needs no root): %v", id, ids)
		}
	}
	e.Root = true
	e.needsYou = nil
	if ids := stepIDs(e); !slices.Contains(ids, "prereq.gh") {
		t.Errorf("as root, gh not planned: %v", ids)
	}
}

// A tool already on disk in a folder makeit puts on PATH (~/.local/bin, ~/go/bin, ~/.cargo/bin)
// is present even when the shell cannot see it yet: never reinstalled, never "removed by you".
func TestPresentOffPathInMakeitDirs(t *testing.T) {
	e, _ := newEnv(t, "apt-get", "sudo", "git", "node", "jq", "go")
	e.Getenv = func(k string) string {
		if k == "HOME" {
			return e.Roots.Home
		}
		return ""
	}
	for _, p := range []string{".local/bin/prek", "go/bin/gopls"} {
		f := filepath.Join(e.Roots.Home, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e.st.Prereqs = map[string]*state.Prereq{"prek": {InstalledBy: "makeit", Route: "release"}}
	ids := stepIDs(e)
	for _, id := range []string{"prereq.prek", "prereq.gopls"} {
		if slices.Contains(ids, id) {
			t.Errorf("%s planned although it is on disk: %v", id, ids)
		}
	}
	if r := keptReason(e, "prek"); r != "" {
		t.Errorf("prek kept as %q", r)
	}
}

// The package index is refreshed by the first install that runs, whichever row that is.
func TestRefreshBeforeTheFirstInstallThatRuns(t *testing.T) {
	e, f := newEnv(t, "apt-get", "sudo", "git", "node", "jq", "go", "npm", "google-chrome", "prek", "deja", "grype", "typescript-language-server", "pyright-langserver")
	steps := e.prereqSteps()
	find := func(id string) plan.Step {
		for _, s := range steps {
			if s.ID == id {
				return s
			}
		}
		t.Fatalf("no %s in %v", id, stepIDs(e))
		return plan.Step{}
	}
	// gh is an earlier row than gopls; run gopls first, as a failed earlier row and a rerun could.
	if _, err := find("prereq.gopls").Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls := f.argvs(); len(calls) < 2 || calls[0] != "sudo -n apt-get update" {
		t.Errorf("calls %v, want apt-get update before the first install that runs", calls)
	}
	if _, err := find("prereq.gh").Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(strings.Join(f.argvs(), "\n"), "apt-get update"); n != 1 {
		t.Errorf("%d refreshes, want one", n)
	}
}

// brew and winget need no root: without sudo the package route still applies for them.
func TestBrewAndWingetNeedNoSudo(t *testing.T) {
	for _, tc := range []struct {
		os  string
		pm  platform.PM
		bin string
	}{{"darwin", platform.Brew, "brew"}, {"windows", platform.WinGet, "winget"}} {
		e, _ := newEnv(t, tc.bin, "git", "node", "jq")
		e.Platform = platform.Platform{OS: tc.os, Arch: "amd64", PM: tc.pm}
		if ids := stepIDs(e); !slices.Contains(ids, "prereq.gh") {
			t.Errorf("%s: gh not planned without sudo: %v", tc.pm, ids)
		}
	}
}

// On Windows npm puts its shims in the prefix itself, so the prefix is ~/.local/bin there.
func TestNpmPrefixOnWindows(t *testing.T) {
	e, _ := newEnv(t, "winget", "git", "node", "jq", "npm")
	e.Platform = platform.Platform{OS: "windows", Arch: "amd64", PM: platform.WinGet}
	s := stepByID(t, e, "prereq.pyright")
	i := slices.Index(s.Command, "--prefix")
	if i < 0 || i+1 >= len(s.Command) || s.Command[i+1] != filepath.Join(e.Roots.Home, ".local", "bin") {
		t.Errorf("command %v, want --prefix ~/.local/bin on Windows", s.Command)
	}
}

// --reinstall names a tool the table knows, or the run stops before the plan.
func TestReinstallUnknownTool(t *testing.T) {
	e, _ := newEnv(t, "apt-get", "sudo", "git", "node", "jq")
	e.Reinstall = []string{"nope"}
	if err := e.checkReinstall(); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("err %v, want the unknown tool named", err)
	}
	e.Reinstall = []string{"prek"}
	if err := e.checkReinstall(); err != nil {
		t.Errorf("a known tool: %v", err)
	}
}
