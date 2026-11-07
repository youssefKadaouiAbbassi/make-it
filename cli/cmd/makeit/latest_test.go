package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"makeit/internal/catalog"
	"makeit/internal/execx"
	"makeit/internal/latest"
	"makeit/internal/state"
)

// TestMain keeps update from asking the network for plugin releases: the real step runs with no
// runner (as offline), so its carry-forward code runs in every command test; the tests below put
// back a fake where they need one.
func TestMain(m *testing.M) {
	// Nor the npm registry for Claude Code's newest version: a lookup fails fast and nothing moves.
	_ = os.Setenv("MAKEIT_REGISTRY_URL", "https://127.0.0.1:1")
	latestApply = func(ctx context.Context, _ execx.Runner, t, current *catalog.Tree, skips map[string][]string) (*catalog.Tree, []state.PinMove, []string) {
		return latest.Apply(ctx, nil, t, current, skips)
	}
	os.Exit(m.Run())
}

type applyCall struct {
	called, online bool
	skips          map[string][]string
}

func fakeLatest(t *testing.T, move *state.PinMove) *applyCall {
	t.Helper()
	c := &applyCall{}
	saved := latestApply
	latestApply = func(_ context.Context, run execx.Runner, tr, _ *catalog.Tree, skips map[string][]string) (*catalog.Tree, []state.PinMove, []string) {
		c.called, c.online, c.skips = true, run != nil, skips
		if move == nil {
			return tr, nil, nil
		}
		return tr, []state.PinMove{*move}, nil
	}
	t.Cleanup(func() { latestApply = saved })
	return c
}

// The fake claude never updates a plugin: a move latest made is then not recorded, so the next
// session announces nothing and rollback has nothing to undo for it.
func TestUpdateDropsMovesThatDidNotUpdate(t *testing.T) {
	m := newCatMachine(t, nil)
	if code, _, errOut := run2(t, "setup", "--yes"); code != 0 {
		t.Fatalf("setup: exit %d\n%s", code, errOut)
	}
	c := fakeLatest(t, &state.PinMove{ID: "superpowers@makeit", FromTag: "v7.0.1", FromSHA: "a", ToTag: "v7.1.0", ToSHA: "b"})
	if code, _, errOut := run2(t, "update", "--yes"); code != 0 {
		t.Fatalf("update: exit %d\n%s", code, errOut)
	}
	if !c.called {
		t.Fatal("update did not look for newer plugin releases")
	}
	gens, err := state.LoadGenerations(m.stack)
	if err != nil || len(gens) == 0 {
		t.Fatalf("generations %v, %v", gens, err)
	}
	if p := gens[0].Manifest.Pins; len(p) != 0 {
		t.Errorf("pins %+v, want none: superpowers did not update", p)
	}
}

func TestUpdateLeavesPinnedCatalog(t *testing.T) {
	m := newCatMachine(t, nil)
	if code, _, errOut := run2(t, "setup", "--yes", "--catalog", m.url+"#v1.0.0"); code != 0 {
		t.Fatalf("setup: exit %d\n%s", code, errOut)
	}
	c := fakeLatest(t, nil)
	if code, _, errOut := run2(t, "update", "--yes"); code != 0 {
		t.Fatalf("update: exit %d\n%s", code, errOut)
	}
	if c.called {
		t.Error("a plugin set pinned to a tag was moved to newer plugin releases")
	}
}

func TestAutoRunsOnceADay(t *testing.T) {
	m := newCatMachine(t, nil)
	if code, _, errOut := run2(t, "setup", "--yes"); code != 0 {
		t.Fatalf("setup: exit %d\n%s", code, errOut)
	}
	fakeLatest(t, &state.PinMove{ID: "superpowers@makeit", FromTag: "v7.0.1", ToTag: "v7.1.0"})
	code, out, errOut := run2(t, "update", "--auto")
	if code != 0 || out != "" || errOut != "" {
		t.Fatalf("first auto run: exit %d, stdout %q, stderr %q; want silence", code, out, errOut)
	}
	log, _ := os.ReadFile(filepath.Join(m.stack, "update.log"))
	if strings.Count(string(log), "update --auto") != 1 {
		t.Errorf("update.log:\n%s", log)
	}
	if note, err := os.ReadFile(filepath.Join(m.stack, "pending-note")); err == nil {
		t.Errorf("pending-note %q announces a plugin that did not update", note)
	}
	if code, _, _ := run2(t, "update", "--auto"); code != 0 {
		t.Errorf("second auto run: exit %d", code)
	}
	log, _ = os.ReadFile(filepath.Join(m.stack, "update.log"))
	if strings.Count(string(log), "update --auto") != 1 {
		t.Errorf("a second auto run the same day ran:\n%s", log)
	}
}

