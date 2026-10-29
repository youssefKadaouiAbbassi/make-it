package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"makeit/internal/buildinfo"
	"makeit/internal/execx"
	"makeit/internal/minisign/minisigntest"
	"makeit/internal/paths"
	"makeit/internal/plan"
	"makeit/internal/selfupdate"
	"makeit/internal/selfupdate/releasetest"
	"makeit/internal/setup"
	"makeit/internal/state"
)

// childScript stands in for the new makeit: it answers `version --json` for 1.2.0, and otherwise
// prints what it was run with and exits 3.
const childScript = `#!/bin/sh
if [ "$*" = "version --json" ]; then
  echo '{"version":"1.2.0","target":"TARGET","repo":"o/makeit","keys":["X"]}'
  exit 0
fi
echo "child MAKEIT_REEXEC=$MAKEIT_REEXEC args=$*"
exit 3
`

// scriptMachine is makeit 1.1.0 installed by its script, trusting trusted, against a release 1.2.0
// signed by signer whose binary is childScript. The stack run uses fakeSetupEnv.
func scriptMachine(t *testing.T, signer, trusted minisigntest.Key) (selfupdate.Env, *releasetest.Server) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("a shell script stands in for the new makeit")
	}
	srv := releasetest.NewServer("1.2.0")
	t.Cleanup(srv.Close)
	srv.Add(releasetest.Files(signer, "1.2.0", buildinfo.Target(), []byte(strings.Replace(childScript, "TARGET", buildinfo.Target(), 1))))
	home := t.TempDir()
	roots := paths.Roots{Home: home, Claude: filepath.Join(home, ".claude"), Stack: filepath.Join(home, ".makeit")}
	exe := filepath.Join(home, ".local", "bin", "makeit")
	write(t, exe, "makeit 1.1.0")
	if err := os.MkdirAll(roots.Stack, 0o700); err != nil {
		t.Fatal(err)
	}
	e := selfupdate.Env{Roots: roots, Exe: exe, Version: "1.1.0", Target: buildinfo.Target(), Repo: "o/makeit",
		Keys: []string{trusted.Public()}, Base: srv.URL, Client: srv.Client(), Run: execx.Run,
		Getenv: func(string) string { return "" }, Now: time.Now}
	saveReceipt(t, e, state.Receipt{Method: "script", BinPath: roots.Portable(exe), Version: "1.1.0", Target: e.Target})
	useSelfEnv(t, e)
	fakeSetupEnv(t)
	return e, srv
}

// noStackRun makes the parent fail the test if it runs the stack update itself.
func noStackRun(t *testing.T) {
	t.Helper()
	saved := setupEnv
	setupEnv = func(context.Context) (setup.Env, error) {
		t.Error("the parent ran the stack update")
		return setup.Env{}, errors.New("not here")
	}
	t.Cleanup(func() { setupEnv = saved })
}

