package setup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"makeit/internal/catalog"
	"makeit/internal/claudecli"
	"makeit/internal/execx"
	"makeit/internal/state"
)

// settingsEnv has a catalog with baseline, a live settings.json (absent when live is ""), a
// generation, and a claude whose doctor answers with invalid(callNumber).
func settingsEnv(t *testing.T, baseline, live string, invalid func(n int) bool) (*env, string) {
	t.Helper()
	e, f := newEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "settings.baseline.json"), []byte(baseline), 0o644); err != nil {
		t.Fatal(err)
	}
	e.Catalog = catalog.Dir(dir)
	e.req.Marketplace = "makeit"
	path := filepath.Join(e.Roots.Claude, "settings.json")
	if live != "" {
		if err := os.MkdirAll(e.Roots.Claude, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(live), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	f.answers["claude doctor"] = func(c execx.Cmd) execx.Result {
		calls++
		if invalid == nil || !invalid(calls) {
			return execx.Result{}
		}
		var cfg string
		for _, v := range c.Env {
			if d, ok := strings.CutPrefix(v, "CLAUDE_CONFIG_DIR="); ok {
				cfg = d
			}
		}
		return execx.Result{Stdout: "Invalid settings\n- " + filepath.Join(cfg, "settings.json") + " › model: Expected string\n"}
	}
	e.claude = &claudecli.CLI{Path: "claude", Run: f.run}
	g, err := state.NewGeneration(e.Roots.Stack, 0, "setup", nil)
	if err != nil {
		t.Fatal(err)
	}
	e.gen = g
	return e, path
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSettingsAddsOnlyMissing(t *testing.T) {
	e, path := settingsEnv(t, `{"model":"claude-opus-5-5","env":{"X":"1"}}`, `{"model":"claude-sonnet-5-5"}`, nil)
	if _, err := e.settingsStep().Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := read(t, path)
	if !strings.Contains(got, `"model": "claude-sonnet-5-5"`) || !strings.Contains(got, `"X": "1"`) {
		t.Errorf("settings %s: want the user's model kept and env.X added", got)
	}
	if ops := e.gen.Manifest.Ops; len(ops) != 1 || read(t, filepath.Join(e.gen.Dir, ops[0].PreImage)) != `{"model":"claude-sonnet-5-5"}` {
		t.Errorf("ops %+v, want the pre-image of the old file", ops)
	}
	if len(e.st.Settings.Paths) == 0 {
		t.Error("merge base not recorded")
	}
}

func TestSettingsRecordsBaseWithoutWrite(t *testing.T) {
	live := "{\n  \"model\": \"claude-opus-5-5\"\n}\n"
	e, path := settingsEnv(t, `{"model":"claude-opus-5-5"}`, live, nil)
	s := e.settingsStep()
	if done, err := s.Already(context.Background()); done || err != nil {
		t.Fatalf("already = %v, %v; want false while no merge base is recorded", done, err)
	}
	if _, err := s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if read(t, path) != live || len(e.gen.Manifest.Ops) != 0 {
		t.Errorf("file or ops changed: %q %+v", read(t, path), e.gen.Manifest.Ops)
	}
	if done, _ := e.settingsStep().Already(context.Background()); !done {
		t.Error("not already after the base was recorded")
	}
}

func TestSettingsMergesAtApplyTime(t *testing.T) {
	e, path := settingsEnv(t, `{"env":{"X":"1"}}`, `{"model":"claude-sonnet-5-5"}`, nil)
	s := e.settingsStep()
	if err := os.WriteFile(path, []byte(`{"model":"claude-haiku-5-5"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); !strings.Contains(got, "claude-haiku-5-5") {
		t.Errorf("settings %s: the value written after planning was lost", got)
	}
}

func TestSettingsRefusesInvalidCandidate(t *testing.T) {
	e, path := settingsEnv(t, `{"env":{"X":"1"}}`, `{"model":"x"}`, func(int) bool { return true })
	if _, err := e.settingsStep().Run(context.Background()); err == nil || !strings.Contains(err.Error(), "model") {
		t.Errorf("err = %v, want the invalid key", err)
	}
	if read(t, path) != `{"model":"x"}` || len(e.gen.Manifest.Ops) != 0 {
		t.Error("an invalid candidate was written")
	}
}

func TestSettingsRestoresWhenInvalidAfter(t *testing.T) {
	e, path := settingsEnv(t, `{"env":{"X":"1"}}`, `{"model":"x"}`, func(n int) bool { return n == 2 })
	if _, err := e.settingsStep().Run(context.Background()); err == nil {
		t.Fatal("invalid after writing, but no error")
	}
	if got := read(t, path); got != `{"model":"x"}` {
		t.Errorf("settings %q, want the pre-image restored byte for byte", got)
	}
}

func TestSettingsDryRunHidesSecret(t *testing.T) {
	e, _ := settingsEnv(t, `{"env":{"API_KEY":"s3cret"}}`, `{}`, nil)
	d := e.settingsStep().Detail
	if strings.Contains(d, "s3cret") || !strings.Contains(d, "+ /env/API_KEY: (secret)") {
		t.Errorf("detail %q", d)
	}
}

func TestSettingsRestoresThroughSymlink(t *testing.T) {
	e, path := settingsEnv(t, `{"env":{"X":"1"}}`, "", func(n int) bool { return n == 2 })
	target := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(target, []byte(`{"model":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := e.settingsStep().Run(context.Background()); err == nil {
		t.Fatal("invalid after writing, but no error")
	}
	if fi, err := os.Lstat(path); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the restore replaced the symlink")
	}
	if got := read(t, target); got != `{"model":"x"}` {
		t.Errorf("target %q, want the pre-image restored", got)
	}
}

func TestSettingsRecordsCreated(t *testing.T) {
	e, path := settingsEnv(t, `{"model":"claude-opus-5-5"}`, "", nil)
	if _, err := e.settingsStep().Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if read(t, path) == "" || !e.st.Settings.CreatedByUs {
		t.Errorf("created = %v, want settings.json written and recorded as created by makeit", e.st.Settings.CreatedByUs)
	}
	e2, _ := settingsEnv(t, `{"model":"claude-opus-5-5"}`, `{"other":1}`, nil)
	if _, err := e2.settingsStep().Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e2.st.Settings.CreatedByUs {
		t.Error("an existing settings.json was recorded as created by makeit")
	}
}
