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
	// The node version probe is planning, not a route's command: no test asserts on it.
	if strings.Join(c.Argv, " ") != "node -v" {
		f.calls = append(f.calls, c)
	}
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

// Nothing is optional: every missing tool that applies here gets a step, by its route.
func TestPrereqStepsEveryMissingTool(t *testing.T) {
	e, _ := newEnv(t, "apt-get", "sudo", "node", "jq")
	steps := e.prereqSteps()
	if len(steps) == 0 || steps[0].ID != "prereq.git" {
		t.Fatalf("steps %v, want prereq.git first", stepIDs(e))
	}
	if want := []string{"sudo", "-n", "env", "DEBIAN_FRONTEND=noninteractive", "apt-get", "install", "-y", "git"}; !slices.Equal(steps[0].Command, want) {
		t.Errorf("command %v, want %v", steps[0].Command, want)
	}
	ids := stepIDs(e)
	for _, id := range []string{"prereq.gh", "prereq.chrome", "prereq.prek", "prereq.deja-vu", "prereq.grype"} {
		if !slices.Contains(ids, id) {
			t.Errorf("%s not planned: %v", id, ids)
		}
	}
}

// The LSP rows: the toolchain-bound ones only on a machine with that toolchain (LSP-ROWS.md);
// the npm ones through npm; one with neither a package nor its toolchain route here needs you.
func TestPrereqLSPRows(t *testing.T) {
	core := []string{"apt-get", "sudo", "git", "node", "jq"}
	for _, id := range []string{"prereq.gopls", "prereq.rust-analyzer"} {
		e, _ := newEnv(t, core...)
		if slices.Contains(stepIDs(e), id) {
			t.Errorf("%s planned without its toolchain", id)
		}
	}
	e, _ := newEnv(t, append(core, "npm")...)
	ids := stepIDs(e)
	for _, id := range []string{"prereq.typescript-language-server", "prereq.pyright"} {
		if !slices.Contains(ids, id) {
			t.Errorf("%s not planned with npm: %v", id, ids)
		}
	}
	e, _ = newEnv(t, append(core, "cargo")...)
	if ids := stepIDs(e); slices.Contains(ids, "prereq.rust-analyzer") || !slices.ContainsFunc(e.needsYou, func(n NeedsYou) bool { return n.ID == "rust-analyzer" }) {
		t.Errorf("cargo without rustup on apt: steps %v, needs you %+v; want rust-analyzer left to the user", ids, e.needsYou)
	}
}

func TestPrereqAptUpdateOnce(t *testing.T) {
	// Only git and jq are missing: the other tools are present by their program names.
	e, f := newEnv(t, "apt-get", "node", "gh", "google-chrome", "prek", "deja", "grype", "typescript-language-server", "pyright-langserver")
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
	e.Platform = platform.Platform{OS: "darwin", Arch: "arm64", PM: platform.None}
	e.Present = only("git", "jq")
	node := stepByID(t, e, "prereq.node")
	if _, err := node.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "nodejs.org") {
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

// A settings.json that is a symlink (stow, home-manager) stays one: the write lands in its
// target, whose mode is kept, and the pre-image is the target's content.
func TestWriteFollowsSymlink(t *testing.T) {
	e, _ := newEnv(t)
	g, err := state.NewGeneration(e.Roots.Stack, 0, "setup", nil)
	if err != nil {
		t.Fatal(err)
	}
	e.gen = g
	target := filepath.Join(t.TempDir(), "dotfiles-settings.json")
	if err := os.WriteFile(target, []byte(`{"a":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(e.Roots.Claude, "settings.json")
	if err := os.MkdirAll(e.Roots.Claude, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := e.write("settings", link, []byte(`{"a":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the symlink was replaced by a regular file (%v)", err)
	}
	if b, _ := os.ReadFile(target); string(b) != `{"a":2}` {
		t.Errorf("target %q, want the new content", b)
	}
	if fi, _ := os.Stat(target); fi.Mode().Perm() != 0o644 {
		t.Errorf("target mode %v, want 0644 kept", fi.Mode().Perm())
	}
	if b, _ := os.ReadFile(filepath.Join(g.Dir, g.Manifest.Ops[0].PreImage)); string(b) != `{"a":1}` {
		t.Errorf("pre-image %q", b)
	}
}

func TestWriteRefusesDanglingSymlink(t *testing.T) {
	e, _ := newEnv(t)
	g, err := state.NewGeneration(e.Roots.Stack, 0, "setup", nil)
	if err != nil {
		t.Fatal(err)
	}
	e.gen = g
	link := filepath.Join(e.Roots.Claude, "CLAUDE.md")
	if err := os.MkdirAll(e.Roots.Claude, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "gone.md"), link); err != nil {
		t.Fatal(err)
	}
	if err := e.write("global.CLAUDE.md", link, []byte("# x\n"), 0o644); err == nil {
		t.Error("wrote through a symlink to a missing file")
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Error("the dangling symlink was replaced")
	}
}

// A link with a relative target (settings.json -> ../dotfiles/settings.json) resolves against the
// link's directory (filepath.EvalSymlinks), and the write lands in that file.
func TestWriteFollowsRelativeSymlink(t *testing.T) {
	e, _ := newEnv(t)
	g, err := state.NewGeneration(e.Roots.Stack, 0, "setup", nil)
	if err != nil {
		t.Fatal(err)
	}
	e.gen = g
	dotfiles := filepath.Join(e.Roots.Home, "dotfiles")
	if err := os.MkdirAll(dotfiles, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dotfiles, "settings.json"), []byte(`{"a":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(e.Roots.Claude, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(e.Roots.Claude, "settings.json")
	if err := os.Symlink(filepath.Join("..", "dotfiles", "settings.json"), link); err != nil {
		t.Fatal(err)
	}
	if err := e.write("settings", link, []byte(`{"a":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dotfiles, "settings.json")); string(b) != `{"a":2}` {
		t.Errorf("dotfiles copy %q, want the new content", b)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Error("the relative symlink was replaced")
	}
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWriteRecordsCreatedDirs(t *testing.T) {
	e, _ := newEnv(t)
	g, err := state.NewGeneration(e.Roots.Stack, 0, "setup", nil)
	if err != nil {
		t.Fatal(err)
	}
	e.gen = g
	if err := e.write("global.rules/deep/go.md", filepath.Join(e.Roots.Claude, "rules", "deep", "go.md"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(e.Roots.Claude, "existing", "keep.md"), "mine\n")
	if err := e.write("global.existing/new.md", filepath.Join(e.Roots.Claude, "existing", "new.md"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"claude:rules", "claude:rules/deep"} {
		if r := e.st.Dirs[d]; r == nil || !r.CreatedByUs {
			t.Errorf("%s = %+v, want recorded as created by makeit", d, r)
		}
	}
	for _, d := range []string{"claude:", "claude:existing"} {
		if r := e.st.Dirs[d]; r != nil {
			t.Errorf("%s recorded as %+v; it existed before or is the root", d, r)
		}
	}
	if len(e.st.Dirs) != 2 {
		t.Errorf("dirs %v, want only the two created under the Claude directory", e.st.Dirs)
	}
}
