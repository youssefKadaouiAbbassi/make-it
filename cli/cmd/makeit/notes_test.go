package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"makeit/internal/plan"
	"makeit/internal/setup"
	"makeit/internal/ui"
)

// In the live view the notes come before the result, as rows: one line for the optional programs,
// not one wrapped line each.
func TestNotesLive(t *testing.T) {
	var out bytes.Buffer
	u := ui.NewFromReader(nil, &out, false)
	u.Mode = ui.Live
	printNotesLive(u, setup.Report{
		Skipped: []setup.Skipped{{ID: "gh", Manual: "https://cli.github.com"}, {ID: "pyright", Manual: "npm install -g pyright"}},
		Kept:    []setup.Kept{{Item: "~/.claude/CLAUDE.md", Reason: "you changed it"}},
	})
	s := out.String()
	for _, want := range []string{"! Optional", "gh, pyright", "see makeit doctor", "! Kept", "~/.claude/CLAUDE.md"} {
		if !strings.Contains(s, want) {
			t.Errorf("no %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, "install by hand") || strings.Contains(s, "cli.github.com") {
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
	finishLive(u, "update", rep, time.Second)
	s := strings.Join(strings.Fields(out.String()), " ")
	for _, want := range []string{"✗ Update superpowers@makeit", "Could not read from remote repository.", "✗ Update ponytail@makeit", "timed out", "Stopped"} {
		if !strings.Contains(s, want) {
			t.Errorf("no %q in:\n%s", want, out.String())
		}
	}
}
