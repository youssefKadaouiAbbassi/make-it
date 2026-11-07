package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"makeit/internal/state"
)

var now = time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)

func TestAutoDueFresh(t *testing.T) {
	if due, why := due(t.TempDir(), now); !due {
		t.Errorf("a machine that never ran it is not due: %s", why)
	}
}

func TestAutoDueRecent(t *testing.T) {
	stack := t.TempDir()
	write(t, filepath.Join(stack, "last-auto"), now.Add(-2*time.Hour).Format(time.RFC3339))
	if due, why := due(stack, now); due || !strings.Contains(why, "2h") {
		t.Errorf("due %v, why %q; want not due, ran 2h ago", due, why)
	}
	write(t, filepath.Join(stack, "last-auto"), now.Add(-10*time.Minute).Format(time.RFC3339))
	if due, why := due(stack, now); due || !strings.Contains(why, "0h") {
		t.Errorf("10 minutes after a run: due %v, why %q", due, why)
	}
	write(t, filepath.Join(stack, "last-auto"), now.Add(-25*time.Hour).Format(time.RFC3339))
	if due, _ := due(stack, now); !due {
		t.Error("25h after the last run it is due again")
	}
}

func TestAutoDueOff(t *testing.T) {
	stack := t.TempDir()
	write(t, filepath.Join(stack, "config.json"), `{"autoUpdate": false}`)
	if due, why := due(stack, now); due || !strings.Contains(why, "off") {
		t.Errorf("due %v, why %q; want off", due, why)
	}
}

// Two Claude Code sessions start at once: the second auto run sees the first one's lock.
func TestAutoDueLocked(t *testing.T) {
	stack := t.TempDir()
	write(t, filepath.Join(stack, "auto.lock"), fmt.Sprintf("%d %d\n", os.Getpid(), time.Now().Unix()))
	if due, why := due(stack, now); due || !strings.Contains(why, "running") {
		t.Errorf("due %v, why %q; want not due while another runs", due, why)
	}
}

func TestAutoNote(t *testing.T) {
	changes := []state.Change{
		{Kind: "makeit", Item: "makeit", From: "0.2.0", To: "0.2.1"},
		{Kind: "plugin", Item: "superpowers", From: "7.0.1", To: "7.1.0"},
		{Kind: "tool", Item: "grype", From: "", To: "0.122.0"},
	}
	want := "makeit updated: makeit 0.2.0 → 0.2.1, superpowers 7.0.1 → 7.1.0, grype → 0.122.0 · undo: makeit rollback"
	if got := autoNote(changes, true); got != want {
		t.Errorf("note\n%q\nwant\n%q", got, want)
	}
	if n := autoNote(nil, false); n != "" {
		t.Errorf("a run that changed nothing left a note: %q", n)
	}
}

// A daily run that only moved npm tools has nothing rollback undoes: no undo hint.
func TestAutoNoteNoUndoWhenNothingToUndo(t *testing.T) {
	got := autoNote([]state.Change{{Kind: "npm", Item: "pyright", From: "1.1.413", To: "1.1.414"}}, false)
	if want := "makeit updated: pyright 1.1.413 → 1.1.414"; got != want {
		t.Errorf("note %q, want %q", got, want)
	}
	// makeit itself moved: rollback can put the old binary back.
	if got := autoNote([]state.Change{{Kind: "makeit", Item: "makeit", From: "0.3.5", To: "0.3.6"}}, false); !strings.Contains(got, "undo: makeit rollback") {
		t.Errorf("note %q, want the undo hint after a self update", got)
	}
}

// due is autoDue for the tests that only ask whether a run is due: the lock it holds is let go.
func due(stack string, now time.Time) (bool, string) {
	lock, why := autoDue(stack, now)
	if lock == nil {
		return false, why
	}
	lock.Release()
	return true, why
}

// The check runs under the lock the run then keeps: a second start while the first runs is not
// due, so two sessions starting at once never run the daily update twice.
func TestAutoDueHoldsTheLock(t *testing.T) {
	stack := t.TempDir()
	first, _ := autoDue(stack, now)
	if first == nil {
		t.Fatal("not due on a fresh machine")
	}
	if second, why := autoDue(stack, now); second != nil || !strings.Contains(why, "running") {
		t.Errorf("a second start while the first holds the lock: lock %v, why %q", second, why)
	}
	first.Release()
}
