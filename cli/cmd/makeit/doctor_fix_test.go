package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"makeit/internal/cli"
	"makeit/internal/doctor"
	"makeit/internal/plan"
)

func TestFixCommand(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fixes []string
		want  string
		n     int
	}{
		{"none", nil, "", 0},
		{"only manual", []string{"https://cli.github.com"}, "", 0},
		{"setup only", []string{"makeit setup", "https://cli.github.com"}, "setup", 1},
		{"update wins", []string{"makeit setup", "makeit update"}, "update", 2},
	} {
		var checks []doctor.Check
		for _, f := range tc.fixes {
			checks = append(checks, doctor.Check{Level: doctor.Warn, Fix: f})
		}
		cmd, n := fixCommand(checks)
		if cmd != tc.want || n != tc.n {
			t.Errorf("%s: fixCommand = %q, %d; want %q, %d", tc.name, cmd, n, tc.want, tc.n)
		}
	}
}

// fakeTTYPrompt arranges the live prompt: a faked TTY, no CI, and stdin piped with the keys
// (Enter takes the default, n is No), which the live form reads through its EOF wrapper.
func fakeTTYPrompt(t *testing.T, answer string) {
	t.Helper()
	savedTTY, savedStdin := isTTY, os.Stdin
	t.Cleanup(func() { isTTY, os.Stdin = savedTTY, savedStdin })
	isTTY = func(io.Writer) bool { return true }
	t.Setenv("CI", "")
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm")
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(answer); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	os.Stdin = r
}

// realGather is the production gather, run against whatever env vars the test set (newCatMachine).
func realGather(context.Context) (doctor.Machine, error) { return gather(context.Background()) }

func TestDoctorFixRunsSetup(t *testing.T) {
	m := newCatMachine(t, nil)
	fakeTTYPrompt(t, "\r")
	code, _, errOut := call(t, realGather, "doctor")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	if !strings.Contains(errOut, "Fix ") || !strings.Contains(errOut, "problems now?") {
		t.Errorf("no fix question:\n%s", errOut)
	}
	if _, err := os.Stat(filepath.Join(m.home, ".claude", "settings.json")); err != nil {
		t.Errorf("settings.json not written after Yes: %v", err)
	}
}

func TestDoctorFixDeclined(t *testing.T) {
	m := newCatMachine(t, nil)
	fakeTTYPrompt(t, "n")
	if _, _, errOut := call(t, realGather, "doctor"); !strings.Contains(errOut, "problems now?") {
		t.Errorf("no fix question:\n%s", errOut)
	}
	if _, err := os.Stat(filepath.Join(m.home, ".claude", "settings.json")); err == nil {
		t.Error("settings.json was written although the answer was No")
	}
}

func TestDoctorNoTTYNoQuestion(t *testing.T) {
	m := newCatMachine(t, nil)
	if _, _, errOut := call(t, realGather, "doctor"); strings.Contains(errOut, "problems now?") {
		t.Errorf("a question appeared without a terminal:\n%s", errOut)
	}
	if _, err := os.Stat(filepath.Join(m.home, ".claude", "settings.json")); err == nil {
		t.Error("settings.json was written without a terminal")
	}
}

// The consent rule: --yes applies unattended (sudo -n); doctor's own yes applies without asking
// the plan question again but keeps sudo able to ask for a password at the terminal.
func TestDecideRun(t *testing.T) {
	for _, tc := range []struct {
		name  string
		bools map[string]bool
		yes   bool
		mode  plan.Mode
	}{
		{"consented", map[string]bool{"consented": true}, false, plan.Apply},
		{"--yes", map[string]bool{"yes": true}, true, plan.Apply},
		{"a terminal", map[string]bool{}, false, plan.Prompt},
		{"consented dry run", map[string]bool{"consented": true, "dry-run": true}, false, plan.DryRunOnly},
	} {
		yes, mode := decideRun(cli.Parsed{Bool: tc.bools}, true)
		if yes != tc.yes || mode != tc.mode {
			t.Errorf("%s: yes %v mode %v, want %v %v", tc.name, yes, mode, tc.yes, tc.mode)
		}
	}
}

// Doctor asks only in the live view; in plain mode (a pipe, CI, --plain, --verbose) it is read-only.
func TestDoctorPlainModeNeverAsks(t *testing.T) {
	m := newCatMachine(t, nil)
	fakeTTYPrompt(t, "\r")
	t.Setenv("CI", "1")
	if _, _, errOut := call(t, realGather, "doctor"); strings.Contains(errOut, "problems now?") {
		t.Errorf("plain mode asked:\n%s", errOut)
	}
	if _, err := os.Stat(filepath.Join(m.home, ".claude", "settings.json")); err == nil {
		t.Error("settings.json was written in plain mode")
	}
}
