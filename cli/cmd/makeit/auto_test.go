package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"makeit/internal/plan"
	"makeit/internal/setup"
	"makeit/internal/state"
)

var now = time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)

func TestAutoDueFresh(t *testing.T) {
	if due, why := autoDue(t.TempDir(), now); !due {
		t.Errorf("a machine that never ran it is not due: %s", why)
	}
}

func TestAutoDueRecent(t *testing.T) {
	stack := t.TempDir()
	write(t, filepath.Join(stack, "last-auto"), now.Add(-2*time.Hour).Format(time.RFC3339))
	if due, why := autoDue(stack, now); due || !strings.Contains(why, "2h") {
		t.Errorf("due %v, why %q; want not due, ran 2h ago", due, why)
	}
	write(t, filepath.Join(stack, "last-auto"), now.Add(-10*time.Minute).Format(time.RFC3339))
	if due, why := autoDue(stack, now); due || !strings.Contains(why, "0h") {
		t.Errorf("10 minutes after a run: due %v, why %q", due, why)
	}
	write(t, filepath.Join(stack, "last-auto"), now.Add(-25*time.Hour).Format(time.RFC3339))
	if due, _ := autoDue(stack, now); !due {
		t.Error("25h after the last run it is due again")
	}
}

func TestAutoDueOff(t *testing.T) {
	stack := t.TempDir()
	write(t, filepath.Join(stack, "config.json"), `{"autoUpdate": false}`)
	if due, why := autoDue(stack, now); due || !strings.Contains(why, "off") {
		t.Errorf("due %v, why %q; want off", due, why)
	}
}

// Two Claude Code sessions start at once: the second auto run sees the first one's lock.
func TestAutoDueLocked(t *testing.T) {
	stack := t.TempDir()
	write(t, filepath.Join(stack, "auto.lock"), fmt.Sprintf("%d %d\n", os.Getpid(), time.Now().Unix()))
	if due, why := autoDue(stack, now); due || !strings.Contains(why, "running") {
		t.Errorf("due %v, why %q; want not due while another runs", due, why)
	}
}

func TestAutoNote(t *testing.T) {
	rep := setup.Report{Generation: 3, Plan: plan.Report{Outcomes: []plan.Outcome{{ID: "catalog", Status: plan.Done}}}}
	pins := []state.PinMove{{ID: "superpowers@makeit", ToTag: "v7.1.0"}, {ID: "chrome-devtools-mcp@makeit", ToTag: "chrome-devtools-mcp-v1.13.0"}}
	got := autoNote(rep, pins, &selfMove{From: "0.2.0", To: "0.2.1"})
	want := "makeit updated: makeit 0.2.1, superpowers 7.1.0, chrome-devtools-mcp 1.13.0 · undo: makeit rollback"
	if got != want {
		t.Errorf("note\n%q\nwant\n%q", got, want)
	}
	if n := autoNote(setup.Report{}, nil, nil); n != "" {
		t.Errorf("a run that changed nothing left a note: %q", n)
	}
}
