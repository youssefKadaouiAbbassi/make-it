package setup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"makeit/internal/catalog"
	"makeit/internal/execx"
	"makeit/internal/mcplocal"
	"makeit/internal/plan"
)

// mcpEnv is a machine whose catalog pins chrome-devtools-mcp at pin and whose local copy is at
// installed ("" leaves it out).
func mcpEnv(t *testing.T, pin, installed string) (*env, *fakeRunner) {
	t.Helper()
	e, f := newEnv(t, "node", "npm")
	dir := t.TempDir()
	if pin != "" {
		write(t, filepath.Join(dir, "plugins", "makeit", ".mcp.json"),
			`{"mcpServers":{"chrome-devtools":{"command":"node","args":["-e","x","chrome-devtools-mcp@`+pin+`"]}}}`)
	}
	e.Catalog = catalog.Dir(dir)
	if installed != "" {
		write(t, filepath.Join(mcplocal.Dir(e.Roots.Stack), "node_modules", mcplocal.Package, "package.json"), `{"version":"`+installed+`"}`)
	}
	return e, f
}

func write(t testing.TB, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMCPLocalInstallsMissingCopy(t *testing.T) {
	e, f := mcpEnv(t, "1.12.0", "")
	s := e.mcpLocalStep()
	if done, err := s.Already(context.Background()); done || err != nil {
		t.Fatalf("already %v err %v, want a step to run", done, err)
	}
	if _, err := s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := "npm install --prefix " + mcplocal.Dir(e.Roots.Stack) + " --no-audit --no-fund chrome-devtools-mcp@1.12.0"
	if !slices.Contains(f.argvs(), want) {
		t.Errorf("calls %v, want %q", f.argvs(), want)
	}
}

// update: a changed pin reinstalls, the same pin does not.
func TestMCPLocalReinstallsOnlyWhenPinMoved(t *testing.T) {
	e, _ := mcpEnv(t, "1.13.0", "1.12.0")
	if done, err := e.mcpLocalStep().Already(context.Background()); done || err != nil {
		t.Errorf("moved pin: already %v err %v, want reinstall", done, err)
	}
	e, _ = mcpEnv(t, "1.12.0", "1.12.0")
	if done, err := e.mcpLocalStep().Already(context.Background()); !done || err != nil {
		t.Errorf("same pin: already %v err %v, want nothing to do", done, err)
	}
}

// A catalog that pins nothing (a custom one) leaves nothing to install.
func TestMCPLocalNoPin(t *testing.T) {
	e, _ := mcpEnv(t, "", "")
	if done, err := e.mcpLocalStep().Already(context.Background()); !done || err != nil {
		t.Errorf("already %v err %v, want done", done, err)
	}
}

// npm fails (offline, no space): the run goes on, npx still serves the server.
func TestMCPLocalInstallFailureKeeps(t *testing.T) {
	e, f := mcpEnv(t, "1.12.0", "")
	f.answers["npm install --prefix "+mcplocal.Dir(e.Roots.Stack)+" --no-audit --no-fund chrome-devtools-mcp@1.12.0"] =
		func(execx.Cmd) execx.Result { return execx.Result{Code: 1, Stderr: "ENOTFOUND"} }
	if _, err := e.mcpLocalStep().Run(context.Background()); !errors.Is(err, plan.ErrSkip) {
		t.Errorf("err %v, want ErrSkip", err)
	}
	if r := keptReason(e, "chrome-devtools-mcp"); !strings.Contains(r, "npx") {
		t.Errorf("kept %q, want it to say npx serves the server", r)
	}
}

// Offline: no network request, so no install.
func TestMCPLocalOffline(t *testing.T) {
	e, _ := mcpEnv(t, "1.12.0", "")
	e.Offline = true
	if done, err := e.mcpLocalStep().Already(context.Background()); !done || err != nil {
		t.Errorf("already %v err %v, want done", done, err)
	}
}

func TestMCPLocalInSetupSteps(t *testing.T) {
	e, _ := mcpEnv(t, "1.12.0", "")
	var ids []string
	for _, s := range e.setupSteps(nil) {
		ids = append(ids, s.ID)
	}
	if !slices.Contains(ids, "mcp.chrome-devtools") {
		t.Errorf("setup steps %v lack mcp.chrome-devtools", ids)
	}
}

func TestUnMCPLocalRemovesOnlyItsDir(t *testing.T) {
	e, _ := mcpEnv(t, "1.12.0", "1.12.0")
	other := filepath.Join(e.Roots.Stack, "mcp", "yours", "keep")
	write(t, other, "x")
	s := e.unMCPLocalStep()
	if done, _ := s.Already(context.Background()); done {
		t.Fatal("nothing to remove, but the copy is there")
	}
	if _, err := s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(mcplocal.Dir(e.Roots.Stack)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("copy still there: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("a neighbour was removed: %v", err)
	}
	if done, _ := s.Already(context.Background()); !done {
		t.Error("removed copy still planned")
	}
}

// No npm: with node present it is the user's to fix (npx is no better off); with node still to be
// installed, npm comes with it and the step waits for that.
func TestMCPLocalWithoutNPM(t *testing.T) {
	e, _ := mcpEnv(t, "1.12.0", "")
	e.Present = only("node")
	if done, err := e.mcpLocalStep().Already(context.Background()); !done || err != nil {
		t.Errorf("node without npm: already %v err %v, want done", done, err)
	}
	e.Present = only()
	if _, err := e.mcpLocalStep().Already(context.Background()); !errors.Is(err, plan.ErrNotYet) {
		t.Errorf("no node: err %v, want ErrNotYet", err)
	}
}
