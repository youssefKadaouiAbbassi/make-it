package setup

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"makeit/internal/claudecli"
	"makeit/internal/execx"
	"makeit/internal/platform"
)

const nativeClaude = "/home/u/.local/bin/claude"

// claudeEnv is newEnv with the catalog floor set and Locate answering from *found.
func claudeEnv(t *testing.T, found *[]claudecli.Install, present ...string) (*env, *fakeRunner) {
	t.Helper()
	e, f := newEnv(t, present...)
	e.req.Requires.ClaudeCode.Hard = "2.1.287"
	e.Locate = func(context.Context) []claudecli.Install { return *found }
	return e, f
}

func version(v string) func(execx.Cmd) execx.Result {
	return func(execx.Cmd) execx.Result { return execx.Result{Stdout: v + " (Claude Code)\n"} }
}

func TestClaudeAdopted(t *testing.T) {
	found := []claudecli.Install{{Path: nativeClaude, Source: "native", OnPath: true}}
	e, f := claudeEnv(t, &found)
	f.answers[nativeClaude+" --version"] = version("2.1.311")
	if ok, err := e.claudeStep().Already(context.Background()); !ok || err != nil {
		t.Fatalf("already = %v, %v", ok, err)
	}
	if e.claude == nil || e.claude.Path != nativeClaude {
		t.Errorf("claude = %+v", e.claude)
	}
	if got := f.argvs(); len(got) != 1 {
		t.Errorf("calls %v, want only --version", got)
	}
}

func TestClaudeBelowFloor(t *testing.T) {
	found := []claudecli.Install{{Path: nativeClaude, Source: "native", OnPath: true}}
	e, f := claudeEnv(t, &found)
	f.answers[nativeClaude+" --version"] = version("2.1.200")
	s := e.claudeStep()
	if ok, _ := s.Already(context.Background()); ok {
		t.Fatal("a claude below the minimum version counts as installed")
	}
	f.answers["sh -c "+e.shell("claude update")] = func(execx.Cmd) execx.Result {
		f.answers[nativeClaude+" --version"] = version("2.1.311")
		return execx.Result{}
	}
	if _, err := s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.claude == nil {
		t.Error("claude not set after the upgrade")
	}
}

// installs makes the fake repository install put claude at /usr/bin/claude.
func installs(found *[]claudecli.Install, f *fakeRunner) func(execx.Cmd) execx.Result {
	return func(execx.Cmd) execx.Result {
		*found = []claudecli.Install{{Path: "/usr/bin/claude", Source: "path", OnPath: true}}
		f.answers["/usr/bin/claude --version"] = version("2.1.312")
		return execx.Result{}
	}
}

func TestClaudeRepoApt(t *testing.T) {
	var found []claudecli.Install
	e, f := claudeEnv(t, &found, "apt-get", "sudo")
	script, _ := platform.RepoScript(platform.APT)
	f.answers["sh -c "+e.shell(script)] = installs(&found, f)
	if _, err := e.claudeStep().Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := f.argvs()
	if len(got) < 2 || got[0] != "sudo -n true" || got[1] != "sh -c "+e.shell(script) {
		t.Fatalf("calls %v, want the sudo check then the apt script", got)
	}
	if !strings.HasPrefix(e.shell(script), "sudo() { command sudo -n \"$@\"; }\n") {
		t.Errorf("under --yes the script's sudo must add -n: %q", e.shell(script)[:60])
	}
	if p := e.st.Prereqs["claude-code"]; p == nil || p.Route != "apt" || p.InstalledBy != "makeit" {
		t.Errorf("state %+v", p)
	}
}

func TestClaudeRepoBadKey(t *testing.T) {
	var found []claudecli.Install
	e, f := claudeEnv(t, &found, "apt-get", "sudo")
	script, _ := platform.RepoScript(platform.APT)
	f.answers["sh -c "+e.shell(script)] = func(execx.Cmd) execx.Result {
		return execx.Result{Code: 3, Stderr: "makeit: the Claude Code signing key has the wrong fingerprint"}
	}
	_, err := e.claudeStep().Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "signing key") {
		t.Errorf("err = %v, want the key check failure", err)
	}
	for _, c := range f.argvs() {
		if strings.Contains(c, "claude.ai/install.sh") {
			t.Errorf("fell back to native after a bad key: %v", f.argvs())
		}
	}
}

