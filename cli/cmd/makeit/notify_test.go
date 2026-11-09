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

// With no terminal and no --yes, a run with nothing to do is the one that reaches setup's closing
// question: only the Interactive check keeps it silent.
func TestSetupNoTerminalAsksNothing(t *testing.T) {
	claudeDir, stack := handInstalled(t, nil)
	if code, _ := setupYes(t); code != 0 {
		t.Fatal("setup --yes failed")
	}
	code, _, errOut := call(t, noGather(t), "setup", "--plain")
	if code != 0 || strings.Contains(errOut, "needs an answer") || strings.Contains(errOut, "ntfy") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
	if strings.Contains(settingsText(t, claudeDir), "NTFY_TOPIC") {
		t.Error("a topic was written")
	}
	if st, _, _ := state.LoadState(filepath.Join(stack, "state.json")); st.Notify != "" {
		t.Errorf("recorded %q", st.Notify)
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

// A settings.json that cannot be written leaves no key behind and no record for uninstall.
func TestNotifyWriteFailureLeavesNoRecord(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory")
	}
	claudeDir, stack := handInstalled(t, nil)
	before := settingsText(t, claudeDir)
	if err := os.Chmod(claudeDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(claudeDir, 0o755) })
	terminal(t, "y\ny\n")
	if code, _, errOut := call(t, noGather(t), "notify"); code != 1 {
		t.Errorf("exit %d, stderr %q; want 1", code, errOut)
	}
	_ = os.Chmod(claudeDir, 0o755)
	if settingsText(t, claudeDir) != before {
		t.Error("settings.json changed")
	}
	st, _, _ := state.LoadState(filepath.Join(stack, "state.json"))
	if len(st.NotifyKeys) != 0 || st.Notify != "" {
		t.Errorf("state kept notify %q, keys %v after a failed write", st.Notify, st.NotifyKeys)
	}
}

// An interactive update asks the phone questions once, like setup; --yes and --auto never ask.
func TestUpdateAsksPhoneQuestionsOnce(t *testing.T) {
	claudeDir, stack := handInstalled(t, nil)
	terminal(t, "y\ny\nn\n") // the plan question first
	code, _, errOut := call(t, noGather(t), "update", "--offline")
	if code != 0 || !strings.Contains(errOut, "through ntfy?") || !strings.Contains(errOut, "https://ntfy.sh/makeit-") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if got := settingsText(t, claudeDir); !strings.Contains(got, `"NTFY_TOPIC": "makeit-`) {
		t.Errorf("settings after y/n: %s", got)
	}
	if st, _, err := state.LoadState(filepath.Join(stack, "state.json")); err != nil || st.Notify != "asked" {
		t.Errorf("state notify %q, %v", st.Notify, err)
	}
	terminal(t, "") // asked once: an empty input would fail a question
	code, _, errOut = call(t, noGather(t), "update", "--offline")
	if code != 0 || strings.Contains(errOut, "ntfy") || strings.Contains(errOut, "phone push") {
		t.Errorf("second update: exit %d, stderr %q", code, errOut)
	}
}

func TestUpdateYesAndAutoNeverAskPhoneQuestions(t *testing.T) {
	for _, args := range [][]string{{"update", "--offline", "--yes"}, {"update", "--offline", "--auto"}} {
		claudeDir, stack := handInstalled(t, nil)
		terminal(t, "y\ny\ny\ny\n")
		call(t, noGather(t), args...)
		if got := settingsText(t, claudeDir); strings.Contains(got, "NTFY_TOPIC") {
			t.Errorf("%v wrote a topic: %s", args, got)
		}
		if st, _, _ := state.LoadState(filepath.Join(stack, "state.json")); st.Notify != "" {
			t.Errorf("%v recorded %q", args, st.Notify)
		}
	}
}

func TestNotifyAsksRemoteControlWithLoginNote(t *testing.T) {
	handInstalled(t, nil)
	t.Setenv("ANTHROPIC_API_KEY", "")
	terminal(t, "y\nn\n")
	_, _, errOut := call(t, noGather(t), "notify")
	if !strings.Contains(errOut, "(needs a claude.ai login)") {
		t.Errorf("stderr %q lacks the login note", errOut)
	}
}

// Remote Control needs a claude.ai login: with an API key or apiKeyHelper (environment or
// settings) the question is not asked.
func TestNotifySkipsRemoteControlWithAPIKey(t *testing.T) {
	for name, set := range map[string]func(t *testing.T, claudeDir string){
		"env": func(t *testing.T, _ string) { t.Setenv("ANTHROPIC_API_KEY", "sk-x") },
		"settings env": func(t *testing.T, d string) {
			os.WriteFile(filepath.Join(d, "settings.json"), []byte(`{"env":{"ANTHROPIC_API_KEY":"sk-x"}}`), 0o600)
		},
		"apiKeyHelper": func(t *testing.T, d string) {
			os.WriteFile(filepath.Join(d, "settings.json"), []byte(`{"apiKeyHelper":"/bin/key"}`), 0o600)
		},
	} {
		t.Run(name, func(t *testing.T) {
			claudeDir, _ := handInstalled(t, nil)
			t.Setenv("ANTHROPIC_API_KEY", "")
			set(t, claudeDir)
			terminal(t, "y\n") // a second question would hit end of input and fail
			code, _, errOut := call(t, noGather(t), "notify")
			if code != 0 || strings.Contains(errOut, "Remote Control") {
				t.Errorf("exit %d, stderr %q", code, errOut)
			}
			if got := settingsText(t, claudeDir); strings.Contains(got, "remoteControlAtStartup") || !strings.Contains(got, "NTFY_TOPIC") {
				t.Errorf("settings %s", got)
			}
		})
	}
}

func TestNotifySubscribeLineUsesNtfyURL(t *testing.T) {
	handInstalled(t, nil)
	t.Setenv("NTFY_URL", "https://ntfy.example/")
	terminal(t, "y\nn\n")
	_, _, errOut := call(t, noGather(t), "notify")
	if !strings.Contains(errOut, "subscribe to https://ntfy.example/makeit-") {
		t.Errorf("stderr %q", errOut)
	}
}