func TestUpdateReexecs(t *testing.T) {
	k := minisigntest.NewKey()
	e, _ := scriptMachine(t, k, k)
	noStackRun(t)
	code, out, errOut := call(t, noGather(t), "update", "--yes")
	if code != 3 || out != "child MAKEIT_REEXEC=1.1.0 args=update --yes\n" {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	if r := loadReceipt(t, e); r.Version != "1.2.0" {
		t.Errorf("receipt %+v", r)
	}
}

func TestUpdateReexecJSONParentSilent(t *testing.T) {
	k := minisigntest.NewKey()
	scriptMachine(t, k, k)
	noStackRun(t)
	code, out, _ := call(t, noGather(t), "update", "--yes", "--json")
	if code != 3 || out != "child MAKEIT_REEXEC=1.1.0 args=update --yes --json\n" {
		t.Errorf("exit %d, stdout %q: the parent wrote to stdout, or did not run the child", code, out)
	}
}

// Review Focus 5: without --yes, --json neither prompts, downloads nor swaps.
func TestUpdateJSONNoYesNoSwap(t *testing.T) {
	k := minisigntest.NewKey()
	e, srv := scriptMachine(t, k, k)
	code, out, _ := call(t, noGather(t), "update", "--json")
	var rep map[string]any
	decodeOne(t, out, &rep)
	if code != 2 || rep["notice"] != "would update makeit 1.1.0 → 1.2.0 first" {
		t.Errorf("exit %d, notice %v", code, rep["notice"])
	}
	if got := srv.Requests(); !slices.Equal(got, []string{"HEAD /o/makeit/releases/latest"}) {
		t.Errorf("requests %v, want only the HEAD", got)
	}
	if content(t, e.Exe) != "makeit 1.1.0" {
		t.Error("swapped without consent")
	}
}

func TestUpdateDryRunNoDownload(t *testing.T) {
	k := minisigntest.NewKey()
	_, srv := scriptMachine(t, k, k)
	code, _, errOut := call(t, noGather(t), "update", "--dry-run")
	if code != 0 || !strings.Contains(errOut, "would update makeit 1.1.0 → 1.2.0 first") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
	if got := srv.Requests(); len(got) != 1 {
		t.Errorf("requests %v, want only the HEAD", got)
	}
}

func TestUpdateSelfFailureContinues(t *testing.T) {
	trusted, other := minisigntest.NewKey(), minisigntest.NewKey()
	e, _ := scriptMachine(t, other, trusted)
	_, out, _ := call(t, noGather(t), "update", "--yes", "--json")
	var rep map[string]any
	decodeOne(t, out, &rep)
	notice, _ := rep["notice"].(string)
	if !strings.Contains(notice, "could not update makeit to 1.2.0:") || !strings.HasSuffix(notice, "; continuing with 1.1.0") {
		t.Errorf("notice %q", notice)
	}
	if _, ok := rep["plan"]; !ok {
		t.Errorf("no stack run: %v", rep)
	}
	if content(t, e.Exe) != "makeit 1.1.0" {
		t.Error("a release signed by an untrusted key was swapped in")
	}
}

func TestUpdateSkipLine(t *testing.T) {
	k := minisigntest.NewKey()
	e, srv := scriptMachine(t, k, k)
	saveReceipt(t, e, state.Receipt{Method: "script", BinPath: e.Roots.Portable(e.Exe), Version: "1.1.0", Skipped: "1.2.0"})
	code, out, errOut := call(t, noGather(t), "update", "--dry-run")
	if code != 0 || strings.Contains(out, "child") ||
		!strings.Contains(errOut, "makeit 1.2.0 is skipped (you rolled back from it): `makeit self update --to 1.2.0` takes it") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	if got := srv.Requests(); len(got) != 1 {
		t.Errorf("requests %v, want only the HEAD", got)
	}
}

func TestUpdateOtherPathNotice(t *testing.T) {
	k := minisigntest.NewKey()
	e, _ := scriptMachine(t, k, k)
	saveReceipt(t, e, state.Receipt{Method: "script", BinPath: "home:elsewhere/makeit", Version: "1.1.0"})
	_, _, errOut := call(t, noGather(t), "update", "--yes")
	if !strings.Contains(errOut, e.Roots.Abs("home:elsewhere/makeit")) || !strings.Contains(errOut, e.Exe) {
		t.Errorf("stderr %q does not name both paths", errOut)
	}
	if content(t, e.Exe) != "makeit 1.1.0" {
		t.Error("swapped although the receipt is for another copy")
	}
}

func TestUpdateChildReport(t *testing.T) {
	_, srv := selfMachine(t, true)
	fakeSetupEnv(t)
	t.Setenv("MAKEIT_REEXEC", "1.0.0")
	_, out, _ := call(t, noGather(t), "update", "--dry-run", "--json")
	var rep struct {
		Self map[string]string `json:"self"`
	}
	decodeOne(t, out, &rep)
	if rep.Self["from"] != "1.0.0" || rep.Self["to"] != buildinfo.Version {
		t.Errorf("self %v", rep.Self)
	}
	_, _, errOut := call(t, noGather(t), "update", "--dry-run")
	if first, _, _ := strings.Cut(errOut, "\n"); first != "makeit updated from 1.0.0 to "+buildinfo.Version {
		t.Errorf("first line %q", first)
	}
	if got := srv.Requests(); len(got) > 0 {
		t.Errorf("the child checked for a release again: %v", got)
	}
}

func TestWaitExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep and SIGTERM stand in for the child")
	}
	start := func(t *testing.T) *exec.Cmd {
		t.Helper()
		c := exec.Command("sleep", "30")
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Process.Kill(); _ = c.Wait() })
		return c
	}
	t.Run("interrupt with a child", func(t *testing.T) {
		c := start(t)
		done, sigs, got := make(chan int), make(chan os.Signal), make(chan int)
		cancelled := false
		go func() {
			got <- waitExit(done, sigs, func() { cancelled = true }, func() *os.Process { return c.Process })
		}()
		sigs <- syscall.SIGINT
		done <- 7
		if code := <-got; code != 7 || cancelled {
			t.Errorf("code %d, cancelled %v; want the child's 7, no cancel", code, cancelled)
		}
	})
	t.Run("terminate with a child", func(t *testing.T) {
		c := start(t)
		done, sigs, got := make(chan int), make(chan os.Signal), make(chan int)
		go func() { got <- waitExit(done, sigs, func() {}, func() *os.Process { return c.Process }) }()
		sigs <- syscall.SIGTERM
		err := c.Wait()
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ProcessState.Sys().(syscall.WaitStatus).Signal() != syscall.SIGTERM {
			t.Errorf("the child was not sent SIGTERM: %v", err)
		}
		done <- 5
		if code := <-got; code != 5 {
			t.Errorf("code %d, want the child's 5", code)
		}
	})
	t.Run("interrupt without a child", func(t *testing.T) {
		done, sigs, got := make(chan int), make(chan os.Signal), make(chan int)
		cancelled := false
		go func() { got <- waitExit(done, sigs, func() { cancelled = true }, func() *os.Process { return nil }) }()
		sigs <- syscall.SIGINT
		done <- 0
		if code := <-got; code != 130 || !cancelled {
			t.Errorf("code %d, cancelled %v; want 130 after a cancel", code, cancelled)
		}
	})
}

