package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"makeit/internal/plan"
)

func liveUI() *UI {
	u := NewFromReader(nil, &bytes.Buffer{}, false)
	u.Mode = Live
	return u
}

func ev(id, title string, st plan.Status) plan.Event {
	s := plan.Step{ID: id, Title: title}
	if st == "" {
		return plan.Event{Step: s}
	}
	return plan.Event{Step: s, Outcome: &plan.Outcome{ID: id, Title: title, Status: st}}
}

func lines(m *liveModel) []string {
	return strings.Split(strings.TrimRight(m.View().Content, "\n"), "\n")
}

func planned() []plan.Outcome {
	return []plan.Outcome{
		{ID: "prereq.git", Title: "Install git"},
		{ID: "claude", Title: "Install Claude Code"},
		{ID: "catalog", Title: "Write the plugin set"},
		{ID: "bundle", Title: "Install makeit-bundle"},
		{ID: "settings", Title: "Merge settings"},
		{ID: "global.CLAUDE.md", Title: "Write CLAUDE.md"},
	}
}

func TestLiveGroupsInOrder(t *testing.T) {
	m := newLiveModel(liveUI(), planned())
	for _, e := range []plan.Event{
		ev("prereq.git", "Install git", ""), ev("prereq.git", "Install git", plan.Done),
		ev("claude", "Install Claude Code", ""), ev("claude", "Install Claude Code", plan.Done),
	} {
		m.apply(e)
	}
	got := lines(m)
	want := []string{"  ✓ Machine", "  ✓ Claude Code", "  · Plugins", "  · Settings", "  · Files"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("rows:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestLiveBarCounts(t *testing.T) {
	var outs []plan.Outcome
	for i := 0; i < 12; i++ {
		outs = append(outs, plan.Outcome{ID: "plugin.update.p" + string(rune('a'+i)), Title: "Update"})
	}
	m := newLiveModel(liveUI(), outs)
	for i := 0; i < 5; i++ {
		id := "plugin.update.p" + string(rune('a'+i))
		m.apply(ev(id, "Update", ""))
		m.apply(ev(id, "Update", plan.Done))
	}
	m.apply(ev("plugin.update.pf", "Update", ""))
	row := lines(m)[0]
	if !strings.Contains(row, "Plugins") || !strings.Contains(row, "5/12") || !strings.Contains(row, "━") {
		t.Errorf("plugins row %q, want the bar and 5/12", row)
	}
}

func TestLiveWorstOutcomeWins(t *testing.T) {
	outs := []plan.Outcome{{ID: "catalog"}, {ID: "bundle"}}
	m := newLiveModel(liveUI(), outs)
	m.apply(ev("catalog", "Write the plugin set", ""))
	m.apply(ev("catalog", "Write the plugin set", plan.Failed))
	m.apply(ev("bundle", "Install makeit-bundle", ""))
	m.apply(ev("bundle", "Install makeit-bundle", plan.Done))
	if row := lines(m)[0]; !strings.HasPrefix(row, "  ✗ Plugins") {
		t.Errorf("row %q, want the failure to win", row)
	}
}

func TestLiveAllAlreadySaysUpToDate(t *testing.T) {
	m := newLiveModel(liveUI(), []plan.Outcome{{ID: "settings"}})
	m.apply(ev("settings", "Merge settings", ""))
	m.apply(ev("settings", "Merge settings", plan.Already))
	if row := lines(m)[0]; !strings.HasPrefix(row, "  ✓ Settings") || !strings.Contains(row, "up to date") {
		t.Errorf("row %q", row)
	}
}

func TestLiveInterrupt(t *testing.T) {
	m := newLiveModel(liveUI(), planned())
	m.apply(ev("prereq.git", "Install git", ""))
	m.stop(true)
	row := lines(m)[0]
	if !strings.HasPrefix(row, "  ✗ Machine") || !strings.Contains(row, "interrupted") {
		t.Errorf("row %q, want the running group marked interrupted", row)
	}
}

func TestLiveUnplannedGroupAppears(t *testing.T) {
	m := newLiveModel(liveUI(), nil)
	m.apply(ev("settings", "Merge settings", ""))
	m.apply(ev("settings", "Merge settings", plan.Done))
	m.apply(ev("global.CLAUDE.md", "Write CLAUDE.md", ""))
	got := lines(m)
	if len(got) != 2 || !strings.HasPrefix(got[0], "  ✓ Settings") || !strings.Contains(got[1], "Files") {
		t.Errorf("rows %q", got)
	}
}

func TestLiveNarrowTerminal(t *testing.T) {
	m := newLiveModel(liveUI(), []plan.Outcome{{ID: "settings"}})
	m.width = 20
	m.apply(ev("settings", "Merge makeit's settings into a very long path name", ""))
	m.apply(ev("settings", "Merge makeit's settings into a very long path name", plan.Failed))
	for _, l := range lines(m) {
		if w := len([]rune(stripANSI(l))); w > 20 {
			t.Errorf("row wider than the terminal (%d): %q", w, l)
		}
	}
}

type fakeHolder struct{ calls []string }

func (f *fakeHolder) ReleaseTerminal() error { f.calls = append(f.calls, "release"); return nil }
func (f *fakeHolder) RestoreTerminal() error { f.calls = append(f.calls, "restore"); return nil }

func TestHoldReleasesTerminal(t *testing.T) {
	f := &fakeHolder{}
	l := &LiveView{term: f}
	resume := l.Hold()
	resume()
	if strings.Join(f.calls, ",") != "release,restore" {
		t.Errorf("calls %v, want release then restore", f.calls)
	}
}

func stripANSI(s string) string { return ansi.Strip(s) }
