package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"makeit/internal/plan"
	"makeit/internal/setup"
	"makeit/internal/state"
	"makeit/internal/ui"
)

// In the live view the notes come before the result, as rows: one line for the optional programs,
// not one wrapped line each.
func TestNotesLive(t *testing.T) {
	var out bytes.Buffer
	u := ui.NewFromReader(nil, &out, false)
	u.Mode = ui.Live
	printNotesLive(u, setup.Report{
		NeedsYou: []setup.NeedsYou{{ID: "chrome", Manual: "Google Chrome or Chrome for Testing: https://www.google.com/chrome/"}},
		Kept:     []setup.Kept{{Item: "~/.claude/CLAUDE.md", Reason: "you changed it"}},
		Hints:    []setup.Hint{{ID: "makeit@makeit", Rule: "in-place", Text: "loads in place from /c/plugins/makeit"}},
	})
	s := out.String()
	if strings.Contains(s, "loads in place") {
		t.Errorf("a hint is background, not a warning row:\n%s", s)
	}
	for _, want := range []string{"! chrome", "needs you", "https://www.google.com/chrome/", "! Kept", "~/.claude/CLAUDE.md"} {
		if !strings.Contains(s, want) {
			t.Errorf("no %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, "install by hand") || strings.Contains(s, "Optional") {
		t.Errorf("the plain lines leaked into the live view:\n%s", s)
	}
}

// A failed run lists every failed step with its whole error, wrapped, never cut: the line the
// result points to must say exactly what went wrong.
func TestFinishLiveShowsEveryFailure(t *testing.T) {
	var out bytes.Buffer
	u := ui.NewFromReader(nil, &out, false)
	u.Mode, u.Width = ui.Live, 50
	long := "claude plugin update superpowers@makeit: Failed to clone repository: Host key verification failed. fatal: Could not read from remote repository."
	rep := setup.Report{Plan: plan.Report{Outcomes: []plan.Outcome{
		{ID: "plugin.superpowers@makeit", Title: "Update superpowers@makeit", Status: plan.Failed, Detail: long},
		{ID: "plugin.ponytail@makeit", Title: "Update ponytail@makeit", Status: plan.Failed, Detail: "timed out"},
		{ID: "settings", Title: "Merge settings", Status: plan.Done},
	}}}
	finishLive(u, "update", rep, nil, time.Second)
	s := strings.Join(strings.Fields(out.String()), " ")
	for _, want := range []string{"✗ Update superpowers@makeit", "Could not read from remote repository.", "✗ Update ponytail@makeit", "timed out", "Stopped"} {
		if !strings.Contains(s, want) {
			t.Errorf("no %q in:\n%s", want, out.String())
		}
	}
}

// Update's result names what moved; counts only when nothing did.
func TestFinishLiveNamesMoves(t *testing.T) {
	pin := func(id string) state.PinMove { return state.PinMove{ID: id + "@makeit", FromTag: "v1", ToTag: "v2"} }
	done := func(ids ...string) []plan.Outcome {
		var outs []plan.Outcome
		for _, id := range ids {
			outs = append(outs, plan.Outcome{ID: id, Title: id, Status: plan.Done})
		}
		return outs
	}
	for _, tc := range []struct {
		name string
		self *selfMove
		pins []state.PinMove
		outs []plan.Outcome
		want string
	}{
		{"makeit and two plugins", &selfMove{From: "0.2.1", To: "0.3.0"}, []state.PinMove{pin("superpowers"), pin("ponytail")},
			done("plugin.superpowers@makeit", "plugin.ponytail@makeit"), "makeit 0.2.1 → 0.3.0 and superpowers, ponytail moved to new releases"},
		{"three plugins", nil, []state.PinMove{pin("superpowers"), pin("ponytail"), pin("deja-vu")},
			done("plugin.superpowers@makeit", "plugin.ponytail@makeit", "plugin.deja-vu@makeit"), "3 plugins moved to new releases"},
		{"makeit and other changes", &selfMove{From: "0.2.1", To: "0.3.0"}, nil, done("settings", "global.CLAUDE.md"), "makeit 0.2.1 → 0.3.0 and 2 other changes"},
		{"nothing moved", nil, nil, done("settings", "a", "b", "c"), "4 changed"},
	} {
		var out bytes.Buffer
		u := ui.NewFromReader(nil, &out, false)
		u.Mode = ui.Live
		finishLive(u, "update", setup.Report{Plan: plan.Report{Outcomes: tc.outs}, Pins: tc.pins, Generation: 1}, tc.self, time.Second)
		if !strings.Contains(out.String(), tc.want) {
			t.Errorf("%s: no %q in:\n%s", tc.name, tc.want, out.String())
		}
	}
}

// A run that just put the PATH line in the profile says to open a new terminal.
func TestFinishLiveSaysNewTerminal(t *testing.T) {
	var out bytes.Buffer
	u := ui.NewFromReader(nil, &out, false)
	u.Mode = ui.Live
	rep := setup.Report{Generation: 1, Plan: plan.Report{Outcomes: []plan.Outcome{{ID: "path.profile", Title: "Load ~/.makeit/env from the shell profile", Status: plan.Done}}}}
	finishLive(u, "setup", rep, nil, time.Second)
	if !strings.Contains(out.String(), "Open a new terminal to pick up PATH.") {
		t.Errorf("no new-terminal line in:\n%s", out.String())
	}
}

// Nothing to do, but a tool was left to the user: the result says so instead of "everything is current".
func TestFinishLiveNothingToDoNamesNeedsYou(t *testing.T) {
	var out bytes.Buffer
	u := ui.NewFromReader(nil, &out, false)
	u.Mode = ui.Live
	finishLive(u, "setup", setup.Report{NeedsYou: []setup.NeedsYou{{ID: "chrome", Manual: "x"}}}, nil, time.Second)
	if s := out.String(); !strings.Contains(s, "1 tool needs you") || strings.Contains(s, "everything is current") {
		t.Errorf("result:\n%s", s)
	}
}
