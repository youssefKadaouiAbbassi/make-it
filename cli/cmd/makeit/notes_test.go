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

// Every change is listed with both versions, aligned, and the result counts them.
func TestUpdateLiveListsChanges(t *testing.T) {
	var out bytes.Buffer
	u := ui.NewFromReader(nil, &out, false)
	u.Mode = ui.Live
	rep := setup.Report{Generation: 4, Plan: plan.Report{Outcomes: []plan.Outcome{{ID: "catalog", Status: plan.Done}}}, Changes: []state.Change{
		{Kind: "claude-code", Item: "Claude Code", From: "2.1.317", To: "2.1.320"},
		{Kind: "plugin", Item: "superpowers", From: "7.0.1", To: "7.0.2"},
		{Kind: "tool", Item: "grype", From: "0.121.1", To: "0.122.0"},
	}}
	finishLive(u, "update", rep, &selfMove{From: "0.3.1", To: "0.3.2"}, time.Second)
	s := out.String()
	for _, want := range []string{"Changed", "makeit       0.3.1 → 0.3.2", "Claude Code  2.1.317 → 2.1.320", "superpowers  7.0.1 → 7.0.2", "grype        0.121.1 → 0.122.0", "4 changes"} {
		if !strings.Contains(s, want) {
			t.Errorf("no %q in:\n%s", want, s)
		}
	}
	if strings.Index(s, "Changed") > strings.Index(s, "4 changes") {
		t.Errorf("the list comes after the result:\n%s", s)
	}
}

// Nothing moved: no list.
func TestNoChangesNoList(t *testing.T) {
	var out bytes.Buffer
	u := ui.NewFromReader(nil, &out, false)
	u.Mode = ui.Live
	finishLive(u, "update", setup.Report{}, nil, time.Second)
	if strings.Contains(out.String(), "Changed") {
		t.Errorf("a list with nothing in it:\n%s", out.String())
	}
}

// Plain mode and the log get one line per change.
func TestPlainListsChanges(t *testing.T) {
	var out bytes.Buffer
	printChangesPlain(&out, []state.Change{{Kind: "plugin", Item: "superpowers", From: "7.0.1", To: "7.0.2"}, {Kind: "tool", Item: "prek", To: "0.6.0"}})
	if s := out.String(); s != "changed: superpowers 7.0.1 → 7.0.2\nchanged: prek → 0.6.0\n" {
		t.Errorf("plain %q", s)
	}
}

// update's closing lines name rollback only when the run has something it undoes.
func TestUpdateUndoHintOnlyWhenUndoable(t *testing.T) {
	npmOnly := setup.Report{Generation: 5, Changes: []state.Change{{Kind: "npm", Item: "pyright", From: "1.1.413", To: "1.1.414"}}}
	for _, c := range []struct {
		name string
		rep  setup.Report
		self *selfMove
		want bool
	}{
		{"npm only", npmOnly, nil, false},
		{"undoable run", setup.Report{Generation: 6, Undoable: true}, nil, true},
		{"makeit moved", npmOnly, &selfMove{From: "0.3.5", To: "0.3.6"}, true},
	} {
		var out bytes.Buffer
		u := ui.NewFromReader(nil, &out, false)
		u.Mode = ui.Live
		finishLive(u, "update", c.rep, c.self, time.Second)
		if got := strings.Contains(out.String(), "makeit rollback"); got != c.want {
			t.Errorf("%s: undo hint %v, want %v:\n%s", c.name, got, c.want, out.String())
		}
	}
}
