package setup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"makeit/internal/execx"
	"makeit/internal/platform"
	"makeit/internal/state"
)

// nodeTarball is Node's linux-x64 tarball for version v, its bin/node holding "node v".
func nodeTarball(t *testing.T, v string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	top := "node-v" + v + "-linux-x64/"
	add := func(h tar.Header, body string) {
		h.Size = int64(len(body))
		if h.Typeflag != tar.TypeReg {
			h.Size = 0
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(body))
	}
	add(tar.Header{Name: top + "bin/node", Typeflag: tar.TypeReg, Mode: 0o755}, "node "+v)
	add(tar.Header{Name: top + "lib/node_modules/npm/bin/npm-cli.js", Typeflag: tar.TypeReg, Mode: 0o755}, "npm "+v)
	add(tar.Header{Name: top + "lib/node_modules/npm/bin/npx-cli.js", Typeflag: tar.TypeReg, Mode: 0o755}, "npx "+v)
	add(tar.Header{Name: top + "bin/npm", Typeflag: tar.TypeSymlink, Linkname: "../lib/node_modules/npm/bin/npm-cli.js", Mode: 0o777}, "")
	add(tar.Header{Name: top + "bin/npx", Typeflag: tar.TypeSymlink, Linkname: "../lib/node_modules/npm/bin/npx-cli.js", Mode: 0o777}, "")
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

// nodeSite serves nodejs.org's dist for the LTS versions given, newest first; badSums serves a
// checksum file that does not match.
func nodeSite(t *testing.T, e *env, badSums bool, versions ...string) {
	t.Helper()
	files := map[string][]byte{}
	var index []string
	for _, v := range versions {
		index = append(index, `{"version":"v`+v+`","lts":"Jod"}`)
		tb := nodeTarball(t, v)
		sum := sha256.Sum256(tb)
		name := "node-v" + v + "-linux-x64.tar.gz"
		hexsum := hex.EncodeToString(sum[:])
		if badSums {
			hexsum = strings.Repeat("0", 64)
		}
		files["/dist/v"+v+"/"+name] = tb
		files["/dist/v"+v+"/SHASUMS256.txt"] = []byte(hexsum + "  " + name + "\n")
	}
	files["/dist/index.json"] = []byte(`[{"version":"v99.0.0","lts":false},` + strings.Join(index, ",") + `]`)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	e.Client, e.NodeBase = srv.Client(), srv.URL
}

func withGen(t *testing.T, e *env) {
	t.Helper()
	g, err := state.NewGeneration(e.Roots.Stack, 0, "setup", nil)
	if err != nil {
		t.Fatal(err)
	}
	e.gen = g
}

func nodeDir(e *env) string { return filepath.Join(e.Roots.Home, ".local", "share", "makeit", "node") }

func linkTarget(e *env, name string) string {
	p, _ := os.Readlink(filepath.Join(e.Roots.Home, ".local", "bin", name))
	if !filepath.IsAbs(p) {
		p = filepath.Join(e.Roots.Home, ".local", "bin", p)
	}
	return filepath.Clean(p)
}

// nodeEnv is an apt machine whose node reports version v ("" = no node).
func nodeEnv(t *testing.T, v string) (*env, *fakeRunner) {
	t.Helper()
	t.Setenv("PATH", t.TempDir()) // the route puts ~/.local/bin on the process PATH
	present := []string{"apt-get", "sudo", "git", "jq"}
	if v != "" {
		present = append(present, "node")
	}
	e, f := newEnv(t, present...)
	if v != "" {
		f.answers["node -v"] = func(execx.Cmd) execx.Result { return execx.Result{Stdout: "v" + v + "\n"} }
	}
	return e, f
}

func nodeStep(e *env) (found bool, detail string, argv []string) {
	for _, s := range e.prereqSteps() {
		if s.ID == "prereq.node" {
			return true, s.Detail, s.Command
		}
	}
	return false, "", nil
}

func TestNodeDistRoute(t *testing.T) {
	t.Run("apt, node missing", func(t *testing.T) {
		e, _ := nodeEnv(t, "")
		ok, detail, _ := nodeStep(e)
		if !ok || !strings.Contains(detail, "nodejs.org") {
			t.Errorf("step %v %q, want the nodejs.org route", ok, detail)
		}
	})
	t.Run("apt, node 18 counts as missing", func(t *testing.T) {
		e, _ := nodeEnv(t, "18.19.1")
		if ok, detail, _ := nodeStep(e); !ok || !strings.Contains(detail, "nodejs.org") {
			t.Errorf("step %v %q", ok, detail)
		}
	})
	t.Run("node 22.12 is present", func(t *testing.T) {
		e, _ := nodeEnv(t, "22.12.0")
		if ok, _, _ := nodeStep(e); ok {
			t.Error("a node inside the floor got a step")
		}
	})
	t.Run("node 20.18 is below the floor", func(t *testing.T) {
		e, _ := nodeEnv(t, "20.18.3")
		if ok, _, _ := nodeStep(e); !ok {
			t.Error("20.18 is below the floor but got no step")
		}
	})
	t.Run("old node installed by makeit's apt route is not removed by you", func(t *testing.T) {
		e, _ := nodeEnv(t, "18.19.1")
		e.st.Prereqs = map[string]*state.Prereq{"node": {InstalledBy: "makeit", Route: "apt"}}
		if ok, detail, _ := nodeStep(e); !ok || !strings.Contains(detail, "nodejs.org") {
			t.Errorf("step %v %q", ok, detail)
		}
	})
	for name, pm := range map[string]platform.PM{"apk": platform.APK, "brew": platform.Brew} {
		t.Run("not on "+name, func(t *testing.T) {
			e, _ := nodeEnv(t, "")
			e.Platform.PM = pm
			ok, detail, _ := nodeStep(e)
			if ok && strings.Contains(detail, "nodejs.org") {
				t.Errorf("the nodejs.org route on %s: %q", name, detail)
			}
		})
	}
	t.Run("not on winget", func(t *testing.T) {
		e, _ := nodeEnv(t, "")
		e.Platform = platform.Platform{OS: "windows", Arch: "amd64", PM: platform.WinGet}
		ok, detail, _ := nodeStep(e)
		if ok && strings.Contains(detail, "nodejs.org") {
			t.Errorf("the nodejs.org route on windows: %q", detail)
		}
	})
	t.Run("offline: no route, and an old node is left", func(t *testing.T) {
		e, _ := nodeEnv(t, "")
		e.Offline = true
		if _, detail, argv := nodeStep(e); strings.Contains(detail, "nodejs.org") || !slices.Contains(argv, "nodejs") {
			t.Errorf("offline detail %q argv %v, want the package route", detail, argv)
		}
		old, _ := nodeEnv(t, "18.19.1")
		old.Offline = true
		if ok, _, _ := nodeStep(old); ok {
			t.Error("offline with an old node planned a step")
		}
	})
	t.Run("a daily run plans no tool", func(t *testing.T) {
		e, _ := nodeEnv(t, "")
		e.Background = true
		if ok, _, _ := nodeStep(e); ok {
			t.Error("daily run planned node")
		}
	})
}

func TestNodeDistInstall(t *testing.T) {
	e, _ := nodeEnv(t, "18.19.1")
	nodeSite(t, e, false, "24.11.0", "22.21.0")
	withGen(t, e)
	out, err := stepByID(t, e, "prereq.node").Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if out != "v24.11.0" {
		t.Errorf("result %q", out)
	}
	if b, _ := os.ReadFile(filepath.Join(nodeDir(e), "24.11.0", "bin", "node")); string(b) != "node 24.11.0" {
		t.Errorf("bin/node %q", b)
	}
	for _, n := range []string{"node", "npm", "npx"} {
		want := filepath.Join(nodeDir(e), "24.11.0", "bin", n)
		if got := linkTarget(e, n); got != want {
			t.Errorf("link %s -> %s, want %s", n, got, want)
		}
	}
	// npm resolves through its own link to the tarball's file.
	if b, _ := os.ReadFile(filepath.Join(e.Roots.Home, ".local", "bin", "npm")); string(b) != "npm 24.11.0" {
		t.Errorf("npm link reads %q", b)
	}
	tb := nodeTarball(t, "24.11.0")
	sum := sha256.Sum256(tb)
	rec := e.st.Prereqs["node"]
	if rec == nil || rec.InstalledBy != "makeit" || rec.Route != "nodedist" || rec.Version != "24.11.0" || rec.Sha256 != hex.EncodeToString(sum[:]) {
		t.Errorf("record %+v", rec)
	}
	if !strings.HasPrefix(os.Getenv("PATH"), filepath.Join(e.Roots.Home, ".local", "bin")) {
		t.Errorf("PATH %q does not start with ~/.local/bin, so npm routes later in the run would not find node", os.Getenv("PATH"))
	}
	if len(e.gen.Manifest.Ops) == 0 {
		t.Error("nothing recorded in the generation")
	}
}

func TestNodeDistChecksumMismatchWritesNothing(t *testing.T) {
	e, _ := nodeEnv(t, "")
	nodeSite(t, e, true, "24.11.0")
	withGen(t, e)
	if _, err := stepByID(t, e, "prereq.node").Run(context.Background()); err == nil {
		t.Fatal("a mismatching checksum was accepted")
	}
	if _, err := os.Stat(filepath.Join(e.Roots.Home, ".local")); err == nil {
		t.Error("the run wrote under ~/.local")
	}
	if e.st.Prereqs["node"] != nil || len(e.gen.Manifest.Ops) != 0 {
		t.Error("a record or an op was kept")
	}
}

func TestNodeDistKeepsAUsersLink(t *testing.T) {
	e, _ := nodeEnv(t, "")
	nodeSite(t, e, false, "24.11.0")
	withGen(t, e)
	bin := filepath.Join(e.Roots.Home, ".local", "bin")
	_ = os.MkdirAll(bin, 0o755)
	_ = os.WriteFile(filepath.Join(bin, "npx"), []byte("mine"), 0o755)
	if _, err := stepByID(t, e, "prereq.node").Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(bin, "npx")); string(b) != "mine" {
		t.Errorf("the user's npx was replaced: %q", b)
	}
	if !slices.ContainsFunc(e.kept, func(k Kept) bool { return k.Item == "node" && strings.Contains(k.Reason, "npx") }) {
		t.Errorf("kept %+v, want npx named", e.kept)
	}
}

