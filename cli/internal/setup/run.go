package setup

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"time"

	"makeit/internal/catalog"
	"makeit/internal/fsx"
	"makeit/internal/plan"
	"makeit/internal/pluginrule"
	"makeit/internal/state"
)

// ErrConsent means the plan needs consent that no one can give here (no terminal, no --yes).
var ErrConsent = errors.New(plan.ConsentMessage)

// ErrDeclined means the person at the terminal said no to the plan.
var ErrDeclined = errors.New("declined: nothing was changed")

// Report is what a run did, or would do.
type Report struct {
	Plan plan.Report
	// Hints say how each adopted stack plugin that makeit does not update gets its updates.
	Hints []Hint
	// Kept are the items the run left alone, each with the reason.
	Kept []Kept
	// Reload: a plugin version or pin moved, or the catalog changed; Claude Code needs a restart or
	// /reload-plugins to load it.
	Reload bool
	// Generation is the run's generation, 0 when nothing was applied.
	Generation int
	// Pins are the plugin release moves the run recorded (the plugin really updated).
	Pins []state.PinMove
	// NeedsYou are the tools with no install route on this machine, each with its manual route.
	NeedsYou []NeedsYou
	// Changes are the versions the run moved (tools and Claude Code; plugins are in Pins).
	Changes []state.Change
}

// NeedsYou is a tool makeit cannot install on this machine.
type NeedsYou struct {
	ID     string `json:"id"`
	Manual string `json:"manual"`
}

// Hint is how an adopted stack plugin gets its updates.
type Hint struct {
	ID   string `json:"id"`
	Rule string `json:"rule"`
	Text string `json:"text"`
}

// Kept is an item a run left alone, and why.
type Kept struct {
	Item   string `json:"item"`
	Reason string `json:"reason"`
}

// phase is a list of steps. A phase starts only when no earlier phase failed; keepGoing lets its
// own steps continue past a failed one.
type phase struct {
	steps     []plan.Step
	keepGoing bool
}

// Run plans setup and, as mode allows, applies it. confirm is asked once, with the plan, in
// Prompt mode. A plan with nothing to do applies nothing and makes no generation.
func Run(ctx context.Context, e Env, mode plan.Mode, confirm func([]plan.Outcome) (bool, error), argv []string) (Report, error) {
	rep, _, err := execute(ctx, e, "setup", mode, confirm, argv, func(en *env) []phase {
		return []phase{{steps: en.setupRunSteps()}}
	})
	return rep, err
}

// setupRunSteps are setup's steps, ending with the bundle's missing dependencies: an install cut
// short leaves the bundle listed without all of them, and the bundle step only sees the bundle.
func (e *env) setupRunSteps() []plan.Step {
	declared, derr := pluginrule.BundleIDs(e.Catalog, e.req)
	return append(e.setupSteps(e.globalSteps()), e.bundleDepsStep(declared, derr))
}

// setupSteps are setup's steps in order, with global the global-file steps of the command.
func (e *env) setupSteps(global []plan.Step) []plan.Step {
	steps := append(e.prereqSteps(), e.toolUpgradeSteps()...)
	steps = append(steps, e.claudeStep(), e.claudeUpgradeStep())
	steps = append(steps, e.stackSteps()...)
	steps = append(steps, e.settingsStep())
	steps = append(steps, global...)
	return append(steps, e.pathSteps()...)
}

