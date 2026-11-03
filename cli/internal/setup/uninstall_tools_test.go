package setup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"makeit/internal/fsx"
	"makeit/internal/state"
)

func toolEnv(t *testing.T) *env {
	t.Helper()
	e := globalEnv(t)
	if err := os.MkdirAll(filepath.Join(e.Roots.Home, ".local", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	return e
}

func writeBin(t *testing.T, e *env, name, content string) string {
	t.Helper()
	path := filepath.Join(e.Roots.Home, ".local", "bin", name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// sha is the form setup records for a release binary (fsx.Sha256), so a match is a real match.
func sha(s string) string { return fsx.Sha256([]byte(s)) }

// Release binaries makeit downloaded are removed; one changed since is kept, like any owned file.
func TestUninstallRemovesReleaseBinaries(t *testing.T) {
	e := toolEnv(t)
	writeBin(t, e, "prek", "#!/bin/sh\necho prek\n")
	grype := writeBin(t, e, "grype", "#!/bin/sh\necho grype\n")
	e.st.Prereqs = map[string]*state.Prereq{
		"prek":  {InstalledBy: "makeit", Route: "release", Sha256: sha("#!/bin/sh\necho prek\n")},
		"grype": {InstalledBy: "makeit", Route: "release", Sha256: sha("#!/bin/sh\necho grype\n")},
	}
	if err := os.WriteFile(grype, []byte("edited by hand\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := step(t, e.toolSteps(), "tool.prek")
	if _, err := s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.Roots.Home, ".local", "bin", "prek")); err == nil {
		t.Error("prek still there")
	}
	if e.st.Prereqs["prek"] != nil {
		t.Error("prek record still there")
	}
	g := step(t, e.toolSteps(), "tool.grype")
	if done, _ := g.Already(context.Background()); !done {
		t.Fatal("a changed binary is not already kept")
	}
	if r := keptReason(e, "grype"); r != "changed since makeit wrote it" {
		t.Errorf("kept %q", r)
	}
	if _, err := os.Stat(grype); err != nil {
		t.Error("the changed grype binary was removed")
	}
}

// A tool installed through the package manager or a toolchain is left alone, and uninstall names
// how to remove it.
func TestUninstallNamesPackageTools(t *testing.T) {
	e := toolEnv(t)
	e.st.Prereqs = map[string]*state.Prereq{
		"gh":                         {InstalledBy: "makeit", Route: "apk"},
		"chrome":                     {InstalledBy: "makeit", Route: "deb"},
		"gopls":                      {InstalledBy: "makeit", Route: "go"},
		"typescript-language-server": {InstalledBy: "makeit", Route: "npm"},
		"prek":                       {InstalledBy: "makeit", Route: "winget"},
		"deja-vu":                    {InstalledBy: "makeit", Route: "brew"},
	}
	e.toolSteps()
	// The package names, not makeit's tool ids.
	for id, want := range map[string]string{
		"gh":                         "sudo apk del github-cli",
		"chrome":                     "sudo apt-get remove google-chrome-stable",
		"gopls":                      "rm ~/.local/bin/gopls",
		"typescript-language-server": "npm uninstall -g --prefix ~/.local typescript-language-server typescript",
		"prek":                       "winget uninstall --id j178.prek",
		"deja-vu":                    "brew uninstall deja-vu",
	} {
		r := keptReason(e, id)
		if r == "" {
			t.Errorf("%s: no kept reason", id)
			continue
		}
		if !strings.Contains(r, want) {
			t.Errorf("%s: kept %q, want it to mention %q", id, r, want)
		}
	}
}

func TestUninstallRemovesReleaseExeOnWindows(t *testing.T) {
	e := toolEnv(t)
	e.Platform.OS = "windows"
	writeBin(t, e, "prek.exe", "MZ")
	e.st.Prereqs = map[string]*state.Prereq{"prek": {InstalledBy: "makeit", Route: "release", Sha256: sha("MZ")}}
	s := step(t, e.toolSteps(), "tool.prek")
	if done, _ := s.Already(context.Background()); done {
		t.Fatal("prek.exe counted as gone")
	}
	if _, err := s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.Roots.Home, ".local", "bin", "prek.exe")); err == nil {
		t.Error("prek.exe still there")
	}
}
