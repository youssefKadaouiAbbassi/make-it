package main

import (
	"fmt"
	"io"
	"slices"
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

// allChanges are makeit's own move, then the run's.
func allChanges(self *selfMove, rep setup.Report) []state.Change {
	var out []state.Change
	if self != nil {
		out = append(out, state.Change{Kind: "makeit", Item: "makeit", From: self.From, To: self.To})
	}
	return append(out, rep.Changes...)
}

// changeText is "from → to", or "→ to" when the earlier version is not known.
func changeText(c state.Change) string {
	if c.From == "" {
		return "→ " + c.To
	}
	return c.From + " → " + c.To
}

// printChangesLive lists every change under "Changed", the names in one column.
func printChangesLive(u *ui.UI, changes []state.Change) {
	if len(changes) == 0 {
		return
	}
	w := 0
	for _, c := range changes {
		w = max(w, len(c.Item))
	}
	_, _ = fmt.Fprintln(u.Out)
	_, _ = fmt.Fprintln(u.Out, "  Changed")
	for _, c := range changes {
		_, _ = fmt.Fprintf(u.Out, "    %-*s  %s\n", w, c.Item, changeText(c))
	}
}

// printChangesPlain is one line per change, for plain mode and update.log.
func printChangesPlain(w io.Writer, changes []state.Change) {
	for _, c := range changes {
		_, _ = fmt.Fprintf(w, "changed: %s %s\n", c.Item, changeText(c))
	}
}

func finishLive(u *ui.UI, name string, rep setup.Report, self *selfMove, elapsed time.Duration) {
	changes := allChanges(self, rep)
	printChangesLive(u, changes)
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
	// The list above names every version that moved; the result counts them.
	if len(changes) > 0 {
		counts = []string{plural(len(changes), "change", "changes")}
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
	// Rollback is named only when it has this run to undo, or the makeit it replaced to put back.
	next := func(line string) {
		lines := []string{line}
		if rep.Undoable || self != nil {
			lines = append(lines, "undo: makeit rollback")
		}
		if line != "" || len(lines) > 1 {
			u.Next(lines...)
		}
	}
	switch {
	case name == "setup":
		next("Open Claude Code to start.")
	case name == "update" && rep.Reload:
		next("Restart Claude Code to load the changes.")
	case name == "update":
		next("")
	case name == "init":
		u.Next("In Claude Code, run /makeit:init for the project facts and the checks.")
	}
}
