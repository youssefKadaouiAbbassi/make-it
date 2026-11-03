package main

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"makeit/internal/plan"
	"makeit/internal/setup"
	"makeit/internal/state"
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
	// A tool with no route on this machine: the one thing makeit leaves to the user.
	for _, n := range rep.NeedsYou {
		u.Row(ui.GWarn, n.ID, "needs you", n.Manual)
	}
	for _, k := range rep.Kept {
		u.Row(ui.GWarn, "Kept", k.Item, k.Reason)
	}
	// Hints (how an adopted plugin gets its updates) are background: plain mode and the log keep them.
}

// movedPhrase names what an update moved: makeit first, then up to two plugins by name, more as
// a count, then the other changed steps as a count. "" when nothing moved.
func movedPhrase(self *selfMove, pins []state.PinMove, others int) string {
	var parts []string
	if self != nil {
		parts = append(parts, "makeit "+self.From+" → "+self.To)
	}
	switch n := len(pins); {
	case n == 0:
	case n <= 2:
		names := make([]string, n)
		for i, p := range pins {
			names[i], _, _ = strings.Cut(p.ID, "@")
		}
		parts = append(parts, strings.Join(names, ", ")+" moved to new releases")
	default:
		parts = append(parts, fmt.Sprintf("%d plugins moved to new releases", n))
	}
	if len(parts) == 0 {
		return ""
	}
	if others > 0 {
		parts = append(parts, plural(others, "other change", "other changes"))
	}
	return strings.Join(parts, " and ")
}

func finishLive(u *ui.UI, name string, rep setup.Report, self *selfMove, elapsed time.Duration) {
	if rep.Generation == 0 && !rep.Plan.Failed() {
		note := []string{"everything is current"}
		if n := len(rep.NeedsYou); n > 0 {
			note = []string{plural(n, "tool needs you", "tools need you")}
		}
		u.Result(true, "Nothing to do", elapsed, note)
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
	// The steps that moved a plugin are named, not counted.
	others := changed
	for _, p := range rep.Pins {
		for _, o := range rep.Plan.Outcomes {
			if o.ID == "plugin."+p.ID && o.Status == plan.Done {
				others--
			}
		}
	}
	if phrase := movedPhrase(self, rep.Pins, others); phrase != "" {
		counts = []string{phrase}
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
	if slices.ContainsFunc(rep.Plan.Outcomes, func(o plan.Outcome) bool { return o.ID == "path.profile" && o.Status == plan.Done }) {
		u.Next("Open a new terminal to pick up PATH.")
	}
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