// execute is the run every modifying command shares: the lock, state, one dry pass over every
// phase for the plan, one consent, one generation, and state journaled after each step.
func execute(ctx context.Context, e Env, command string, mode plan.Mode, confirm func([]plan.Outcome) (bool, error),
	argv []string, build func(*env) []phase) (Report, *env, error) {
	// A run that may apply holds the lock from before it reads state, so it never plans on state
	// another run is about to change. A dry run and a run that only prints its plan take none.
	if mode == plan.Apply || mode == plan.Prompt {
		lock, err := fsx.Acquire(filepath.Join(e.Roots.Stack, "lock"), time.Now())
		if err != nil {
			return Report{}, nil, err
		}
		defer lock.Release()
	}
	statePath := filepath.Join(e.Roots.Stack, "state.json")
	st, _, err := state.LoadState(statePath)
	if err != nil {
		return Report{}, nil, err
	}
	req, err := catalog.LoadRequires(e.Catalog)
	if err != nil {
		return Report{}, nil, err
	}
	en := &env{Env: e, st: st, req: req, command: command}
	phases := build(en)
	rep := Report{Plan: runPhases(ctx, phases, plan.Options{DryRun: true})}
	rep.Hints, rep.Kept, rep.NeedsYou = en.hints, en.kept, en.needsYou
	if nothingToDo(rep.Plan) || rep.Plan.Failed() {
		return rep, en, nil
	}
	switch mode {
	case plan.DryRunOnly:
		return rep, en, nil
	case plan.NeedConsent:
		return rep, en, ErrConsent
	case plan.Prompt:
		yes, err := confirm(rep.Plan.Outcomes)
		if err != nil {
			return rep, en, err
		}
		if !yes {
			return rep, en, ErrDeclined
		}
	}

	gen, err := state.NewGeneration(e.Roots.Stack, st.Generation, command, argv)
	if err != nil {
		return rep, en, err
	}
	en.gen = gen
	st.Generation = gen.Manifest.Generation
	st.Machine = state.Machine{OS: e.Platform.OS, Arch: e.Platform.Arch, WSL: e.Platform.WSL}
	if st.Steps == nil {
		st.Steps = map[string]*state.StepRecord{}
	}
	// The apply pass checks every step again, so it reports what it keeps afresh.
	// needsYou is decided when the plan is built, once; hints and kept come from the steps,
	// which run again now.
	en.hints, en.kept, en.changes = nil, nil, nil
	var saveErr error
	rep.Plan = runPhases(ctx, phases, plan.Options{Before: func(s plan.Step) {
		if e.Progress != nil {
			e.Progress(plan.Event{Step: s})
		}
	}, After: func(o plan.Outcome) {
		if e.Progress != nil {
			e.Progress(plan.Event{Step: plan.Step{ID: o.ID, Title: o.Title}, Outcome: &o})
		}
		r := &state.StepRecord{Status: string(o.Status), At: time.Now().UTC()}
		if o.Status == plan.Failed || o.Status == plan.Interrupted {
			r.Error = o.Detail
		}
		st.Steps[o.ID] = r
		// The journal: after every step, so an interrupted run leaves a true record; never once
		// uninstall has removed the record.
		if !en.stateRemoved {
			saveErr = errors.Join(saveErr, state.SaveState(statePath, st))
		}
	}})
	rep.Hints, rep.Kept, rep.NeedsYou, rep.Reload = en.hints, en.kept, en.needsYou, en.reload
	rep.Generation = gen.Manifest.Generation
	// Only the moves whose plugin really updated: the next session announces them, rollback undoes them.
	gen.Manifest.Pins = updatedPins(e.Pins, gen.Manifest.Plugins.Updated)
	rep.Pins = gen.Manifest.Pins
	gen.Manifest.Changes = append(gen.Manifest.Changes, en.changes...)
	rep.Changes = en.changes
	gen.Manifest.FinishedAt = time.Now().UTC()
	gen.Manifest.Outcome = rep.Plan.Summary()
	saveErr = errors.Join(saveErr, gen.Save())
	if !en.stateRemoved {
		saveErr = errors.Join(saveErr, state.SaveState(statePath, st))
	}
	if !rep.Plan.Failed() {
		saveErr = errors.Join(saveErr, state.Prune(e.Roots.Stack))
	}
	return rep, en, saveErr
}

// runPhases executes the phases in order. Once one fails, the steps of later phases are reported
// as skipped and do not run.
func runPhases(ctx context.Context, phases []phase, opts plan.Options) plan.Report {
	var all plan.Report
	failed := false
	for _, ph := range phases {
		if failed {
			for _, s := range ph.steps {
				o := plan.Outcome{ID: s.ID, Title: s.Title, Status: plan.Skipped, Detail: "not started: an earlier step failed"}
				all.Outcomes = append(all.Outcomes, o)
				if opts.After != nil {
					opts.After(o)
				}
			}
			continue
		}
		o := opts
		o.KeepGoing = ph.keepGoing
		r := plan.Execute(ctx, ph.steps, o)
		all.Outcomes = append(all.Outcomes, r.Outcomes...)
		failed = r.Failed()
	}
	return all
}

func nothingToDo(r plan.Report) bool {
	for _, o := range r.Outcomes {
		if o.Status != plan.Already {
			return false
		}
	}
	return true
}

// updatedPins keeps the pin moves whose plugin is in updated.
func updatedPins(pins []state.PinMove, updated []string) []state.PinMove {
	var out []state.PinMove
	for _, p := range pins {
		if slices.Contains(updated, p.ID) {
			out = append(out, p)
		}
	}
	return out
}
