package setup

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"makeit/internal/catalog"
	"makeit/internal/fsx"
	"makeit/internal/plan"
	"makeit/internal/state"
)

// ErrConsent means the plan needs consent that no one can give here (no terminal, no --yes).
var ErrConsent = errors.New(plan.ConsentMessage)

// ErrDeclined means the person at the terminal said no to the plan.
var ErrDeclined = errors.New("declined: nothing was changed")

// Report is what a setup run did, or would do.
type Report struct {
	Plan plan.Report
	// Skipped are the optional prerequisites left to install by hand, with their manual route.
	Skipped []Skipped
	// Generation is the run's generation, 0 when nothing was applied.
	Generation int
}

// Skipped is an optional prerequisite setup did not install.
type Skipped struct {
	ID, Manual string
}

// Run plans setup and, as mode allows, applies it. confirm is asked once, with the plan, in
// Prompt mode. A plan with nothing to do applies nothing and makes no generation.
func Run(ctx context.Context, e Env, mode plan.Mode, confirm func([]plan.Outcome) (bool, error), argv []string) (Report, error) {
	statePath := filepath.Join(e.Roots.Stack, "state.json")
	st, _, err := state.LoadState(statePath)
	if err != nil {
		return Report{}, err
	}
	req, err := catalog.LoadRequires(e.Catalog)
	if err != nil {
		return Report{}, err
	}
	en := &env{Env: e, st: st, req: req}
	steps := en.prereqSteps()
	steps = append(steps, en.claudeStep())
	steps = append(steps, en.stackSteps()...)
	steps = append(steps, en.settingsStep())
	steps = append(steps, en.globalSteps()...)
	rep := Report{Plan: plan.Execute(ctx, steps, plan.Options{DryRun: true})}
	for _, r := range en.skipped {
		rep.Skipped = append(rep.Skipped, Skipped{ID: r.ID, Manual: r.Manual})
	}
	if nothingToDo(rep.Plan) || rep.Plan.Failed() {
		return rep, nil
	}
	switch mode {
	case plan.DryRunOnly:
		return rep, nil
	case plan.NeedConsent:
		return rep, ErrConsent
	case plan.Prompt:
		yes, err := confirm(rep.Plan.Outcomes)
		if err != nil {
			return rep, err
		}
		if !yes {
			return rep, ErrDeclined
		}
	}

	lock, err := fsx.Acquire(filepath.Join(e.Roots.Stack, "lock"), time.Now())
	if err != nil {
		return rep, err
	}
	defer lock.Release()
	gen, err := state.NewGeneration(e.Roots.Stack, st.Generation, "setup", argv)
	if err != nil {
		return rep, err
	}
	en.gen = gen
	st.Generation = gen.Manifest.Generation
	st.Machine = state.Machine{OS: e.Platform.OS, Arch: e.Platform.Arch, WSL: e.Platform.WSL}
	if st.Steps == nil {
		st.Steps = map[string]*state.StepRecord{}
	}
	var saveErr error
	rep.Plan = plan.Execute(ctx, steps, plan.Options{After: func(o plan.Outcome) {
		r := &state.StepRecord{Status: string(o.Status), At: time.Now().UTC()}
		if o.Status == plan.Failed || o.Status == plan.Interrupted {
			r.Error = o.Detail
		}
		st.Steps[o.ID] = r
		// The journal: after every step, so an interrupted run leaves a true record.
		saveErr = errors.Join(saveErr, state.SaveState(statePath, st))
	}})
	rep.Generation = gen.Manifest.Generation
	gen.Manifest.FinishedAt = time.Now().UTC()
	gen.Manifest.Outcome = rep.Plan.Summary()
	saveErr = errors.Join(saveErr, gen.Save(), state.SaveState(statePath, st))
	if !rep.Plan.Failed() {
		saveErr = errors.Join(saveErr, state.Prune(e.Roots.Stack))
	}
	return rep, saveErr
}

func nothingToDo(r plan.Report) bool {
	for _, o := range r.Outcomes {
		if o.Status != plan.Already {
			return false
		}
	}
	return true
}
