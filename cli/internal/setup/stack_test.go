package setup

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"makeit/internal/claudecli"
	"makeit/internal/execx"
	"makeit/internal/plan"
	"makeit/internal/state"
)

func text(out string) func(execx.Cmd) execx.Result {
	return func(execx.Cmd) execx.Result { return execx.Result{Stdout: out} }
}

const ok = `{"command":"x","outcome":"ok"}`

// stackEnv is newEnv with a usable claude and the embedded catalog's names.
func stackEnv(t *testing.T, markets, plugins string) (*env, *fakeRunner) {
	t.Helper()
	e, f := newEnv(t)
	e.claude = &claudecli.CLI{Path: "claude", Run: f.run}
	e.req.Marketplace, e.req.Bundle = "makeit", "makeit-bundle@makeit"
	f.answers["claude plugin marketplace list --json"] = text(markets)
	f.answers["claude plugin list --json"] = text(plugins)
	return e, f
}

func step(t *testing.T, steps []plan.Step, id string) plan.Step {
	t.Helper()
	for _, s := range steps {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("no step %s", id)
	return plan.Step{}
}

func TestStackStepOrder(t *testing.T) {
	e, _ := stackEnv(t, "[]", "[]")
	var ids []string
	for _, s := range e.stackSteps() {
		ids = append(ids, s.ID)
	}
	if want := []string{"catalog", "marketplace.official", "marketplace", "bundle"}; !slices.Equal(ids, want) {
		t.Errorf("steps %v, want %v", ids, want)
	}
}

func TestStackWaitsForClaude(t *testing.T) {
	e, _ := stackEnv(t, "[]", "[]")
	e.claude = nil
	for _, s := range e.stackSteps() {
		if _, err := s.Already(context.Background()); !errors.Is(err, plan.ErrNotYet) {
			t.Errorf("%s: %v, want ErrNotYet before Claude Code is installed", s.ID, err)
		}
	}
}

func TestMarketplaceAdopted(t *testing.T) {
	e, f := stackEnv(t, `[{"name":"makeit","source":"directory","path":"/repo"},{"name":"claude-plugins-official","source":"github"}]`, "[]")
	steps := e.stackSteps()
	for _, id := range []string{"marketplace", "catalog", "marketplace.official"} {
		if done, err := step(t, steps, id).Already(context.Background()); !done || err != nil {
			t.Errorf("%s already = %v, %v", id, done, err)
		}
	}
	if m := e.st.Marketplaces["makeit"]; m == nil || m.AddedBy != "user" {
		t.Errorf("state %+v, want adopted as the user's", m)
	}
	// The plan shows these steps as already done, so a kept line says why, and how to hand over.
	if i := slices.IndexFunc(e.kept, func(k Kept) bool { return k.Item == "marketplace makeit" }); i < 0 ||
		!strings.Contains(e.kept[i].Reason, "claude plugin marketplace remove makeit") {
		t.Errorf("kept %+v, want the adopted marketplace named with the command that hands it to makeit", e.kept)
	}
	for _, c := range f.argvs() {
		if strings.Contains(c, "marketplace add") {
			t.Errorf("adopted marketplace was added again: %v", f.argvs())
		}
	}
}

func TestMarketplaceAdded(t *testing.T) {
	e, f := stackEnv(t, "[]", "[]")
	dir := filepath.Join(e.Roots.Stack, "catalog")
	f.answers["claude plugin marketplace add "+dir+" --scope user --json"] = text(ok)
	s := step(t, e.stackSteps(), "marketplace")
	if done, _ := s.Already(context.Background()); done {
		t.Fatal("missing marketplace counts as present")
	}
	if _, err := s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m := e.st.Marketplaces["makeit"]; m == nil || m.AddedBy != "makeit" || m.Kind != "directory" {
		t.Errorf("state %+v", m)
	}
}

func TestCatalogSynced(t *testing.T) {
	e, _ := stackEnv(t, "[]", "[]")
	s := step(t, e.stackSteps(), "catalog")
	if done, _ := s.Already(context.Background()); done {
		t.Fatal("empty catalog dir counts as synced")
	}
	if _, err := s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if done, err := s.Already(context.Background()); !done || err != nil {
		t.Errorf("after sync: %v, %v", done, err)
	}
}

func TestBundleRefusesShownCommand(t *testing.T) {
	e, f := stackEnv(t, "[]", "[]")
	f.answers["claude plugin install makeit-bundle@makeit --scope user --json"] = text(
		`{"command":"install","outcome":"failed","shownCommand":{"sha256":"abc123","acceptCommandMatched":false}}`)
	_, err := step(t, e.stackSteps(), "bundle").Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "abc123") {
		t.Errorf("err = %v, want the refused command's sha256", err)
	}
	for _, c := range f.argvs() {
		if strings.Contains(c, " -y") || strings.Contains(c, "--accept-command") {
			t.Errorf("accepted a command on the user's behalf: %v", c)
		}
	}
}

