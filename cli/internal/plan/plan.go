// Package plan runs a list of steps with one consent rule and one report shape. Every modifying
// command builds its steps first, prints them, gets consent, then applies them in order.
package plan

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Status of one step after a run.
type Status string

// Step statuses.
const (
	Done        Status = "done"
	Already     Status = "already"
	WouldChange Status = "would-change"
	Skipped     Status = "skipped"
	Failed      Status = "failed"
	Interrupted Status = "interrupted"
)

// ErrNotYet is returned by Already when the check depends on an earlier step that has not run.
var ErrNotYet = errors.New("runs after an earlier step; cannot be checked yet")

// ErrSkip is returned by Run when the user declined the step.
var ErrSkip = errors.New("skipped")

// Step is one unit of work. Already must not modify anything: dry runs call it.
type Step struct {
	ID     string
	Title  string
	Detail string
	// Command is the argv a step runs, shown in the plan; optional.
	Command []string
	Already func(ctx context.Context) (bool, error)
	Run     func(ctx context.Context) (string, error)
}

// Outcome is one line of the report.
type Outcome struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Status  Status   `json:"status"`
	Detail  string   `json:"detail,omitempty"`
	Command []string `json:"command,omitempty"`
}

// Options for Execute.
type Options struct {
	DryRun bool
	// KeepGoing runs the remaining steps after a failure (uninstall removes what it can).
	KeepGoing bool
	// After is called after each step, for the state journal and progress lines.
	After func(Outcome)
}

// Report is the result of Execute.
type Report struct{ Outcomes []Outcome }

// Failed reports whether any step failed or was interrupted.
func (r Report) Failed() bool {
	for _, o := range r.Outcomes {
		if o.Status == Failed || o.Status == Interrupted {
			return true
		}
	}
	return false
}

// Summary counts outcomes, such as "2 done, 1 already".
func (r Report) Summary() string {
	order := []Status{Done, WouldChange, Already, Skipped, Failed, Interrupted}
	n := map[Status]int{}
	for _, o := range r.Outcomes {
		n[o.Status]++
	}
	var parts []string
	for _, s := range order {
		if n[s] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n[s], s))
		}
	}
	if len(parts) == 0 {
		return "nothing to do"
	}
	return strings.Join(parts, ", ")
}

// Execute runs the steps. A dry run only calls Already.
func Execute(ctx context.Context, steps []Step, opts Options) Report {
	var rep Report
	emit := func(o Outcome) {
		rep.Outcomes = append(rep.Outcomes, o)
		if opts.After != nil {
			opts.After(o)
		}
	}
	for i, s := range steps {
		o := Outcome{ID: s.ID, Title: s.Title, Command: s.Command}
		if ctx.Err() != nil {
			for _, rest := range steps[i:] {
				emit(Outcome{ID: rest.ID, Title: rest.Title, Status: Interrupted, Detail: "not started: interrupted"})
			}
			return rep
		}
		if s.Already != nil {
			done, err := s.Already(ctx)
			switch {
			case errors.Is(err, ErrNotYet):
				if opts.DryRun {
					o.Status, o.Detail = WouldChange, err.Error()
					emit(o)
					continue
				}
			case err != nil:
				o.Status, o.Detail = Failed, "check failed: "+err.Error()
				emit(o)
				if !opts.KeepGoing {
					return rep
				}
				continue
			case done:
				o.Status = Already
				emit(o)
				continue
			}
		}
		if opts.DryRun {
			o.Status, o.Detail = WouldChange, s.Detail
			emit(o)
			continue
		}
		detail, err := s.Run(ctx)
		o.Detail = detail
		switch {
		case errors.Is(err, ErrSkip):
			o.Status = Skipped
		case err != nil && ctx.Err() != nil:
			o.Status, o.Detail = Interrupted, err.Error()
		case err != nil:
			o.Status, o.Detail = Failed, err.Error()
		default:
			o.Status = Done
		}
		emit(o)
		if (o.Status == Failed && !opts.KeepGoing) || o.Status == Interrupted {
			for _, rest := range steps[i+1:] {
				emit(Outcome{ID: rest.ID, Title: rest.Title, Status: Skipped, Detail: "not started: an earlier step " + string(o.Status)})
			}
			return rep
		}
	}
	return rep
}

// Mode is how a modifying command proceeds.
type Mode int

// Modes, from the consent rule.
const (
	// DryRunOnly prints the plan and exits 0.
	DryRunOnly Mode = iota
	// Apply applies without asking: --yes was given.
	Apply
	// Prompt prints the plan and asks once on the terminal.
	Prompt
	// NeedConsent prints the plan and exits 2: no terminal and no --yes.
	NeedConsent
)

// Decide applies the consent rule. interactive is true only when stdin and stderr are
// terminals and none of --no-input and --json was given.
func Decide(interactive, yes, dryRun bool) Mode {
	switch {
	case dryRun:
		return DryRunOnly
	case yes:
		return Apply
	case interactive:
		return Prompt
	default:
		return NeedConsent
	}
}

// ConsentMessage is printed with exit 2.
const ConsentMessage = "stdin is not a terminal: nothing was changed. Run again with --yes to apply this plan, or --dry-run to only print it"
