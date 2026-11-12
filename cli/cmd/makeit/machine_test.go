package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"makeit/internal/platform"
)

// outside are the programs that reach outside a test machine: sudo and the package managers
// makeit knows would change the host, npm would download from the registry.
var outside = []string{"sudo", "apt-get", "dnf", "apk", "pacman", "brew", "npm"}

// ranOutside is what a stand-in prints before it fails.
const ranOutside = "a test machine ran: "

// standIn is a program that fails and names its call.
func standIn(name string) string {
	return "#!/bin/sh\necho \"" + ranOutside + name + " $*\" >&2\nexit 97\n"
}

// stubTools fills a test machine's bin, which comes first on its PATH. Every tool makeit may
// install (platform.Prereqs) gets an empty program, so the plan is the same on every host: one
// with go and no gopls plans no install. The programs in outside get a stand-in that fails and
// names the call, so a test that reaches one fails or says so instead of changing the host
// (GitHub's runners have a sudo that asks no password). The names in real stay the host's; a
// real git refuses a remote URL (gitStandIn).
func stubTools(t *testing.T, bin string, real ...string) {
	t.Helper()
	for _, r := range platform.Prereqs {
		for _, b := range r.Bins {
			if !slices.Contains(real, b) {
				write(t, filepath.Join(bin, b), "#!/bin/sh\n")
			}
		}
	}
	for _, b := range outside {
		write(t, filepath.Join(bin, b), standIn(b))
	}
	if slices.Contains(real, "git") {
		write(t, filepath.Join(bin, "git"), gitStandIn(t, bin))
	}
}

// gitStandIn is the host's git that refuses a call to a remote (setup's release lookups, a clone
// from GitHub): a subcommand that talks to a remote, given a remote address (<scheme>:// other
// than file://, or scp-style user@host:path). It fails like a stand-in and notes the call in
// remoteGitTrace. Every other call goes to the host's git, found before the test machine's PATH
// replaces the host's.
func gitStandIn(t *testing.T, bin string) string {
	t.Helper()
	host, err := exec.LookPath("git")
	if err != nil {
		t.Skip("needs git")
	}
	return `#!/bin/sh
sub= skip=
for a in "$@"; do
  if [ -n "$skip" ]; then skip=; continue; fi
  case "$a" in -C | -c) skip=1 ;; -*) ;; *) sub=$a; break ;; esac
done
remote=
case "$sub" in
  ls-remote | fetch | clone | pull | push | submodule)
    for a in "$@"; do
      case "$a" in
        file://*) ;;
        *://*) remote=1 ;;
        -* | */*:*) ;;
        *@*:*) remote=1 ;;
      esac
    done ;;
esac
if [ -n "$remote" ]; then
  echo "git $*" >>'` + remoteGitTrace(bin) + `'
  echo "` + ranOutside + `git $*" >&2
  exit 97
fi
exec '` + host + `' "$@"
`
}

// remoteGitTrace is the file where a test machine's git notes each remote call it refused.
func remoteGitTrace(bin string) string { return filepath.Join(bin, ".remote-git") }

// Setup looks up the newest tool releases with git ls-remote on github.com. On a test machine
// the host's git refuses that call, notes it, and setup goes on as it does offline.
func TestMachineRefusesRemoteGit(t *testing.T) {
	m := newCatMachine(t, nil)
	code, out, errOut := run2(t, "setup", "--yes", "--plain")
	if code != 0 {
		t.Fatalf("setup with remote git refused: exit %d\n%s", code, out+errOut)
	}
	trace, _ := os.ReadFile(remoteGitTrace(m.bin))
	if !strings.Contains(string(trace), "git ls-remote --tags https://github.com/") {
		t.Errorf("no remote lookup was refused; trace %q\n%s", trace, out+errOut)
	}
}

// The test machine's git refuses by what a call does: a command that talks to a remote address.
// A local command that only carries such an address as text goes to the host's git.
func TestMachineGitStandIn(t *testing.T) {
	bin := t.TempDir()
	write(t, filepath.Join(bin, "git"), gitStandIn(t, bin))
	repo := t.TempDir()
	// GIT_ALLOW_PROTOCOL keeps the host's git offline should a remote call get through.
	env := append(os.Environ(), "HOME="+t.TempDir(), "GIT_CONFIG_NOSYSTEM=1", "GIT_ALLOW_PROTOCOL=file",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
	git := func(args ...string) (int, string) {
		cmd := exec.Command(filepath.Join(bin, "git"), args...)
		cmd.Dir, cmd.Env = repo, env
		out, _ := cmd.CombinedOutput()
		return cmd.ProcessState.ExitCode(), string(out)
	}
	if code, out := git("init", "-q"); code != 0 {
		t.Fatalf("git init: exit %d: %s", code, out)
	}
	for _, args := range [][]string{
		{"remote", "add", "origin", "https://example.com/x.git"},
		{"commit", "-q", "--allow-empty", "-m", "https://example.com/notes"},
	} {
		if code, out := git(args...); code != 0 {
			t.Errorf("git %s: exit %d, want the host's git: %s", strings.Join(args, " "), code, out)
		}
	}
	for _, args := range [][]string{
		{"ls-remote", "git://example.com/x"},
		{"clone", "user@example.com:x"},
	} {
		code, out := git(args...)
		if code != 97 || !strings.Contains(out, ranOutside+"git ") {
			t.Errorf("git %s: exit %d, want refused (97): %s", strings.Join(args, " "), code, out)
		}
		trace, _ := os.ReadFile(remoteGitTrace(bin))
		if !strings.Contains(string(trace), "git "+strings.Join(args, " ")) {
			t.Errorf("git %s is not in the trace %q", strings.Join(args, " "), trace)
		}
	}
}

// The public CI ran `sudo apt-get install gopls` for real from these tests: its runners have go
// and a sudo that asks no password, and the machine's list of stand-ins had fallen behind the
// tool table. A host's toolchain must not add an install to a test machine's plan.
func TestMachinePlanIgnoresTheHost(t *testing.T) {
	m := newCatMachine(t, nil)
	toolchains := []string{"go", "cargo", "rustup"}
	for _, b := range toolchains {
		write(t, filepath.Join(m.bin, b), standIn(b))
	}
	code, out, errOut := run2(t, "setup", "--yes", "--plain")
	if code != 0 {
		t.Fatalf("setup on a host with toolchains: exit %d\n%s", code, errOut)
	}
	s := out + errOut
	// npm is the one stand-in setup may reach: the browser server's local copy, which it keeps
	// as it is when npm fails.
	for _, b := range append(toolchains, "sudo", "apt-get", "dnf", "apk", "pacman", "brew") {
		if strings.Contains(s, ranOutside+b+" ") {
			t.Errorf("the plan ran %s:\n%s", b, s)
		}
	}
	for _, r := range platform.Prereqs {
		// The step's line ends at the tool's name or goes on with ": <why it failed>".
		if regexp.MustCompile(`(?m)Install ` + regexp.QuoteMeta(r.ID) + `(:|$)`).MatchString(s) {
			t.Errorf("the plan installs %s:\n%s", r.ID, s)
		}
	}
}