func TestBundleInstalled(t *testing.T) {
	e, f := stackEnv(t, "[]", "[]")
	f.answers["claude plugin install makeit-bundle@makeit --scope user --json"] = func(execx.Cmd) execx.Result {
		f.answers["claude plugin list --json"] = text(`[{"id":"makeit-bundle@makeit","version":"3.10.0","enabled":true}]`)
		return execx.Result{Stdout: ok}
	}
	s := step(t, e.stackSteps(), "bundle")
	if _, err := s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p := e.st.Plugins["makeit-bundle@makeit"]; p == nil || p.InstalledBy != "makeit" || p.Observed.Version != "3.10.0" {
		t.Errorf("state %+v", p)
	}
	if done, _ := s.Already(context.Background()); !done {
		t.Error("installed bundle not seen as installed")
	}
}

// A marketplace makeit added itself is not adopted: setup keeps its catalog in sync and repairs
// a deleted one (SETUP.md skips the catalog only for an adopted marketplace).
func TestCatalogResyncedWhenMakeitAddedIt(t *testing.T) {
	e, _ := stackEnv(t, `[{"name":"makeit","source":"directory"}]`, "[]")
	e.st.Marketplaces = map[string]*state.Marketplace{"makeit": {AddedBy: "makeit", Kind: "directory"}}
	if done, err := step(t, e.stackSteps(), "catalog").Already(context.Background()); done || err != nil {
		t.Errorf("already = %v, %v; want a sync of the missing catalog", done, err)
	}
}

// broughtIn installs the bundle with makeit's marketplace at a local catalog: x@other was there
// before; the install adds the bundle, a@makeit (pinned at aaa) and b@makeit.
func broughtIn(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".claude-plugin", "marketplace.json"), `{"name": "makeit", "plugins": [
		{"name": "makeit-bundle", "source": "./plugins/makeit-bundle"},
		{"name": "a", "source": {"source": "github", "repo": "o/a", "sha": "aaa"}},
		{"name": "b", "source": "./plugins/b"}]}`)
	e, f := stackEnv(t, `[{"name":"makeit","source":"directory","path":"`+dir+`"}]`, `[{"id":"x@other","enabled":true}]`)
	f.answers["claude plugin install makeit-bundle@makeit --scope user --json"] = func(execx.Cmd) execx.Result {
		f.answers["claude plugin list --json"] = text(`[{"id":"x@other","enabled":true},
			{"id":"makeit-bundle@makeit","version":"3.10.0","enabled":true,"readFromFolder":"` + dir + `/plugins/makeit-bundle"},
			{"id":"a@makeit","version":"1.2.0","enabled":true},{"id":"b@makeit","version":"1.0.0","enabled":true}]`)
		return execx.Result{Stdout: ok}
	}
	g, err := state.NewGeneration(e.Roots.Stack, 0, "setup", nil)
	if err != nil {
		t.Fatal(err)
	}
	e.gen = g
	if _, err := step(t, e.stackSteps(), "bundle").Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestBundleRecordsWhatItBroughtIn(t *testing.T) {
	e := broughtIn(t)
	for _, id := range []string{"makeit-bundle@makeit", "a@makeit", "b@makeit"} {
		if p := e.st.Plugins[id]; p == nil || p.InstalledBy != "makeit" || p.Scope != "user" {
			t.Errorf("%s recorded as %+v, want installed by makeit at user scope", id, p)
		}
	}
	if p := e.st.Plugins["x@other"]; p != nil {
		t.Errorf("x@other recorded as %+v; it was there before, so it stays the user's", p)
	}
	got := slices.Sorted(slices.Values(e.gen.Manifest.Plugins.Installed))
	if want := []string{"a@makeit", "b@makeit", "makeit-bundle@makeit"}; !slices.Equal(got, want) {
		t.Errorf("generation installed %v, want %v", got, want)
	}
}

func TestBundleRecordsPins(t *testing.T) {
	e := broughtIn(t)
	for id, pin := range map[string]string{"a@makeit": "aaa", "b@makeit": "", "makeit-bundle@makeit": ""} {
		if p := e.st.Plugins[id]; p == nil || p.Observed.GitCommitSha != pin {
			t.Errorf("%s pin = %+v, want %q", id, p, pin)
		}
	}
	if v := e.st.Plugins["a@makeit"].Observed.Version; v != "1.2.0" {
		t.Errorf("a@makeit version %q", v)
	}
}
