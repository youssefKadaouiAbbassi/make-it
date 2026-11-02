package main

import (
	"fmt"
	"strings"
	"time"

	"makeit/internal/plan"
	"makeit/internal/setup"
	"makeit/internal/ui"
)

// area is one row of the live plan: the steps of one group (Machine, Plugins, ...).
type area struct {
	name                    string
	change, already, failed int
	first                   string
}

func areasOf(outs []plan.Outcome) []*area {
	var as []*area
	find := func(name string) *area {
		for _, a := range as {
			if a.name == name {
				return a
			}
		}
		a := &area{name: name}
		as = append(as, a)
		return a
	}
	for _, o := range outs {
		a := find(ui.Group(o.ID, o.Title))
		switch o.Status {
		case plan.WouldChange, plan.Done:
			if a.change == 0 {
				a.first = o.Title
			}
			a.change++
		case plan.Failed, plan.Interrupted:
			a.failed++
		default:
			a.already++
		}
	}
	return as
}

// printAreas is the plan in live mode: one row per area, what will change or that it is current.
func printAreas(u *ui.UI, outs []plan.Outcome) {
	for _, a := range areasOf(outs) {
		switch {
		case a.failed > 0:
			u.Row(ui.GFail, a.name, "cannot check", "")
		case a.change == 1:
			u.Row(ui.GChange, a.name, lowerFirst(a.first), "")
		case a.change > 1:
			u.Row(ui.GChange, a.name, fmt.Sprintf("%d changes", a.change), "")
		default:
			u.Row(ui.GDone, a.name, "up to date", "")
		}
	}
	_, _ = fmt.Fprintln(u.Out)
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	if r[0] >= 'A' && r[0] <= 'Z' {
		r[0] += 'a' - 'A'
	}
	return string(r)
}

// verbs are the result words of the commands that change things.
var verbs = map[string]string{"setup": "Ready", "update": "Updated", "uninstall": "Removed", "rollback": "Rolled back", "init": "Project set up"}

// finishLive closes a live run: the result line with its counts and the next step.
// printNotesLive shows a run's notes as rows above its result: the optional programs left out on
// one line (makeit doctor prints how to install each), each file kept, each hint.
func printNotesLive(u *ui.UI, rep setup.Report) {
	if len(rep.Skipped) > 0 {
		ids := make([]string, len(rep.Skipped))
		for i, s := range rep.Skipped {
			ids[i] = s.ID
		}
		u.Row(ui.GWarn, "Optional", strings.Join(ids, ", "), "see makeit doctor")
	}
	for _, k := range rep.Kept {
		u.Row(ui.GWarn, "Kept", k.Item, k.Reason)
	}
	for _, h := range rep.Hints {
		u.Row(ui.GWarn, h.ID, h.Text, "")
	}
}

func finishLive(u *ui.UI, name string, rep setup.Report, elapsed time.Duration) {
	if rep.Generation == 0 && !rep.Plan.Failed() {
		u.Result(true, "Nothing to do", elapsed, []string{"everything is current"})
		return
	}
	changed, already := 0, 0
	for _, o := range rep.Plan.Outcomes {
		switch o.Status {
		case plan.Done:
			changed++
		case plan.Already:
			already++
		}
	}
	verb := verbs[name]
	if verb == "" {
		verb = "Done"
	}
	counts := []string{fmt.Sprintf("%d changed", changed)}
	if already > 0 {
		counts = append(counts, fmt.Sprintf("%d already", already))
	}
	if rep.Plan.Failed() {
		// The live rows keep one cut line per area; here is every failed step, whole.
		_, _ = fmt.Fprintln(u.Out)
		for _, o := range rep.Plan.Outcomes {
			if o.Status == plan.Failed {
				u.Row(ui.GFail, o.Title, "", "")
				u.Detail(o.Detail)
			}
		}
		u.Result(false, "Stopped", elapsed, counts)
		u.Next("Fix the ✗ line above, then run makeit " + name + " again.")
		return
	}
	u.Result(true, verb, elapsed, counts)
	switch {
	case name == "setup":
		u.Next("Open Claude Code to start.", "undo: makeit rollback")
	case name == "update" && rep.Reload:
		u.Next("Restart Claude Code to load the changes.", "undo: makeit rollback")
	case name == "update":
		u.Next("", "undo: makeit rollback")
	case name == "init":
		u.Next("In Claude Code, run /makeit:init for the project facts and the checks.")
	}
}
