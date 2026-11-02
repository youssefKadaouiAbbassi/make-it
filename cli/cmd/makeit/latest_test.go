package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"makeit/internal/catalog"
	"makeit/internal/execx"
	"makeit/internal/state"
)

// TestMain keeps update from asking the network for plugin releases; the tests below put back
// a fake where they need one.
func TestMain(m *testing.M) {
	latestApply = func(_ context.Context, _ execx.Runner, t, _ *catalog.Tree, _ map[string][]string) (*catalog.Tree, []state.PinMove, []string) {
		return t, nil, nil
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

func TestUpdateRecordsLatestMoves(t *testing.T) {
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
	if p := gens[0].Manifest.Pins; len(p) != 1 || p[0].ToTag != "v7.1.0" {
		t.Errorf("pins %+v, want the move recorded for rollback", p)
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
	if note, _ := os.ReadFile(filepath.Join(m.stack, "pending-note")); !strings.Contains(string(note), "superpowers 7.1.0") {
		t.Errorf("pending-note %q", note)
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
func TestLatestOnlineOnlyForUpdate(t *testing.T) {
	newCatMachine(t, nil)
	c := fakeLatest(t, nil)
	for _, tc := range []struct {
		args   []string
		online bool
	}{{[]string{"setup", "--yes"}, false}, {[]string{"update", "--yes", "--offline"}, false}, {[]string{"update", "--yes"}, true}} {
		*c = applyCall{}
		if code, _, errOut := run2(t, tc.args...); code != 0 {
			t.Fatalf("%v: exit %d\n%s", tc.args, code, errOut)
		}
		if !c.called || c.online != tc.online {
			t.Errorf("%v: called %v online %v, want online %v", tc.args, c.called, c.online, tc.online)
		}
	}
}
