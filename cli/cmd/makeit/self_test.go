package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"makeit/internal/buildinfo"
	"makeit/internal/cli"
	"makeit/internal/execx"
	"makeit/internal/fsx"
	"makeit/internal/minisign/minisigntest"
	"makeit/internal/paths"
	"makeit/internal/selfupdate"
	"makeit/internal/selfupdate/releasetest"
	"makeit/internal/state"
)

// byContent answers `<file> version --json` with the version the file holds ("makeit 1.2.0").
func byContent(_ context.Context, c execx.Cmd) (execx.Result, error) {
	b, err := os.ReadFile(c.Argv[0])
	if err != nil {
		return execx.Result{Code: 1}, nil
	}
	v := strings.TrimPrefix(string(b), "makeit ")
	return execx.Result{Stdout: `{"version":"` + v + `","target":"` + buildinfo.Target() + `","repo":"o/makeit","keys":["ABC"]}`}, nil
}

// selfMachine replaces selfEnv with a temp machine: makeit 1.1.0 at ~/.local/bin/makeit, trusting
// k, online against srv (latest 1.2.0, signed by k). receipt says whether its script installed it.
func selfMachine(t *testing.T, receipt bool) (selfupdate.Env, *releasetest.Server) {
	t.Helper()
	k := minisigntest.NewKey()
	srv := releasetest.NewServer("1.2.0")
	t.Cleanup(srv.Close)
	srv.Add(releasetest.Files(k, "1.2.0", buildinfo.Target(), []byte("makeit 1.2.0")))
	home := t.TempDir()
	roots := paths.Roots{Home: home, Claude: filepath.Join(home, ".claude"), Stack: filepath.Join(home, ".makeit")}
	exe := filepath.Join(home, ".local", "bin", "makeit")
	write(t, exe, "makeit 1.1.0")
	e := selfupdate.Env{Roots: roots, Exe: exe, Version: "1.1.0", Target: buildinfo.Target(), Repo: "o/makeit",
		Keys: []string{k.Public()}, Base: srv.URL, Client: srv.Client(), Run: byContent,
		Getenv: func(string) string { return "" }, Now: time.Now}
	if err := os.MkdirAll(roots.Stack, 0o700); err != nil {
		t.Fatal(err)
	}
	if receipt {
		saveReceipt(t, e, state.Receipt{Method: "script", BinPath: roots.Portable(exe), Version: "1.1.0", Target: e.Target})
	}
	useSelfEnv(t, e)
	return e, srv
}

func useSelfEnv(t *testing.T, e selfupdate.Env) {
	t.Helper()
	saved := selfEnv
	selfEnv = func(context.Context) (selfupdate.Env, error) { return e, nil }
	t.Cleanup(func() { selfEnv = saved })
}

func saveReceipt(t *testing.T, e selfupdate.Env, r state.Receipt) {
	t.Helper()
	if err := state.SaveReceipt(e.ReceiptPath(), &r); err != nil {
		t.Fatal(err)
	}
}

func loadReceipt(t *testing.T, e selfupdate.Env) *state.Receipt {
	t.Helper()
	r, _, err := state.LoadReceipt(e.ReceiptPath())
	if err != nil || r == nil {
		t.Fatalf("receipt %v %v", r, err)
	}
	return r
}