// installedNode runs setup's route for version v and returns the env, ready for an update.
func installedNode(t *testing.T, v string, newer ...string) *env {
	t.Helper()
	e, _ := nodeEnv(t, "")
	nodeSite(t, e, false, v)
	withGen(t, e)
	if _, err := stepByID(t, e, "prereq.node").Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.Present = only("apt-get", "sudo", "git", "jq", "node")
	nodeSite(t, e, false, append(newer, v)...)
	e.nodeOnce = sync.Once{} // the next run looks the index up again
	withGen(t, e)
	return e
}

func nodeUpgrade(t *testing.T, e *env) (planStep, bool) {
	t.Helper()
	for _, s := range e.toolUpgradeSteps() {
		if s.ID == "upgrade.node" {
			return planStep{s.Already, s.Run}, true
		}
	}
	return planStep{}, false
}

type planStep struct {
	Already func(context.Context) (bool, error)
	Run     func(context.Context) (string, error)
}

func TestNodeUpdateRepointsAndKeepsPrevious(t *testing.T) {
	e := installedNode(t, "22.21.0", "24.11.0")
	s, ok := nodeUpgrade(t, e)
	if !ok {
		t.Fatal("no upgrade.node step")
	}
	if done, err := s.Already(context.Background()); done || err != nil {
		t.Fatalf("already %v %v, want an update", done, err)
	}
	if _, err := s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"node", "npm", "npx"} {
		if got, want := linkTarget(e, n), filepath.Join(nodeDir(e), "24.11.0", "bin", n); got != want {
			t.Errorf("link %s -> %s, want %s", n, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(nodeDir(e), "22.21.0", "bin", "node")); err != nil {
		t.Errorf("the previous version folder is gone: %v", err)
	}
	if rec := e.st.Prereqs["node"]; rec.Version != "24.11.0" {
		t.Errorf("record %+v", rec)
	}
	if !slices.Contains(e.changes, state.Change{Kind: "tool", Item: "node", From: "22.21.0", To: "24.11.0"}) {
		t.Errorf("changes %+v", e.changes)
	}
	// The next update drops the folder from two versions back.
	nodeSite(t, e, false, "26.0.0", "24.11.0", "22.21.0")
	s2, _ := nodeUpgrade(t, e)
	e.nodeOnce = sync.Once{} // a new run looks the index up again
	if _, err := s2.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(nodeDir(e), "22.21.0")); err == nil {
		t.Error("the folder from two versions back was kept")
	}
	if _, err := os.Stat(filepath.Join(nodeDir(e), "24.11.0")); err != nil {
		t.Errorf("the previous folder is gone: %v", err)
	}
}

