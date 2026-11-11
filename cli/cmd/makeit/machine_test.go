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

// gitStandIn is the host's git that refuses a remote URL (setup's release lookups, a clone from
// GitHub): it fails like a stand-in and notes the call in remoteGitTrace. Local and file://
// repositories go to the host's git, found before the test machine's PATH replaces the host's.
func gitStandIn(t *testing.T, bin string) string {
	t.Helper()
	host, err := exec.LookPath("git")
	if err != nil {
		t.Skip("needs git")
	}
	return "#!/bin/sh\nfor a in \"$@\"; do\n  case \"$a\" in\n" +
		"    https://* | http://* | ssh://* | git@*)\n" +
		"      echo \"git $*\" >>'" + remoteGitTrace(bin) + "'\n" +
		"      echo \"" + ranOutside + "git $*\" >&2\n      exit 97 ;;\n  esac\ndone\n" +
		"exec '" + host + "' \"$@\"\n"
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
