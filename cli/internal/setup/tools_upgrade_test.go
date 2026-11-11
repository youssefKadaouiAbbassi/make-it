package setup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"makeit/internal/execx"
	"makeit/internal/fsx"
	"makeit/internal/plan"
	"makeit/internal/state"
)

const prekAssetName = "prek-x86_64-unknown-linux-musl.tar.gz"

// prekReleases serves prek release archives by version, and answers git ls-remote with tags.
func prekReleases(t *testing.T, e *env, f *fakeRunner, tags string, bins map[string][]byte) {
	t.Helper()
	files := map[string][]byte{}
	for v, bin := range bins {
		archive := tarGzWith(t, "prek", bin)
		sum := sha256.Sum256(archive)
		files["/j178/prek/releases/download/v"+v+"/"+prekAssetName] = archive
		files["/j178/prek/releases/download/v"+v+"/"+prekAssetName+".sha256"] = []byte(hex.EncodeToString(sum[:]) + "  " + prekAssetName + "\n")
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
	e.Client, e.ReleaseBase = srv.Client(), srv.URL
	f.answers["git ls-remote --tags https://github.com/j178/prek.git"] = func(execx.Cmd) execx.Result {
		if tags == "" {
			return execx.Result{Code: 128, Stderr: "fatal: unable to access"}
		}
		return execx.Result{Stdout: tags}
	}
}

// installedPrek is a machine where makeit installed prek at version v with content bin.
func installedPrek(t *testing.T, v string, bin []byte, present ...string) (*env, *fakeRunner, string) {
	t.Helper()
	e, f := newEnv(t, append([]string{"apt-get", "git", "node", "jq", "prek"}, present...)...)
	path := filepath.Join(e.Roots.Home, ".local", "bin", "prek")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bin, 0o755); err != nil {
		t.Fatal(err)
	}
	e.st.Prereqs = map[string]*state.Prereq{"prek": {InstalledBy: "makeit", Route: "release", Version: v, Sha256: fsx.Sha256(bin)}}
	g, err := state.NewGeneration(e.Roots.Stack, 0, "update", nil)
	if err != nil {
		t.Fatal(err)
	}
	e.gen = g
	return e, f, path
}

func upgradeStep(t *testing.T, e *env, id string) (plan.Step, bool) {
	t.Helper()
	for _, s := range e.toolUpgradeSteps() {
		if s.ID == "upgrade."+id {
			return s, true
		}
	}
	return plan.Step{}, false
}

// A missing release tool is installed at its newest release, and the version is recorded.
func TestReleaseInstallUsesNewest(t *testing.T) {
	e, f := newEnv(t, "apt-get", "git", "node", "jq")
	prekReleases(t, e, f, "a\trefs/tags/v0.5.9\nb\trefs/tags/v0.6.0\n", map[string][]byte{"0.6.0": []byte("prek 0.6.0\n")})
	g, err := state.NewGeneration(e.Roots.Stack, 0, "setup", nil)
	if err != nil {
		t.Fatal(err)
	}
	e.gen = g
	if _, err := stepByID(t, e, "prereq.prek").Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(e.Roots.Home, ".local", "bin", "prek")); string(b) != "prek 0.6.0\n" {
		t.Errorf("installed %q", b)
	}
	if rec := e.st.Prereqs["prek"]; rec == nil || rec.Version != "0.6.0" {
		t.Errorf("record %+v, want version 0.6.0", rec)
	}
}

// A release tool makeit installed moves to the newest release through the generation, and the
// change names both versions.
func TestToolUpgradeMovesOwnedBinary(t *testing.T) {
	e, f, path := installedPrek(t, "0.5.9", []byte("prek 0.5.9\n"))
	prekReleases(t, e, f, "a\trefs/tags/v0.5.9\nb\trefs/tags/v0.6.0\n", map[string][]byte{"0.6.0": []byte("prek 0.6.0\n")})
	s, ok := upgradeStep(t, e, "prek")
	if !ok {
		t.Fatal("no upgrade step for prek")
	}
	if done, err := s.Already(context.Background()); done || err != nil {
		t.Fatalf("already %v %v, want an upgrade", done, err)
	}
	if _, err := s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "prek 0.6.0\n" {
		t.Errorf("binary %q", b)
	}
	if rec := e.st.Prereqs["prek"]; rec.Version != "0.6.0" || rec.Sha256 != fsx.Sha256([]byte("prek 0.6.0\n")) {
		t.Errorf("record %+v", rec)
	}
	if ops := e.gen.Manifest.Ops; len(ops) != 1 || ops[0].PreSha256 == nil {
		t.Errorf("ops %+v, want one write with the old binary kept for rollback", ops)
	}
	if want := (state.Change{Kind: "tool", Item: "prek", From: "0.5.9", To: "0.6.0"}); !slices.Contains(e.changes, want) {
		t.Errorf("changes %+v, want %+v", e.changes, want)
	}
}

