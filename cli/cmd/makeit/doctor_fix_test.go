package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"makeit/internal/doctor"
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

// fakeTTYPrompt arranges the plain-mode prompt the way the existing uninstall tests do: a faked
// TTY, CI=1 (so the plain prompt runs, not the live one, which has its own tests), and stdin piped.
func fakeTTYPrompt(t *testing.T, answer string) {
	t.Helper()
	savedTTY, savedStdin := isTTY, os.Stdin
	t.Cleanup(func() { isTTY, os.Stdin = savedTTY, savedStdin })
	isTTY = func(io.Writer) bool { return true }
	t.Setenv("CI", "1")
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(answer + "\n"); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	os.Stdin = r
}

// realGather is the production gather, run against whatever env vars the test set (newCatMachine).
func realGather(context.Context) (doctor.Machine, error) { return gather(context.Background()) }

func TestDoctorFixRunsSetup(t *testing.T) {
	m := newCatMachine(t, nil)
	fakeTTYPrompt(t, "y")
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