func TestNodeUpdateCurrentIsNoStep(t *testing.T) {
	e := installedNode(t, "24.11.0")
	s, _ := nodeUpgrade(t, e)
	if done, err := s.Already(context.Background()); !done || err != nil {
		t.Errorf("already %v %v, want nothing to do", done, err)
	}
}

func TestNodeUpdateLookupFailureKeeps(t *testing.T) {
	e := installedNode(t, "22.21.0", "24.11.0")
	e.Client = &http.Client{Transport: failing{}}
	s, _ := nodeUpgrade(t, e)
	if done, err := s.Already(context.Background()); !done || err != nil {
		t.Errorf("already %v %v", done, err)
	}
	if !slices.ContainsFunc(e.kept, func(k Kept) bool { return k.Item == "node" }) {
		t.Errorf("kept %+v", e.kept)
	}
}

type failing struct{}

func (failing) RoundTrip(*http.Request) (*http.Response, error) { return nil, os.ErrDeadlineExceeded }

func TestNodeRollbackBringsBackThePreviousVersion(t *testing.T) {
	e := installedNode(t, "22.21.0", "24.11.0")
	s, _ := nodeUpgrade(t, e)
	if _, err := s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.gen.Manifest.Changes = e.changes
	if rep := runAll(t, e.rollbackSteps(e.gen)); rep.Failed() {
		t.Fatalf("rollback failed: %+v", rep.Outcomes)
	}
	if _, err := os.Stat(filepath.Join(nodeDir(e), "24.11.0")); err == nil {
		t.Error("rollback left the new version folder")
	}
	for _, n := range []string{"node", "npm", "npx"} {
		if got, want := linkTarget(e, n), filepath.Join(nodeDir(e), "22.21.0", "bin", n); got != want {
			t.Errorf("link %s -> %s, want %s", n, got, want)
		}
	}
	if rec := e.st.Prereqs["node"]; rec.Version != "22.21.0" {
		t.Errorf("record %+v", rec)
	}
	if !slices.Contains(e.st.ToolSkips["node"], "v24.11.0") {
		t.Errorf("skips %v, want v24.11.0", e.st.ToolSkips)
	}
}

