package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"makeit/internal/fsx"
	"makeit/internal/state"
)

// terminal makes the next commands see a terminal whose input is answers.
func terminal(t *testing.T, answers string) {
	t.Helper()
	savedTTY, savedStdin := isTTY, os.Stdin
	t.Cleanup(func() { isTTY, os.Stdin = savedTTY, savedStdin })
	isTTY = func(io.Writer) bool { return true }
	t.Setenv("CI", "1") // the plain prompt; the live one has its own tests in internal/ui
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(answers); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	os.Stdin = r
}

func settingsText(t *testing.T, claudeDir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSetupAsksPhoneQuestionsOnce(t *testing.T) {
	claudeDir, stack := handInstalled(t, nil)
	terminal(t, "y\ny\nn\n") // the plan question first
	code, _, errOut := call(t, noGather(t), "setup")
	if code != 0 || !strings.Contains(errOut, "through ntfy?") || !strings.Contains(errOut, "https://ntfy.sh/makeit-") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	got := settingsText(t, claudeDir)
	if !strings.Contains(got, `"NTFY_TOPIC": "makeit-`) || strings.Contains(got, "remoteControlAtStartup") {
		t.Errorf("settings after y/n: %s", got)
	}
	st, _, err := state.LoadState(filepath.Join(stack, "state.json"))
	if err != nil || st.Notify != "asked" {
		t.Errorf("state notify %q, %v", st.Notify, err)
	}
	// The second run has nothing to ask; an empty input would fail a question.
	terminal(t, "")
	code, _, errOut = call(t, noGather(t), "setup")
	if code != 0 || strings.Contains(errOut, "ntfy") || strings.Contains(errOut, "phone push") {
		t.Errorf("second run: exit %d, stderr %q", code, errOut)
	}
}

func TestSetupYesAndNoTerminalAskNothing(t *testing.T) {
	claudeDir, stack := handInstalled(t, nil)
	if code, _ := setupYes(t); code != 0 {
		t.Fatal("setup --yes failed")
	}
	if got := settingsText(t, claudeDir); strings.Contains(got, "NTFY_TOPIC") {
		t.Errorf("--yes wrote a topic: %s", got)
	}
	if st, _, _ := state.LoadState(filepath.Join(stack, "state.json")); st.Notify != "" {
		t.Errorf("--yes recorded %q", st.Notify)
	}
}

func TestNotifyAsksAgain(t *testing.T) {
	claudeDir, stack := handInstalled(t, nil)
	statePath := filepath.Join(stack, "state.json")
	st, _, _ := state.LoadState(statePath)
	st.Notify = "asked"
	if err := state.SaveState(statePath, st); err != nil {
		t.Fatal(err)
	}
	terminal(t, "n\ny\n")
	code, _, errOut := call(t, noGather(t), "notify")
	if code != 0 || !strings.Contains(errOut, "Remote Control") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	got := settingsText(t, claudeDir)
	for _, k := range []string{"remoteControlAtStartup", "inputNeededNotifEnabled", "agentPushNotifEnabled"} {
		if !strings.Contains(got, `"`+k+`": true`) {
			t.Errorf("%s missing: %s", k, got)
		}
	}
	if strings.Contains(got, "NTFY_TOPIC") {
		t.Errorf("answered no to ntfy but a topic was written")
	}
	st, _, _ = state.LoadState(statePath)
	if st.Notify != "asked" || st.NotifyKeys["/agentPushNotifEnabled"] == nil || st.NotifyKeys["/env/NTFY_TOPIC"] != nil {
		t.Errorf("state notify %q, keys %v", st.Notify, st.NotifyKeys)
	}
	if code, _, errOut := call(t, noGather(t), "notify", "now"); code != 2 {
		t.Errorf("argument: exit %d, %q", code, errOut)
	}
}

func TestNotifyDryRunWritesNothing(t *testing.T) {
	claudeDir, stack := handInstalled(t, nil)
	before := settingsText(t, claudeDir)
	terminal(t, "y\ny\n")
	if code, _, errOut := call(t, noGather(t), "notify", "--dry-run"); code != 2 || !strings.Contains(errOut, "dry run") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
	if settingsText(t, claudeDir) != before {
		t.Error("settings.json changed under --dry-run")
	}
	if _, err := os.Stat(filepath.Join(stack, "state.json")); err == nil {
		t.Error("state.json written under --dry-run")
	}
}

func TestNotifyWaitsOutARunningMakeit(t *testing.T) {
	claudeDir, stack := handInstalled(t, nil)
	lock, err := fsx.Acquire(filepath.Join(stack, "lock"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	before := settingsText(t, claudeDir)
	terminal(t, "y\ny\n")
	if code, _, errOut := call(t, noGather(t), "notify"); code != 1 || !strings.Contains(errOut, "another makeit run") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
	if settingsText(t, claudeDir) != before {
		t.Error("settings.json written while another run held the lock")
	}
}

func TestNotifyNeedsTerminal(t *testing.T) {
	handInstalled(t, nil)
	if code, _, errOut := call(t, noGather(t), "notify"); code != 2 || !strings.Contains(errOut, "needs a terminal") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
}

// Each of these would ask if its guard were gone; the answers are yes, so a question shows as a topic.
func TestSetupGuardsAskNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		tty  bool
	}{
		{"yes at a terminal", []string{"setup", "--yes"}, true},
		{"dry run at a terminal", []string{"setup", "--dry-run"}, true},
		{"no terminal, plain", []string{"setup", "--yes", "--plain"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claudeDir, stack := handInstalled(t, nil)
			if tc.tty {
				terminal(t, "y\ny\ny\n")
			}
			call(t, noGather(t), tc.args...)
			if got := settingsText(t, claudeDir); strings.Contains(got, "NTFY_TOPIC") {
				t.Errorf("a topic was written: %s", got)
			}
			if st, _, _ := state.LoadState(filepath.Join(stack, "state.json")); st.Notify != "" {
				t.Errorf("recorded %q", st.Notify)
			}
		})
	}
}
