package main

import (
	"bytes"
	"strings"
	"testing"

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
	for _, want := range []string{"! Optional", "gh, pyright", "makeit doctor shows how", "! Kept", "~/.claude/CLAUDE.md"} {
		if !strings.Contains(s, want) {
			t.Errorf("no %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, "install by hand") || strings.Contains(s, "cli.github.com") {
		t.Errorf("the plain lines leaked into the live view:\n%s", s)
	}
}