func TestNodeRollbackOfTheFirstInstallRemovesItAll(t *testing.T) {
	e, _ := nodeEnv(t, "")
	nodeSite(t, e, false, "24.11.0")
	withGen(t, e)
	if _, err := stepByID(t, e, "prereq.node").Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rep := runAll(t, e.rollbackSteps(e.gen)); rep.Failed() {
		t.Fatalf("rollback failed: %+v", rep.Outcomes)
	}
	if _, err := os.Stat(nodeDir(e)); err == nil {
		t.Error("rollback left the node folder")
	}
	for _, n := range []string{"node", "npm", "npx"} {
		if _, err := os.Lstat(filepath.Join(e.Roots.Home, ".local", "bin", n)); err == nil {
			t.Errorf("rollback left the %s link", n)
		}
	}
}

func TestNodeUninstallRemovesOnlyMakeits(t *testing.T) {
	e := installedNode(t, "24.11.0")
	e.command = "uninstall"
	bin := filepath.Join(e.Roots.Home, ".local", "bin")
	// The user repointed npx.
	_ = os.Remove(filepath.Join(bin, "npx"))
	if err := os.Symlink("/usr/bin/true", filepath.Join(bin, "npx")); err != nil {
		t.Fatal(err)
	}
	var step *planStep
	for _, s := range e.toolSteps() {
		if s.ID == "tool.node" {
			step = &planStep{s.Already, s.Run}
		}
	}
	if step == nil {
		t.Fatal("no tool.node uninstall step")
	}
	if done, err := step.Already(context.Background()); done || err != nil {
		t.Fatalf("already %v %v", done, err)
	}
	if _, err := step.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(nodeDir(e)); err == nil {
		t.Error("the node folder stayed")
	}
	for _, n := range []string{"node", "npm"} {
		if _, err := os.Lstat(filepath.Join(bin, n)); err == nil {
			t.Errorf("the %s link stayed", n)
		}
	}
	if p, _ := os.Readlink(filepath.Join(bin, "npx")); p != "/usr/bin/true" {
		t.Errorf("the user's npx link was touched: %q", p)
	}
	if !slices.ContainsFunc(e.kept, func(k Kept) bool { return k.Item == "node" && strings.Contains(k.Reason, "npx") }) {
		t.Errorf("kept %+v, want npx reported", e.kept)
	}
	if e.st.Prereqs["node"] != nil {
		t.Error("the record stayed")
	}
}