func TestClaudeRepoFallsBackToNative(t *testing.T) {
	var found []claudecli.Install
	e, f := claudeEnv(t, &found, "apt-get", "sudo")
	f.answers["sudo -n true"] = func(execx.Cmd) execx.Result {
		return execx.Result{Code: 1, Stderr: "sudo: a password is required"}
	}
	f.answers["bash -c curl -fsSL https://claude.ai/install.sh | bash"] = func(execx.Cmd) execx.Result {
		found = []claudecli.Install{{Path: nativeClaude, Source: "native"}}
		f.answers[nativeClaude+" --version"] = version("2.1.312")
		return execx.Result{}
	}
	if _, err := e.claudeStep().Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(f.argvs(), "bash -c curl -fsSL https://claude.ai/install.sh | bash") {
		t.Errorf("calls %v, want the native installer", f.argvs())
	}
	if p := e.st.Prereqs["claude-code"]; p == nil || p.Route != "native" {
		t.Errorf("state %+v", p)
	}
}

func TestClaudeDetailListsFallback(t *testing.T) {
	var found []claudecli.Install
	e, _ := claudeEnv(t, &found, "apt-get", "sudo")
	d := e.claudeStep().Detail
	if !strings.Contains(d, "downloads.claude.ai/claude-code/apt/latest") || !strings.Contains(d, "curl -fsSL https://claude.ai/install.sh | bash") {
		t.Errorf("detail %q, want the apt script and the native fallback", d)
	}
}

// The apk script itself, run by a real shell: a key with the wrong sha256 exits 3 before any
// sudo step runs.
func TestClaudeRepoApkHash(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "wget"), []byte("#!/bin/sh\n# wget -qO <file> <url>\necho not-the-key > \"$2\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	script, _ := platform.RepoScript(platform.APK)
	guard := "sudo() { echo \"sudo ran: $*\" >&2; exit 9; }\n"
	r, err := execx.Run(context.Background(), execx.Cmd{Argv: []string{"sh", "-c", guard + script},
		Env: []string{"PATH=" + bin + ":" + os.Getenv("PATH")}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Code != 3 || strings.Contains(r.Stderr, "sudo ran") {
		t.Errorf("exit %d, stderr %q, want 3 before any sudo", r.Code, r.Stderr)
	}
}

// A claude from the apt repository below the minimum version is upgraded without a second prompt: under
// --yes no one can answer apt's "Do you want to continue?".
func TestClaudeBelowFloorFromRepository(t *testing.T) {
	found := []claudecli.Install{{Path: "/usr/bin/claude", Source: "path", OnPath: true}}
	e, f := claudeEnv(t, &found, "apt-get", "sudo")
	f.answers["/usr/bin/claude --version"] = version("2.1.200")
	upgrade := "sh -c " + e.shell("sudo apt-get update && sudo apt-get install -y --only-upgrade claude-code")
	f.answers[upgrade] = func(execx.Cmd) execx.Result {
		f.answers["/usr/bin/claude --version"] = version("2.1.312")
		return execx.Result{}
	}
	if _, err := e.claudeStep().Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(f.argvs(), upgrade) {
		t.Errorf("calls %v, want the non-interactive apt upgrade", f.argvs())
	}
}

// At a terminal, a user who cannot or will not give sudo its password gets the native installer
// (SETUP.md: "the user declines the sudo step").
func TestClaudeRepoDeclinedSudoFallsBack(t *testing.T) {
	var found []claudecli.Install
	e, f := claudeEnv(t, &found, "apt-get", "sudo")
	e.Interactive, e.Yes = true, false
	f.answers["sudo -v"] = func(execx.Cmd) execx.Result {
		return execx.Result{Code: 1, Stderr: "sudo: 3 incorrect password attempts"}
	}
	f.answers["bash -c curl -fsSL https://claude.ai/install.sh | bash"] = func(execx.Cmd) execx.Result {
		found = []claudecli.Install{{Path: nativeClaude, Source: "native"}}
		f.answers[nativeClaude+" --version"] = version("2.1.312")
		return execx.Result{}
	}
	if _, err := e.claudeStep().Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) == 0 || strings.Join(f.calls[0].Argv, " ") != "sudo -v" || !f.calls[0].Inherit {
		t.Fatalf("calls %v, want sudo -v on the terminal first", f.argvs())
	}
	if !slices.Contains(f.argvs(), "bash -c curl -fsSL https://claude.ai/install.sh | bash") {
		t.Errorf("calls %v, want the native installer", f.argvs())
	}
}