// The reviewer's probe: an interrupt while the new binary is checked ends update there, with no
// swap, no child and no stack run.
func TestUpdateInterruptedNoChild(t *testing.T) {
	k := minisigntest.NewKey()
	e, _ := scriptMachine(t, k, k)
	noStackRun(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.Run = func(context.Context, execx.Cmd) (execx.Result, error) {
		cancel()
		return execx.Result{Stdout: `{"version":"1.2.0","target":"` + buildinfo.Target() + `","repo":"o/makeit","keys":["X"]}`}, nil
	}
	useSelfEnv(t, e)
	var out, errb strings.Builder
	code := run(ctx, []string{"update", "--yes"}, &out, &errb, noGather(t))
	if code == 0 || out.Len() > 0 || content(t, e.Exe) != "makeit 1.1.0" {
		t.Errorf("exit %d, stdout %q, stderr %q, binary %q", code, out.String(), errb.String(), content(t, e.Exe))
	}
}

// Review Focus 5 without help from the stack: with the stack up to date and a newer makeit, update
// without --yes still exits 2 and says how to apply.
func TestUpdateConsentWhenOnlyMakeitIsBehind(t *testing.T) {
	k := minisigntest.NewKey()
	e, srv := scriptMachine(t, k, k)
	saved := stackUpdate
	stackUpdate = func(context.Context, setup.Env, plan.Mode, func([]plan.Outcome) (bool, error), []string) (setup.Report, error) {
		return setup.Report{}, nil // nothing to do
	}
	t.Cleanup(func() { stackUpdate = saved })
	code, _, errOut := call(t, noGather(t), "update")
	if code != 2 || !strings.Contains(errOut, "would update makeit 1.1.0 → 1.2.0 first") || !strings.Contains(errOut, "Run again with --yes") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
	if code, _, _ := call(t, noGather(t), "update", "--json"); code != 2 {
		t.Errorf("--json: exit %d, want 2", code)
	}
	if content(t, e.Exe) != "makeit 1.1.0" {
		t.Error("swapped without consent")
	}
	for _, r := range srv.Requests() {
		if !strings.HasPrefix(r, "HEAD ") {
			t.Errorf("downloaded without consent: %s", r)
		}
	}
}