// nodejs.org's node needs libatomic, which slim images lack: the route installs the package when
// the new node does not start, and refuses (naming the cause) when that does not help.
func TestNodeDistInstallsLibatomic(t *testing.T) {
	e, f := nodeEnv(t, "")
	nodeSite(t, e, false, "24.11.0")
	withGen(t, e)
	probe := filepath.Join(nodeDir(e), "24.11.0", "bin", "node") + " -v"
	broken := true
	f.answers[probe] = func(execx.Cmd) execx.Result {
		if broken {
			return execx.Result{Code: 127, Stderr: "node: error while loading shared libraries: libatomic.so.1: cannot open shared object file"}
		}
		return execx.Result{Stdout: "v24.11.0\n"}
	}
	f.answers["sudo -n env DEBIAN_FRONTEND=noninteractive apt-get install -y libatomic1"] = func(execx.Cmd) execx.Result {
		broken = false
		return execx.Result{}
	}
	if _, err := stepByID(t, e, "prereq.node").Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	calls := strings.Join(f.argvs(), "\n")
	if !strings.Contains(calls, "apt-get update") || !strings.Contains(calls, "apt-get install -y libatomic1") {
		t.Errorf("calls %v, want apt-get update and the libatomic1 install", f.argvs())
	}
	if e.st.Prereqs["node"] == nil {
		t.Error("no record after the install")
	}

	e2, f2 := nodeEnv(t, "")
	nodeSite(t, e2, false, "24.11.0")
	withGen(t, e2)
	f2.answers[filepath.Join(nodeDir(e2), "24.11.0", "bin", "node")+" -v"] = func(execx.Cmd) execx.Result {
		return execx.Result{Code: 127, Stderr: "libatomic.so.1 missing"}
	}
	if _, err := stepByID(t, e2, "prereq.node").Run(context.Background()); err == nil || !strings.Contains(err.Error(), "libatomic") {
		t.Errorf("err %v, want the failure to name libatomic", err)
	}
	if e2.st.Prereqs["node"] != nil {
		t.Error("a record for a node that does not start")
	}
}
