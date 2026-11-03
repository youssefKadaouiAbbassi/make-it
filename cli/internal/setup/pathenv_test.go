package setup

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"makeit/internal/pathenv"
	"makeit/internal/plan"
	"makeit/internal/state"
)

// pathEnv is newEnv with a generation (steps write) and HOME/SHELL in its environment.
func pathEnvFixture(t *testing.T, shell string) *env {
	t.Helper()
	e, _ := newEnv(t, "apt-get", "git", "node", "jq", "gh", "google-chrome", "prek", "deja", "grype", "typescript-language-server", "pyright-langserver")
	e.Getenv = func(k string) string {
		switch k {
		case "HOME":
			return e.Roots.Home
		case "SHELL":
			return shell
		}
		return ""
	}
	g, err := state.NewGeneration(e.Roots.Stack, 0, "setup", nil)
	if err != nil {
		t.Fatal(err)
	}
	e.gen = g
	return e
}

func runStep(t *testing.T, s plan.Step) {
	t.Helper()
	if done, err := s.Already(context.Background()); err != nil {
		t.Fatal(err)
	} else if done {
		return
	}
	if _, err := s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestEnvStepWritesEnvFile(t *testing.T) {
	e := pathEnvFixture(t, "/bin/bash")
	if err := os.MkdirAll(filepath.Join(e.Roots.Home, "go", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	runStep(t, e.envStep())
	b, err := os.ReadFile(filepath.Join(e.Roots.Stack, "env"))
	if err != nil || !strings.Contains(string(b), `$HOME/.local/bin`) || !strings.Contains(string(b), `$HOME/go/bin`) {
		t.Errorf("env file %q, %v", b, err)
	}
	if rec := e.st.Files[e.Roots.Portable(filepath.Join(e.Roots.Stack, "env"))]; rec == nil || rec.Class != "owned" {
		t.Errorf("record %+v", rec)
	}
	if done, _ := e.envStep().Already(context.Background()); !done {
		t.Error("a second run is not already done")
	}
}

func TestProfileStepAddsBlockOnce(t *testing.T) {
	e := pathEnvFixture(t, "/usr/bin/zsh")
	runStep(t, e.profileStep())
	runStep(t, e.profileStep())
	for _, f := range []string{".profile", ".zshenv"} {
		b, err := os.ReadFile(filepath.Join(e.Roots.Home, f))
		if err != nil || strings.Count(string(b), "# >>> makeit >>>") != 1 || !strings.Contains(string(b), `. "$HOME/.makeit/env"`) {
			t.Errorf("%s: %q %v", f, b, err)
		}
		rec := e.st.Files[e.Roots.Portable(filepath.Join(e.Roots.Home, f))]
		if rec == nil || rec.Class != "merge" || len(rec.Added) != 3 {
			t.Errorf("%s record %+v", f, rec)
		}
	}
	if done, _ := e.profileStep().Already(context.Background()); !done {
		t.Error("a third run is not already done")
	}
}

func TestProfileStepKeepsUserEdits(t *testing.T) {
	e := pathEnvFixture(t, "/bin/bash")
	bashrc := filepath.Join(e.Roots.Home, ".bashrc")
	writeFile(t, bashrc, "export FOO=1\nalias ll='ls -l'\n")
	runStep(t, e.profileStep())
	b, _ := os.ReadFile(bashrc)
	if !strings.HasPrefix(string(b), "export FOO=1\nalias ll='ls -l'\n") || !strings.Contains(string(b), "# >>> makeit >>>") {
		t.Errorf("bashrc %q", b)
	}
}

func TestBackgroundRunSkipsProfile(t *testing.T) {
	e := pathEnvFixture(t, "/bin/bash")
	e.Background = true
	var ids []string
	for _, s := range e.pathSteps() {
		ids = append(ids, s.ID)
	}
	if !slices.Equal(ids, []string{"path.env"}) {
		t.Errorf("background path steps %v, want the env file only", ids)
	}
}

func TestUninstallRemovesBlockAndEnv(t *testing.T) {
	e := pathEnvFixture(t, "/bin/bash")
	profile := filepath.Join(e.Roots.Home, ".profile")
	writeFile(t, profile, "export FOO=1\n")
	runStep(t, e.envStep())
	runStep(t, e.profileStep())
	for _, key := range []string{e.Roots.Portable(profile), e.Roots.Portable(filepath.Join(e.Roots.Stack, "env"))} {
		runStep(t, e.unFileStep(key))
	}
	if b, _ := os.ReadFile(profile); string(b) != "export FOO=1\n" {
		t.Errorf("profile after uninstall %q", b)
	}
	if _, err := os.Stat(filepath.Join(e.Roots.Stack, "env")); err == nil {
		t.Error("env file still there")
	}
	if len(e.st.Files) != 0 {
		t.Errorf("records left %v", e.st.Files)
	}
}

func TestProfileStepWindowsCommand(t *testing.T) {
	e := pathEnvFixture(t, "")
	e.Platform.OS = "windows"
	e.Roots.Home = filepath.Join(e.Roots.Home, "O'Brien")
	s := e.profileStep()
	if len(s.Command) < 3 || s.Command[0] != "powershell" || !strings.Contains(s.Command[len(s.Command)-1], "SetEnvironmentVariable('Path'") {
		t.Errorf("command %v", s.Command)
	}
	// A quote in the home path is doubled inside the single-quoted PowerShell string.
	if ps := s.Command[len(s.Command)-1]; !strings.Contains(ps, "O''Brien") || strings.Contains(ps, "O'Brien") {
		t.Errorf("quote not escaped: %s", ps)
	}
}

// A line someone copied from makeit's block into their profile by hand survives uninstall; only
// the block between the markers goes.
func TestUninstallKeepsHandWrittenLoaderLine(t *testing.T) {
	e := pathEnvFixture(t, "/bin/bash")
	profile := filepath.Join(e.Roots.Home, ".profile")
	runStep(t, e.envStep())
	runStep(t, e.profileStep())
	withBlock, _ := os.ReadFile(profile)
	loader := pathenv.Block(e.envPath(), e.Roots.Home)[1]
	writeFile(t, profile, "export FOO=1\n"+loader+"\n"+string(withBlock))
	runStep(t, e.unFileStep(e.Roots.Portable(profile)))
	if b, _ := os.ReadFile(profile); string(b) != "export FOO=1\n"+loader+"\n" {
		t.Errorf("profile after uninstall %q", b)
	}
}