func content(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// rolledForward is selfMachine after an update to 1.2.0, seen from the new binary.
func rolledForward(t *testing.T) selfupdate.Env {
	t.Helper()
	e, _ := selfMachine(t, true)
	write(t, filepath.Join(e.Roots.Stack, "self", "makeit-1.1.0"), "makeit 1.1.0")
	write(t, e.Exe, "makeit 1.2.0")
	prev := "1.1.0"
	saveReceipt(t, e, state.Receipt{Method: "script", BinPath: e.Roots.Portable(e.Exe), Version: "1.2.0", Target: e.Target, Previous: &prev})
	e.Version = "1.2.0"
	useSelfEnv(t, e)
	return e
}

// The delegate's test: a default build refuses even a correctly signed release, before any request.
// The delegate's test, moved from self update to update's self step: a default build refuses even a
// correctly signed release, before any request.
func TestUpdateDefaultBuildRefusesSelf(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MAKEIT_DIR", filepath.Join(os.Getenv("HOME"), ".makeit"))
	machine, srv := selfMachine(t, true)
	e, err := defaultSelfEnv(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Keys) != 0 || e.Version != buildinfo.Version {
		t.Fatalf("not the default build: keys %v, version %q", e.Keys, e.Version)
	}
	// Only the machine is faked; the version, repository and keys are the build's own.
	e.Roots, e.Exe, e.Base, e.Client, e.Run = machine.Roots, machine.Exe, machine.Base, machine.Client, machine.Run
	useSelfEnv(t, e)
	i := slices.IndexFunc(commands, func(c cli.Command) bool { return c.Name == "update" })
	p, err := cli.Parse(commands[i], []string{"--yes"})
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if o := selfStep(context.Background(), p, []string{"update", "--yes"}, &out, &errb); o.exited {
		t.Errorf("the self step swapped a default build: %+v, stderr %q", o, errb.String())
	}
	if got := content(t, e.Exe); got != "makeit 1.1.0" {
		t.Errorf("binary %q", got)
	}
	if got := srv.Requests(); len(got) > 0 {
		t.Errorf("requests %v", got)
	}
}

// makeit never rolls itself back to a release without the daily update.
func TestRollbackNeverBelow020(t *testing.T) {
	e, _ := selfMachine(t, true)
	write(t, filepath.Join(e.Roots.Stack, "self", "makeit-0.1.1"), "makeit 0.1.1")
	write(t, e.Exe, "makeit 0.2.0")
	prev := "0.1.1"
	saveReceipt(t, e, state.Receipt{Method: "script", BinPath: e.Roots.Portable(e.Exe), Version: "0.2.0", Target: e.Target, Previous: &prev})
	e.Version = "0.2.0"
	useSelfEnv(t, e)
	code, _, errOut := call(t, noGather(t), "rollback", "--yes")
	if code != 0 || !strings.Contains(errOut, "makeit stays at 0.2.0") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
	if content(t, e.Exe) != "makeit 0.2.0" {
		t.Errorf("binary %q, want 0.2.0 kept", content(t, e.Exe))
	}
}

// A daily update that replaced only makeit wrote no generation: rollback undoes that update, not the
// setup before it.
func TestRollbackBinaryOnlyUpdate(t *testing.T) {
	e, _ := selfMachine(t, true)
	g, err := state.NewGeneration(e.Roots.Stack, 0, "setup", []string{"setup"})
	if err != nil {
		t.Fatal(err)
	}
	g.Manifest.StartedAt = time.Now().Add(-2 * time.Hour).UTC()
	if err := g.Save(); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(e.Roots.Stack, "self", "makeit-0.2.0"), "makeit 0.2.0")
	write(t, e.Exe, "makeit 0.3.0")
	prev := "0.2.0"
	saveReceipt(t, e, state.Receipt{Method: "script", BinPath: e.Roots.Portable(e.Exe), Version: "0.3.0", Target: e.Target,
		Previous: &prev, InstalledAt: time.Now().Add(-10 * time.Minute).UTC()})
	e.Version = "0.3.0"
	useSelfEnv(t, e)
	code, _, errOut := call(t, noGather(t), "rollback", "--yes")
	if code != 0 || strings.Contains(errOut, "Undo the setup") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if content(t, e.Exe) != "makeit 0.2.0" {
		t.Errorf("binary %q, want 0.2.0 back", content(t, e.Exe))
	}
	gens, _ := state.LoadGenerations(e.Roots.Stack)
	if len(gens) != 1 || gens[0].Manifest.RolledBack {
		t.Errorf("the setup was rolled back: %d generations", len(gens))
	}
	e.Version = "0.2.0"
	useSelfEnv(t, e)
	call(t, noGather(t), "rollback", "--yes")
	if content(t, e.Exe) != "makeit 0.2.0" {
		t.Errorf("a second rollback swapped makeit back to %q", content(t, e.Exe))
	}
}

func TestSelfFlags(t *testing.T) {
	selfMachine(t, true)
	for _, args := range [][]string{
		{"self"}, {"self", "fly"}, {"self", "install", "update"},
		{"self", "install", "--to", "1.0.0"}, {"self", "update", "--prefix", "/x"},
		{"self", "update", "--allow-downgrade"}, {"self", "rollback", "--to", "1.0.0"},
	} {
		if code, _, errOut := call(t, noGather(t), args...); code != 2 {
			t.Errorf("%v: exit %d, stderr %q", args, code, errOut)
		}
	}
}

