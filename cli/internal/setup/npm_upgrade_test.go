package setup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"makeit/internal/execx"
	"makeit/internal/plan"
	"makeit/internal/state"
)

// npmEnv is a machine where makeit installed row id with npm into ~/.local: installed holds the
// versions on disk ("" leaves a package's folder out), newest what npm view answers ("" fails).
func npmEnv(t *testing.T, id string, pkgs []string, installed, newest map[string]string) (*env, *fakeRunner, string) {
	t.Helper()
	e, f := newEnv(t, "node", "npm")
	prefix := filepath.Join(e.Roots.Home, ".local")
	e.st.Prereqs = map[string]*state.Prereq{id: {InstalledBy: "makeit", Route: "npm",
		Argv: append([]string{"npm", "install", "-g", "--prefix", prefix}, pkgs...)}}
	for _, p := range pkgs {
		if v := installed[p]; v != "" {
			dir := filepath.Join(prefix, "lib", "node_modules", p)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"`+p+`","version":"`+v+`"}`), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		v := newest[p]
		f.answers["npm view "+p+" version --fetch-retries=0"] = func(execx.Cmd) execx.Result {
			if v == "" {
				return execx.Result{Code: 1, Stderr: "npm error network"}
			}
			return execx.Result{Stdout: v + "\n"}
		}
	}
	return e, f, prefix
}

func npmStep(t *testing.T, e *env, id string) (plan.Step, bool) {
	t.Helper()
	for _, s := range e.npmUpgradeSteps() {
		if s.ID == "upgrade."+id {
			return s, true
		}
	}
	return plan.Step{}, false
}

func TestNPMUpgradeMovesBehindPackages(t *testing.T) {
	e, f, prefix := npmEnv(t, "typescript-language-server", []string{"typescript-language-server", "typescript"},
		map[string]string{"typescript-language-server": "4.0.0", "typescript": "5.0.0"},
		map[string]string{"typescript-language-server": "4.1.0", "typescript": "5.0.0"})
	s, ok := npmStep(t, e, "typescript-language-server")
	if !ok {
		t.Fatal("no upgrade step")
	}
	if done, err := s.Already(context.Background()); done || err != nil {
		t.Fatalf("already %v err %v", done, err)
	}
	if _, err := s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := "npm install -g --prefix " + prefix + " typescript-language-server@4.1.0"
	if !slices.Contains(f.argvs(), want) {
		t.Errorf("calls %v, want %q", f.argvs(), want)
	}
	if c := (state.Change{Kind: "npm", Item: "typescript-language-server", From: "4.0.0", To: "4.1.0"}); !slices.Equal(e.changes, []state.Change{c}) {
		t.Errorf("changes %+v, want only %+v", e.changes, c)
	}
}

func TestNPMUpgradeAlreadyNewest(t *testing.T) {
	e, _, _ := npmEnv(t, "marked", []string{"marked"}, map[string]string{"marked": "18.1.3"}, map[string]string{"marked": "18.1.3"})
	s, ok := npmStep(t, e, "marked")
	if !ok {
		t.Fatal("no upgrade step")
	}
	if done, err := s.Already(context.Background()); !done || err != nil {
		t.Errorf("already %v err %v, want done", done, err)
	}
}

// The registry cannot be read: the tool stays, the run says why, nothing fails.
func TestNPMUpgradeRegistryDown(t *testing.T) {
	e, _, _ := npmEnv(t, "marked", []string{"marked"}, map[string]string{"marked": "18.1.3"}, map[string]string{"marked": ""})
	s, _ := npmStep(t, e, "marked")
	if done, err := s.Already(context.Background()); !done || err != nil {
		t.Errorf("already %v err %v, want done", done, err)
	}
	if r := keptReason(e, "marked"); !strings.Contains(r, "kept at 18.1.3") || !strings.Contains(r, "npm registry") {
		t.Errorf("kept %q", r)
	}
}

func TestNPMUpgradeInstallFails(t *testing.T) {
	e, f, prefix := npmEnv(t, "marked", []string{"marked"}, map[string]string{"marked": "18.1.3"}, map[string]string{"marked": "19.0.0"})
	f.answers["npm install -g --prefix "+prefix+" marked@19.0.0"] = func(execx.Cmd) execx.Result { return execx.Result{Code: 1, Stderr: "EACCES"} }
	s, _ := npmStep(t, e, "marked")
	if done, _ := s.Already(context.Background()); done {
		t.Fatal("already done")
	}
	if _, err := s.Run(context.Background()); !errors.Is(err, plan.ErrSkip) {
		t.Errorf("err %v, want ErrSkip", err)
	}
	if r := keptReason(e, "marked"); !strings.Contains(r, "kept at 18.1.3") {
		t.Errorf("kept %q", r)
	}
	if len(e.changes) != 0 {
		t.Errorf("changes %+v, want none", e.changes)
	}
}

// Offline (or a daily run with no network): no lookup at all.
func TestNPMUpgradeOffline(t *testing.T) {
	e, f, _ := npmEnv(t, "marked", []string{"marked"}, map[string]string{"marked": "18.1.3"}, map[string]string{"marked": "19.0.0"})
	e.Offline = true
	s, _ := npmStep(t, e, "marked")
	if done, err := s.Already(context.Background()); !done || err != nil || len(f.calls) != 0 {
		t.Errorf("already %v err %v calls %v", done, err, f.argvs())
	}
}

// A tool the user installed, or one makeit got by another route, is not makeit's to move.
func TestNPMUpgradeSkipsOthers(t *testing.T) {
	for _, rec := range []state.Prereq{{InstalledBy: "", Route: "npm"}, {InstalledBy: "makeit", Route: "release"}} {
		e, _, _ := npmEnv(t, "marked", []string{"marked"}, map[string]string{"marked": "18.1.3"}, map[string]string{"marked": "19.0.0"})
		r := rec
		r.Argv = e.st.Prereqs["marked"].Argv
		e.st.Prereqs["marked"] = &r
		if _, ok := npmStep(t, e, "marked"); ok {
			t.Errorf("%+v got an upgrade step", rec)
		}
	}
}

// Every package folder gone: removed by you, so not put back by an upgrade.
func TestNPMUpgradeRemovedByYou(t *testing.T) {
	e, _, _ := npmEnv(t, "marked", []string{"marked"}, map[string]string{}, map[string]string{"marked": "19.0.0"})
	if _, ok := npmStep(t, e, "marked"); ok {
		t.Error("an upgrade step for a removed tool")
	}
}

func TestMarkedRowInstallsWithNPM(t *testing.T) {
	e, _ := newEnv(t, "apt-get", "sudo", "git", "node", "jq", "npm")
	for _, s := range e.prereqSteps() {
		if s.ID == "prereq.marked" {
			if want := "npm install -g --prefix " + filepath.Join(e.Roots.Home, ".local") + " marked"; strings.Join(s.Command, " ") != want {
				t.Errorf("command %v, want %q", s.Command, want)
			}
			return
		}
	}
	t.Errorf("no prereq.marked in %v", stepIDs(e))
}

func TestUninstallListsMarked(t *testing.T) {
	if got, want := removeCommand("npm", "marked"), "npm uninstall -g --prefix ~/.local marked"; got != want {
		t.Errorf("removeCommand %q, want %q", got, want)
	}
}

// npm moves are not undone (never pin): a run that only moved npm tools is no run to roll back.
func TestNPMChangesAreNotUndoable(t *testing.T) {
	if undoable(state.Manifest{Changes: []state.Change{{Kind: "npm", Item: "pyright", From: "1.1.400", To: "1.1.401"}}}) {
		t.Error("a run that only moved an npm tool counts as undoable")
	}
}

// npm can print a warning line before the version.
func TestNPMUpgradeReadsTheLastLine(t *testing.T) {
	e, f, _ := npmEnv(t, "marked", []string{"marked"}, map[string]string{"marked": "18.1.3"}, map[string]string{"marked": "19.0.0"})
	f.answers["npm view marked version --fetch-retries=0"] = func(execx.Cmd) execx.Result {
		return execx.Result{Stdout: "npm warn config production Use `--omit=dev` instead.\n19.0.0\n"}
	}
	s, _ := npmStep(t, e, "marked")
	if done, _ := s.Already(context.Background()); done {
		t.Fatal("already done")
	}
	if _, err := s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(f.argvs(), func(a string) bool { return strings.HasSuffix(a, " marked@19.0.0") }) {
		t.Errorf("calls %v", f.argvs())
	}
}

func TestNPMUpgradeNoVersionPrinted(t *testing.T) {
	e, f, _ := npmEnv(t, "marked", []string{"marked"}, map[string]string{"marked": "18.1.3"}, map[string]string{"marked": "19.0.0"})
	f.answers["npm view marked version --fetch-retries=0"] = func(execx.Cmd) execx.Result { return execx.Result{} }
	s, _ := npmStep(t, e, "marked")
	if done, _ := s.Already(context.Background()); !done {
		t.Fatal("not done")
	}
	if r := keptReason(e, "marked"); !strings.Contains(r, "npm printed no version") {
		t.Errorf("kept %q", r)
	}
}

// One registry failure is enough for the run: the other npm tools are not asked again.
func TestNPMUpgradeAsksTheRegistryOnceWhenDown(t *testing.T) {
	e, f, prefix := npmEnv(t, "marked", []string{"marked"}, map[string]string{"marked": "18.1.3"}, map[string]string{"marked": ""})
	dir := filepath.Join(prefix, "lib", "node_modules", "pyright")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"version":"1.1.400"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	e.st.Prereqs["pyright"] = &state.Prereq{InstalledBy: "makeit", Route: "npm", Argv: []string{"npm", "install", "-g", "--prefix", prefix, "pyright"}}
	for _, s := range e.npmUpgradeSteps() {
		_, _ = s.Already(context.Background())
	}
	views := 0
	for _, a := range f.argvs() {
		if strings.HasPrefix(a, "npm view ") {
			views++
		}
	}
	if views != 1 {
		t.Errorf("%d registry lookups %v, want 1", views, f.argvs())
	}
}

// On Windows makeit's npm prefix is ~/.local/bin and the packages sit in <prefix>/node_modules.
func TestNPMUpgradeWindowsLayout(t *testing.T) {
	e, _ := newEnv(t, "node", "npm")
	e.Platform.OS = "windows"
	prefix := filepath.Join(e.Roots.Home, ".local", "bin")
	dir := filepath.Join(prefix, "node_modules", "marked")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"version":"18.1.3"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	e.st.Prereqs = map[string]*state.Prereq{"marked": {InstalledBy: "makeit", Route: "npm", Argv: []string{"npm", "install", "-g", "--prefix", prefix, "marked"}}}
	if _, ok := npmStep(t, e, "marked"); !ok {
		t.Error("no upgrade step for a Windows install")
	}
}