func TestManualUpdateIgnoresSkips(t *testing.T) {
	m := newCatMachine(t, nil)
	if code, _, errOut := run2(t, "setup", "--yes"); code != 0 {
		t.Fatalf("setup: exit %d\n%s", code, errOut)
	}
	st, _, _ := state.LoadState(filepath.Join(m.stack, "state.json"))
	st.PluginSkips = map[string][]string{"superpowers@makeit": {"v7.1.0"}}
	if err := state.SaveState(filepath.Join(m.stack, "state.json"), st); err != nil {
		t.Fatal(err)
	}
	c := fakeLatest(t, nil)
	if code, _, errOut := run2(t, "update", "--yes"); code != 0 {
		t.Fatalf("update: exit %d\n%s", code, errOut)
	}
	if len(c.skips) != 0 {
		t.Errorf("skips %v; an update run by hand takes the latest", c.skips)
	}
}

// setup and update --offline keep the releases on disk without fetching; update fetches.
// setup takes the newest plugin and MCP releases too, not the ones makeit shipped with; only
// --offline keeps those.
func TestSetupResolvesNewestReleases(t *testing.T) {
	newCatMachine(t, nil)
	c := fakeLatest(t, nil)
	for _, tc := range []struct {
		args   []string
		online bool
	}{{[]string{"setup", "--yes"}, true}, {[]string{"setup", "--yes", "--offline"}, false}, {[]string{"update", "--yes", "--offline"}, false}, {[]string{"update", "--yes"}, true}} {
		*c = applyCall{}
		if code, _, errOut := run2(t, tc.args...); code != 0 {
			t.Fatalf("%v: exit %d\n%s", tc.args, code, errOut)
		}
		if !c.called || c.online != tc.online {
			t.Errorf("%v: called %v online %v, want online %v", tc.args, c.called, c.online, tc.online)
		}
	}
}

// A Claude Code that updated itself since the last run shows in the next update's changes,
// once, through the plan pass and the apply pass of a real run.
func TestUpdateShowsClaudeSelfUpdate(t *testing.T) {
	m := newCatMachine(t, nil)
	if code, _, errOut := run2(t, "setup", "--yes"); code != 0 {
		t.Fatalf("setup: exit %d\n%s", code, errOut)
	}
	path := filepath.Join(m.stack, "state.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var st map[string]any
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatal(err)
	}
	cc, _ := st["claudeCode"].(map[string]any)
	if cc == nil || cc["versionSeen"] == nil {
		t.Fatalf("setup recorded no Claude Code version: %v", st["claudeCode"])
	}
	now := cc["versionSeen"].(string)
	cc["versionSeen"] = "2.1.1"
	out, _ := json.Marshal(st)
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, errOut := run2(t, "update", "--yes", "--json")
	if code != 0 {
		t.Fatalf("update: exit %d\n%s", code, errOut)
	}
	if !strings.Contains(stdout, `"from": "2.1.1"`) || !strings.Contains(stdout, `"to": "`+now+`"`) {
		t.Errorf("no Claude Code 2.1.1 → %s in:\n%s", now, stdout)
	}
	_, stdout, _ = run2(t, "update", "--yes", "--json")
	if strings.Contains(stdout, `"from": "2.1.1"`) {
		t.Errorf("the self-update was listed twice:\n%s", stdout)
	}
}

// A daily run that stops before its steps (here an unreadable state.json; also a refused catalog)
// still counts as the day's run: it does not retry at every Claude Code start. The reason is in
// update.log.
func TestAutoFailedRunWaitsADay(t *testing.T) {
	m := newCatMachine(t, nil)
	if code, _, errOut := run2(t, "setup", "--yes"); code != 0 {
		t.Fatalf("setup: exit %d\n%s", code, errOut)
	}
	if err := os.WriteFile(filepath.Join(m.stack, "state.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := run2(t, "update", "--auto"); code == 0 {
		t.Fatal("the daily run did not fail on an unreadable state.json")
	}
	if _, err := os.Stat(filepath.Join(m.stack, "last-auto")); err != nil {
		t.Errorf("no last-auto after a failed daily run: %v", err)
	}
	run2(t, "update", "--auto")
	log, _ := os.ReadFile(filepath.Join(m.stack, "update.log"))
	if n := strings.Count(string(log), "update --auto"); n != 1 {
		t.Errorf("%d daily runs the same day, want 1:\n%s", n, log)
	}
}