func TestSelfInstallYes(t *testing.T) {
	e, _ := selfMachine(t, false)
	e.Exe = filepath.Join(t.TempDir(), "downloaded")
	write(t, e.Exe, "makeit 1.1.0")
	useSelfEnv(t, e)
	prefix := filepath.Join(t.TempDir(), "bin")
	code, _, errOut := call(t, noGather(t), "self", "install", "--yes", "--prefix", prefix)
	target := filepath.Join(prefix, "makeit"+map[bool]string{true: ".exe"}[strings.HasPrefix(e.Target, "windows-")])
	if code != 0 || !strings.Contains(errOut, "makeit 1.1.0 installed at "+target) || content(t, target) != "makeit 1.1.0" {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if r := loadReceipt(t, e); r.BinPath != e.Roots.Portable(target) || r.Method != "script" {
		t.Errorf("receipt %+v", r)
	}
}

func TestSelfInstallForeign(t *testing.T) {
	e, _ := selfMachine(t, false)
	e.Exe = filepath.Join(t.TempDir(), "downloaded")
	write(t, e.Exe, "makeit 1.1.0")
	useSelfEnv(t, e)
	prefix := t.TempDir()
	target := filepath.Join(prefix, "makeit")
	write(t, target, "someone else's")
	code, _, errOut := call(t, noGather(t), "self", "install", "--yes", "--prefix", prefix)
	if code != 1 || !strings.Contains(errOut, "is not makeit's") || content(t, target) != "someone else's" {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
}

func TestSelfInstallPathLine(t *testing.T) {
	e, _ := selfMachine(t, false)
	e.Exe = filepath.Join(t.TempDir(), "downloaded")
	write(t, e.Exe, "makeit 1.1.0")
	useSelfEnv(t, e)
	prefix := t.TempDir()
	_, _, errOut := call(t, noGather(t), "self", "install", "--yes", "--prefix", prefix)
	if want := "add " + prefix + " to PATH, for example: export PATH=\"" + prefix + ":$PATH\""; !strings.Contains(errOut, want) {
		t.Errorf("stderr %q, want %q", errOut, want)
	}
}

func TestSelfRollbackYes(t *testing.T) {
	e := rolledForward(t)
	code, _, errOut := call(t, noGather(t), "rollback", "--yes")
	if code != 0 || !strings.Contains(errOut, "makeit rolled back from 1.2.0 to 1.1.0; 1.2.0 is skipped until a newer release or `makeit update --to 1.2.0`") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if r := loadReceipt(t, e); r.Version != "1.1.0" || r.Skipped != "1.2.0" || content(t, e.Exe) != "makeit 1.1.0" {
		t.Errorf("receipt %+v, binary %q", r, content(t, e.Exe))
	}
}

func TestSelfRollbackNothing(t *testing.T) {
	selfMachine(t, true)
	if code, _, errOut := call(t, noGather(t), "rollback", "--yes"); code != 1 || !strings.Contains(errOut, "nothing to roll back to") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
}

func TestSelfRollbackDryRun(t *testing.T) {
	e := rolledForward(t)
	code, _, errOut := call(t, noGather(t), "rollback", "--dry-run")
	if code != 0 || !strings.Contains(errOut, "would roll makeit back from 1.2.0 to 1.1.0") || content(t, e.Exe) != "makeit 1.2.0" {
		t.Errorf("exit %d, stderr %q, binary %q", code, errOut, content(t, e.Exe))
	}
}

// Answering No to rollback leaves everything as it is, makeit itself included, even when the update
// being undone also installed the running makeit.
func TestRollbackDeclinedKeepsBinary(t *testing.T) {
	exe, file := rollbackAnswered(t, "n")
	if content(t, exe) != "makeit 1.2.0" {
		t.Errorf("makeit was rolled back to %q although the answer was No", content(t, exe))
	}
	if content(t, file) != "after the update" {
		t.Errorf("a file was rolled back although the answer was No")
	}
}

func TestRollbackAcceptedMovesBinary(t *testing.T) {
	exe, file := rollbackAnswered(t, "y")
	if content(t, exe) != "makeit 1.1.0" || content(t, file) != "before the update" {
		t.Errorf("after Yes: makeit %q, file %q; want both rolled back", content(t, exe), content(t, file))
	}
}

// rollbackAnswered sets up an update that wrote a file and installed makeit 1.2.0 over 1.1.0, then
// runs rollback at a (plain) terminal answering a; it returns makeit's path and the file.
func rollbackAnswered(t *testing.T, a string) (string, string) {
	t.Helper()
	m := newCatMachine(t, nil)
	if code, _, errOut := run2(t, "setup", "--yes"); code != 0 {
		t.Fatalf("setup: exit %d\n%s", code, errOut)
	}
	st, _, _ := state.LoadState(filepath.Join(m.stack, "state.json"))
	g, err := state.NewGeneration(m.stack, st.Generation, "update", []string{"update"})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(m.home, "touched.txt")
	write(t, file, "after the update")
	rel, err := g.PreImage([]byte("before the update"))
	if err != nil {
		t.Fatal(err)
	}
	pre, post := fsx.Sha256([]byte("before the update")), fsx.Sha256([]byte("after the update"))
	if err := g.Record(state.Op{ID: "global.touched", Kind: "write", Path: file, PreSha256: &pre, PostSha256: &post, PreImage: rel}); err != nil {
		t.Fatal(err)
	}
	if err := g.Save(); err != nil {
		t.Fatal(err)
	}
	// The update ran as makeit 1.2.0 (a development build never updates itself).
	mf := filepath.Join(g.Dir, "manifest.json")
	b, _ := os.ReadFile(mf)
	write(t, mf, strings.Replace(string(b), `"cliVersion": "`+buildinfo.Version+`"`, `"cliVersion": "1.2.0"`, 1))
	roots := paths.Roots{Home: m.home, Claude: filepath.Join(m.home, ".claude"), Stack: m.stack}
	exe := filepath.Join(m.home, ".local", "bin", "makeit")
	write(t, exe, "makeit 1.2.0")
	write(t, filepath.Join(m.stack, "self", "makeit-1.1.0"), "makeit 1.1.0")
	e := selfupdate.Env{Roots: roots, Exe: exe, Version: "1.2.0", Target: buildinfo.Target(), Repo: "o/makeit", Keys: []string{"ABC"},
		Run: byContent, Getenv: func(string) string { return "" }, Now: time.Now}
	prev := "1.1.0"
	saveReceipt(t, e, state.Receipt{Method: "script", BinPath: roots.Portable(exe), Version: "1.2.0", Target: e.Target,
		Previous: &prev, InstalledAt: g.Manifest.StartedAt.Add(-time.Minute)})
	useSelfEnv(t, e)
	if gens, _ := state.LoadGenerations(m.stack); len(gens) == 0 || !binaryMovedIn(e, gens[0].Manifest) {
		rec, gerr := e.Gate()
		t.Fatalf("precondition: the update did not move makeit in (receipt %+v, %v; generations %d)", rec, gerr, len(gens))
	}
	savedTTY, savedStdin := isTTY, os.Stdin
	t.Cleanup(func() { isTTY, os.Stdin = savedTTY, savedStdin })
	isTTY = func(io.Writer) bool { return true }
	t.Setenv("CI", "1")
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.WriteString(a + "\n")
	_ = w.Close()
	os.Stdin = r
	code, _, errOut := call(t, noGather(t), "rollback")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	return exe, file
}

// When the makeit before this one is older than 0.2.0, it is never brought back, so the binary is
// not the last thing to undo and rollback goes on to the runs that wrote files.
func TestBinaryIsLastNotBelowFloor(t *testing.T) {
	e, _ := selfMachine(t, true)
	write(t, e.Exe, "makeit 0.2.0")
	e.Version = "0.2.0"
	for _, tc := range []struct {
		prev string
		want bool
	}{{"0.1.1", false}, {"0.2.0", true}} {
		prev := tc.prev
		saveReceipt(t, e, state.Receipt{Method: "script", BinPath: e.Roots.Portable(e.Exe), Version: "0.2.0", Target: e.Target, Previous: &prev, InstalledAt: time.Now().UTC()})
		if got := binaryIsLast(e, nil); got != tc.want {
			t.Errorf("previous %s: binaryIsLast %v, want %v", tc.prev, got, tc.want)
		}
	}
}

// The daily run updates makeit hours or days after it was installed. Rolling that update back
// must bring the previous makeit back too: its receipt dates from the swap, not the first install.
func TestRollbackAfterAgedUpdate(t *testing.T) {
	m := newCatMachine(t, nil)
	if code, _, errOut := run2(t, "setup", "--yes"); code != 0 {
		t.Fatalf("setup: exit %d\n%s", code, errOut)
	}
	k := minisigntest.NewKey()
	srv := releasetest.NewServer("1.2.0")
	t.Cleanup(srv.Close)
	srv.Add(releasetest.Files(k, "1.2.0", buildinfo.Target(), []byte("makeit 1.2.0")))
	roots := paths.Roots{Home: m.home, Claude: filepath.Join(m.home, ".claude"), Stack: m.stack}
	exe := filepath.Join(m.home, ".local", "bin", "makeit")
	write(t, exe, "makeit 1.1.0")
	e := selfupdate.Env{Roots: roots, Exe: exe, Version: "1.1.0", Target: buildinfo.Target(), Repo: "o/makeit",
		Keys: []string{k.Public()}, Base: srv.URL, Client: srv.Client(), Run: byContent,
		Getenv: func(string) string { return "" }, Now: time.Now}
	saveReceipt(t, e, state.Receipt{Method: "script", BinPath: roots.Portable(exe), Version: "1.1.0", Target: e.Target,
		InstalledAt: time.Now().Add(-3 * time.Hour).UTC()})
	if _, err := e.Update(context.Background(), "1.2.0"); err != nil {
		t.Fatal(err)
	}
	// The new makeit then ran the stack update and made a generation that wrote a file.
	st, _, _ := state.LoadState(filepath.Join(m.stack, "state.json"))
	g, err := state.NewGeneration(m.stack, st.Generation, "update", []string{"update", "--auto"})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(m.home, "touched.txt")
	write(t, file, "after")
	rel, err := g.PreImage([]byte("before"))
	if err != nil {
		t.Fatal(err)
	}
	pre, post := fsx.Sha256([]byte("before")), fsx.Sha256([]byte("after"))
	if err := g.Record(state.Op{ID: "global.touched", Kind: "write", Path: file, PreSha256: &pre, PostSha256: &post, PreImage: rel}); err != nil {
		t.Fatal(err)
	}
	if err := g.Save(); err != nil {
		t.Fatal(err)
	}
	mf := filepath.Join(g.Dir, "manifest.json")
	b, _ := os.ReadFile(mf)
	write(t, mf, strings.Replace(string(b), `"cliVersion": "`+buildinfo.Version+`"`, `"cliVersion": "1.2.0"`, 1))
	e.Version = "1.2.0"
	useSelfEnv(t, e)
	code, _, errOut := call(t, noGather(t), "rollback", "--yes")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	if content(t, exe) != "makeit 1.1.0" {
		t.Errorf("makeit is %q after the rollback, want 1.1.0 back\n%s", content(t, exe), errOut)
	}
	if content(t, file) != "before" {
		t.Errorf("the file was not rolled back")
	}
}

// Distro suite, 2026-11-04: setup finished, then makeit was replaced seconds later, then an update
// left a run with nothing to undo. makeit is the last change: its swap came after the newest run
// rollback can undo had finished, even though that run started less than a minute earlier.
func TestBinaryIsLastAfterAFinishedRun(t *testing.T) {
	e, _ := selfMachine(t, true)
	write(t, e.Exe, "makeit 0.3.2")
	e.Version = "0.3.2"
	prev := "0.3.1"
	started := time.Now().UTC().Add(-20 * time.Second)
	g := &state.Generation{Manifest: state.Manifest{Generation: 1, Command: "setup", StartedAt: started, FinishedAt: started.Add(10 * time.Second)}}
	saveReceipt(t, e, state.Receipt{Method: "script", BinPath: e.Roots.Portable(e.Exe), Version: "0.3.2", Target: e.Target, Previous: &prev, InstalledAt: started.Add(15 * time.Second)})
	if !binaryIsLast(e, g) {
		t.Error("the swap after the finished setup is not taken as the last change")
	}
	// An update's own swap comes before its run starts: that run, not the swap, is the last change.
	g.Manifest.Command, g.Manifest.StartedAt, g.Manifest.FinishedAt = "update", started.Add(16*time.Second), started.Add(19*time.Second)
	if binaryIsLast(e, g) {
		t.Error("an update's swap is taken as later than its own run")
	}
}
