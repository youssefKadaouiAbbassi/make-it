package setup

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"makeit/internal/execx"
	"makeit/internal/paths"
	"makeit/internal/platform"
	"makeit/internal/state"
)

// fakeRunner answers commands by their full argv joined with spaces and records every call.
type fakeRunner struct {
	mu      sync.Mutex
	answers map[string]func(execx.Cmd) execx.Result
	calls   []execx.Cmd
}

func (f *fakeRunner) run(ctx context.Context, c execx.Cmd) (execx.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
	if err := ctx.Err(); err != nil {
		return execx.Result{Code: -1}, err
	}
	if a, ok := f.answers[strings.Join(c.Argv, " ")]; ok {
		return a(c), nil
	}
	return execx.Result{}, nil
}

func (f *fakeRunner) argvs() []string {
	var out []string
	for _, c := range f.calls {
		out = append(out, strings.Join(c.Argv, " "))
	}
	return out
}

func only(names ...string) func(string) bool {
	return func(n string) bool { return slices.Contains(names, n) }
}

// newEnv is an env on temp roots, linux with apt, --yes, not root.
func newEnv(t *testing.T, present ...string) (*env, *fakeRunner) {
	t.Helper()
	home := t.TempDir()
	f := &fakeRunner{answers: map[string]func(execx.Cmd) execx.Result{}}
	e := &env{Env: Env{
		Roots:    paths.Roots{Home: home, Claude: filepath.Join(home, ".claude"), Stack: filepath.Join(home, ".makeit")},
		Platform: platform.Platform{OS: "linux", Arch: "amd64", PM: platform.APT},
		Present:  only(present...),
		Run:      f.run,
		Yes:      true,
	}, st: &state.State{}}
	return e, f
}

func stepIDs(e *env) []string {
	var ids []string
	for _, s := range e.prereqSteps() {
		ids = append(ids, s.ID)
	}
	return ids
}

// Every prerequisite but git present: git is installed, optional rows go to skipped.
func TestPrereqStepsRequiredOnly(t *testing.T) {
	e, _ := newEnv(t, "apt-get", "node", "jq")
	steps := e.prereqSteps()
	if len(steps) != 1 || steps[0].ID != "prereq.git" {
		t.Fatalf("steps %v, want prereq.git only", stepIDs(e))
	}
	if want := []string{"sudo", "-n", "env", "DEBIAN_FRONTEND=noninteractive", "apt-get", "install", "-y", "git"}; !slices.Equal(steps[0].Command, want) {
		t.Errorf("command %v, want %v", steps[0].Command, want)
	}
	var skipped []string
	for _, r := range e.skipped {
		skipped = append(skipped, r.ID)
	}
	for _, id := range []string{"gh", "prek", "grype"} {
		if !slices.Contains(skipped, id) {
			t.Errorf("optional %s not in skipped %v", id, skipped)
		}
	}
}

func TestPrereqAptUpdateOnce(t *testing.T) {
	e, f := newEnv(t, "apt-get", "node")
	for _, s := range e.prereqSteps() {
		if _, err := s.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	got := f.argvs()
	if len(got) != 3 || got[0] != "sudo -n apt-get update" || strings.Count(strings.Join(got, "\n"), "apt-get update") != 1 {
		t.Errorf("calls %v, want one apt-get update before the two installs", got)
	}
}

func TestPrereqSudoFails(t *testing.T) {
	e, f := newEnv(t, "apt-get", "node", "jq")
	f.answers["sudo -n apt-get update"] = func(execx.Cmd) execx.Result {
		return execx.Result{Code: 1, Stderr: "sudo: a password is required"}
	}
	steps := e.prereqSteps()
	if _, err := steps[0].Run(context.Background()); err == nil || !strings.Contains(err.Error(), "a password is required") {
		t.Errorf("err = %v, want it to name the sudo failure", err)
	}
}

func TestPrereqRequiredWithoutRouteFails(t *testing.T) {
	e, _ := newEnv(t, "git", "node", "jq")
	e.Platform.PM = platform.None
	e.Present = only("git", "jq")
	steps := e.prereqSteps()
	if len(steps) != 1 || steps[0].ID != "prereq.node" {
		t.Fatalf("steps %v", stepIDs(e))
	}
	if _, err := steps[0].Run(context.Background()); err == nil || !strings.Contains(err.Error(), "nodejs.org") {
		t.Errorf("err = %v, want the manual route", err)
	}
}

func TestWriteKeepsPreImage(t *testing.T) {
	e, _ := newEnv(t)
	g, err := state.NewGeneration(e.Roots.Stack, 0, "setup", nil)
	if err != nil {
		t.Fatal(err)
	}
	e.gen = g
	old := filepath.Join(e.Roots.Claude, "settings.json")
	if err := os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, []byte(`{"a":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := e.write("settings", old, []byte(`{"a":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(e.Roots.Claude, "CLAUDE.md")
	if err := e.write("global.CLAUDE.md", fresh, []byte("# hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ops := g.Manifest.Ops
	if len(ops) != 2 || ops[0].PreImage == "" || ops[0].PreSha256 == nil || ops[1].PreImage != "" || ops[1].PreSha256 != nil {
		t.Fatalf("ops %+v", ops)
	}
	if b, _ := os.ReadFile(filepath.Join(g.Dir, ops[0].PreImage)); string(b) != `{"a":1}` {
		t.Errorf("pre-image %q", b)
	}
	if b, _ := os.ReadFile(old); string(b) != `{"a":2}` {
		t.Errorf("written %q", b)
	}
	if ops[0].Path != "claude:settings.json" {
		t.Errorf("path %q, want the portable form", ops[0].Path)
	}
}

func TestWriteRefusesWithoutGeneration(t *testing.T) {
	e, _ := newEnv(t)
	if err := e.write("x", filepath.Join(e.Roots.Claude, "x"), nil, 0o644); err == nil {
		t.Error("a write without a generation (a dry run) succeeded")
	}
}
