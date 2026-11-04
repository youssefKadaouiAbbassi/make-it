package setup

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"makeit/internal/claudecli"
	"makeit/internal/execx"
	"makeit/internal/plan"
	"makeit/internal/platform"
	"makeit/internal/state"
)

// upgradeEnv is a machine with Claude Code at have (source native or apt), npm's latest at
// newest, and an upgrade command that moves the version to after.
func upgradeEnv(t *testing.T, source, have, newest, after string) (*env, *fakeRunner) {
	t.Helper()
	found := []claudecli.Install{{Path: nativeClaude, Source: source, OnPath: true}}
	e, f := claudeEnv(t, &found)
	e.command = "update"
	cur := have
	f.answers[nativeClaude+" --version"] = func(execx.Cmd) execx.Result { return execx.Result{Stdout: cur + " (Claude Code)\n"} }
	f.answers["sh -c "+e.shell(claudecli.UpgradeCommand(source, e.Platform.PM, ""))] = func(execx.Cmd) execx.Result {
		cur = after
		return execx.Result{}
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"stable":"2.1.300","latest":"` + newest + `"}`))
	}))
	t.Cleanup(srv.Close)
	e.Client, e.RegistryBase = srv.Client(), srv.URL
	g, err := state.NewGeneration(e.Roots.Stack, 0, "update", nil)
	if err != nil {
		t.Fatal(err)
	}
	e.gen = g
	return e, f
}

func runUpgrade(t *testing.T, e *env) (done bool, err error) {
	t.Helper()
	s := e.claudeUpgradeStep()
	if ok, err := s.Already(context.Background()); ok || err != nil {
		return false, err
	}
	_, err = s.Run(context.Background())
	return true, err
}

func TestUpdateUpgradesClaude(t *testing.T) {
	e, f := upgradeEnv(t, "native", "2.1.317", "2.1.320", "2.1.320")
	ran, err := runUpgrade(t, e)
	if !ran || err != nil {
		t.Fatalf("ran %v err %v", ran, err)
	}
	if !slices.Contains(f.argvs(), "sh -c "+e.shell("claude update")) {
		t.Errorf("calls %v, want claude update", f.argvs())
	}
	if want := (state.Change{Kind: "claude-code", Item: "Claude Code", From: "2.1.317", To: "2.1.320"}); !slices.Contains(e.changes, want) {
		t.Errorf("changes %+v", e.changes)
	}
	if e.st.ClaudeCode.VersionSeen != "2.1.320" {
		t.Errorf("version seen %q", e.st.ClaudeCode.VersionSeen)
	}
}

func TestUpdateClaudeCurrent(t *testing.T) {
	e, _ := upgradeEnv(t, "native", "2.1.320", "2.1.320", "2.1.320")
	if ran, err := runUpgrade(t, e); ran || err != nil {
		t.Errorf("ran %v err %v, want already", ran, err)
	}
}

// The package repository can lag npm: the upgrade runs, nothing moves, the run says so and
// does not fail.
func TestUpdateClaudeRepoLags(t *testing.T) {
	e, _ := upgradeEnv(t, "apt", "2.1.317", "2.1.320", "2.1.317")
	e.Root = true
	ran, err := runUpgrade(t, e)
	if !ran || (err != nil && err != plan.ErrSkip) {
		t.Fatalf("ran %v err %v", ran, err)
	}
	if r := keptReason(e, "claude-code"); !strings.Contains(r, "2.1.320") || !strings.Contains(r, "not in your package manager yet") {
		t.Errorf("kept %q", r)
	}
	if len(e.changes) != 0 {
		t.Errorf("changes %+v, want none", e.changes)
	}
}

// The daily run never asks for a password: an apt install is left with a note.
func TestDailyRunSkipsSudoClaudeUpgrade(t *testing.T) {
	e, f := upgradeEnv(t, "apt", "2.1.317", "2.1.320", "2.1.320")
	e.Background = true
	if ran, _ := runUpgrade(t, e); ran {
		t.Error("the daily run upgraded through sudo")
	}
	for _, c := range f.argvs() {
		if strings.Contains(c, "apt-get") {
			t.Errorf("ran %s", c)
		}
	}
	if r := keptReason(e, "claude-code"); !strings.Contains(r, "2.1.320") || !strings.Contains(r, "makeit update") {
		t.Errorf("kept %q", r)
	}
}

// The user's own channel choice decides what newest means.
func TestUpdateClaudeFollowsStableChannel(t *testing.T) {
	e, _ := upgradeEnv(t, "native", "2.1.311", "2.1.320", "2.1.320")
	if err := os.MkdirAll(e.Roots.Claude, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.Roots.Claude, "settings.json"), []byte(`{"autoUpdatesChannel":"stable"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if ran, err := runUpgrade(t, e); ran || err != nil {
		t.Errorf("ran %v err %v: 2.1.311 is newer than stable's 2.1.300", ran, err)
	}
}

var _ = platform.APT