// A binary changed since makeit wrote it is the user's: kept, said why.
func TestToolUpgradeKeepsChangedBinary(t *testing.T) {
	e, f, path := installedPrek(t, "0.5.9", []byte("prek 0.5.9\n"))
	if err := os.WriteFile(path, []byte("my own build\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	prekReleases(t, e, f, "b\trefs/tags/v0.6.0\n", map[string][]byte{"0.6.0": []byte("prek 0.6.0\n")})
	s, _ := upgradeStep(t, e, "prek")
	if done, _ := s.Already(context.Background()); !done {
		t.Fatal("a changed binary is planned for replacement")
	}
	if r := keptReason(e, "prek"); r != "changed since makeit wrote it" {
		t.Errorf("kept %q", r)
	}
}

// No network: nothing moves, nothing fails.
func TestToolUpgradeOffline(t *testing.T) {
	e, f, _ := installedPrek(t, "0.5.9", []byte("prek 0.5.9\n"))
	prekReleases(t, e, f, "", nil)
	s, _ := upgradeStep(t, e, "prek")
	if done, err := s.Already(context.Background()); !done || err != nil {
		t.Errorf("already %v %v, want true and no error offline", done, err)
	}
}

// Current already: no step runs.
func TestToolUpgradeCurrent(t *testing.T) {
	e, f, _ := installedPrek(t, "0.6.0", []byte("prek 0.6.0\n"))
	prekReleases(t, e, f, "b\trefs/tags/v0.6.0\n", nil)
	s, _ := upgradeStep(t, e, "prek")
	if done, _ := s.Already(context.Background()); !done {
		t.Error("a current tool is planned for an upgrade")
	}
}

// The daily run upgrades owned release binaries too: no password, no new tool.
func TestToolUpgradeInDailyRun(t *testing.T) {
	e, _, _ := installedPrek(t, "0.5.9", []byte("prek 0.5.9\n"))
	e.Background = true
	if _, ok := upgradeStep(t, e, "prek"); !ok {
		t.Error("the daily run has no upgrade step for prek")
	}
}

// Rollback puts the previous binary back, records its version, and skips the undone release.
func TestRollbackRestoresTool(t *testing.T) {
	e, f, path := installedPrek(t, "0.5.9", []byte("prek 0.5.9\n"))
	prekReleases(t, e, f, "a\trefs/tags/v0.5.9\nb\trefs/tags/v0.6.0\n", map[string][]byte{"0.6.0": []byte("prek 0.6.0\n")})
	s, _ := upgradeStep(t, e, "prek")
	if _, err := s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.gen.Manifest.Changes = e.changes
	g := e.gen
	rb, _, _ := installedPrek(t, "0.6.0", []byte("prek 0.6.0\n"))
	rb.Roots, rb.st = e.Roots, e.st
	rbGen, err := state.NewGeneration(e.Roots.Stack, 0, "rollback", nil)
	if err != nil {
		t.Fatal(err)
	}
	rb.gen = rbGen
	for _, st := range rb.rollbackSteps(g) {
		if st.Already != nil {
			if done, _ := st.Already(context.Background()); done {
				continue
			}
		}
		if _, err := st.Run(context.Background()); err != nil {
			t.Fatalf("%s: %v", st.ID, err)
		}
	}
	if b, _ := os.ReadFile(path); string(b) != "prek 0.5.9\n" {
		t.Errorf("binary after rollback %q", b)
	}
	if rec := rb.st.Prereqs["prek"]; rec.Version != "0.5.9" || rec.Sha256 != fsx.Sha256([]byte("prek 0.5.9\n")) {
		t.Errorf("record %+v", rec)
	}
	if !slices.Contains(rb.st.ToolSkips["prek"], "v0.6.0") {
		t.Errorf("skips %v, want v0.6.0", rb.st.ToolSkips)
	}
}

// The newest release of a tool makeit does not manage is recorded, and the step stays Already so
// the run makes no generation for it.
func TestToolLatestRecordsUnmanagedTool(t *testing.T) {
	cases := []struct {
		name       string
		tags       string
		offline    bool
		want       string
		wantRecord bool
	}{
		{"newer tag", "a\trefs/tags/v0.5.9\nb\trefs/tags/v0.6.0\n", false, "0.6.0", true},
		{"failed lookup", "", false, "", false},
		{"offline", "b\trefs/tags/v0.6.0\n", true, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, f := newEnv(t, "apt-get", "git", "node", "jq", "prek")
			prekReleases(t, e, f, c.tags, nil)
			e.Offline = c.offline
			s := e.toolLatestStep()
			done, err := s.Already(context.Background())
			if !done || err != nil {
				t.Fatalf("already %v %v, want true and no error", done, err)
			}
			if got := e.st.ToolLatest["prek"]; got != c.want {
				t.Errorf("ToolLatest[prek] = %q, want %q", got, c.want)
			}
			if e.recordChanged != c.wantRecord {
				t.Errorf("recordChanged = %v, want %v", e.recordChanged, c.wantRecord)
			}
		})
	}
}

// A record that already matches changes nothing, so no state write.
func TestToolLatestUnchangedRecordsNothing(t *testing.T) {
	e, f := newEnv(t, "apt-get", "git", "node", "jq", "prek")
	prekReleases(t, e, f, "b\trefs/tags/v0.6.0\n", nil)
	e.st.ToolLatest = map[string]string{"prek": "0.6.0"}
	if done, _ := e.toolLatestStep().Already(context.Background()); !done || e.recordChanged {
		t.Errorf("done %v recordChanged %v, want true and false", done, e.recordChanged)
	}
}

// The daily run's skip list applies to the recorded value.
func TestToolLatestRespectsSkips(t *testing.T) {
	e, f := newEnv(t, "apt-get", "git", "node", "jq", "prek")
	prekReleases(t, e, f, "a\trefs/tags/v0.5.9\nb\trefs/tags/v0.6.0\n", nil)
	e.Background = true
	e.st.ToolSkips = map[string][]string{"prek": {"v0.6.0"}}
	_, _ = e.toolLatestStep().Already(context.Background())
	if got := e.st.ToolLatest["prek"]; got != "0.5.9" {
		t.Errorf("ToolLatest[prek] = %q, want 0.5.9", got)
	}
}
