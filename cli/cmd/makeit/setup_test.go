package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"makeit/internal/catalog"
	"makeit/internal/claudecli"
	"makeit/internal/execx"
	"makeit/internal/paths"
	"makeit/internal/platform"
	"makeit/internal/setup"
)

// fakeSetupEnv swaps setupEnv for a machine on temp roots with no Claude Code and a runner
// that answers nothing.
func fakeSetupEnv(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	saved := setupEnv
	t.Cleanup(func() { setupEnv = saved })
	setupEnv = func(context.Context) (setup.Env, error) {
		return setup.Env{
			Roots:    paths.Roots{Home: home, Claude: filepath.Join(home, ".claude"), Stack: filepath.Join(home, ".makeit")},
			Platform: platform.Platform{OS: "linux", Arch: "amd64", PM: platform.APT},
			Present:  func(string) bool { return true },
			Run:      func(context.Context, execx.Cmd) (execx.Result, error) { return execx.Result{}, nil },
			Catalog:  catalog.Embedded,
			Locate:   func(context.Context) []claudecli.Install { return nil },
		}, nil
	}
}

func TestSetupConsentExit2(t *testing.T) {
	fakeSetupEnv(t)
	code, out, errOut := call(t, noGather(t), "setup")
	if code != 2 || out != "" || !strings.Contains(errOut, "Run again with --yes") || !strings.Contains(errOut, "Install Claude Code") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
}

func TestSetupJSON(t *testing.T) {
	fakeSetupEnv(t)
	code, out, errOut := call(t, noGather(t), "setup", "--dry-run", "--json")
	var report struct {
		Plan       []map[string]any `json:"plan"`
		Generation int              `json:"generation"`
	}
	decodeOne(t, out, &report)
	if code != 0 || len(report.Plan) == 0 || report.Generation != 0 || errOut != "" {
		t.Errorf("exit %d, plan %d steps, generation %d, stderr %q", code, len(report.Plan), report.Generation, errOut)
	}
}

func TestSetupRejectsArguments(t *testing.T) {
	fakeSetupEnv(t)
	if code, _, _ := call(t, noGather(t), "setup", "now"); code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
}
