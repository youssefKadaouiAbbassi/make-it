package setup

import (
	"context"
	"fmt"

	"makeit/internal/execx"
	"makeit/internal/plan"
	"makeit/internal/platform"
)

// prereqSteps installs the missing required prerequisites. Missing optional ones go to
// e.skipped for the summary; a required one with no package route is a step that fails with
// its manual route. The package index is refreshed once, before the first install, because
// slim images ship without one.
func (e *env) prereqSteps() []plan.Step {
	var steps []plan.Step
	refreshed := false
	for _, s := range platform.Status(e.Platform, e.Present) {
		r := s.Prereq
		if s.Present {
			continue
		}
		if !r.Required {
			e.skipped = append(e.skipped, r)
			continue
		}
		step := plan.Step{ID: "prereq." + r.ID, Title: "Install " + r.ID, Detail: r.Why,
			Already: func(context.Context) (bool, error) { return false, nil }}
		cmd, ok := platform.InstallCommand(r, e.Platform.PM, e.prompting(), e.Root)
		if !ok {
			step.Detail = r.Manual
			step.Run = func(context.Context) (string, error) {
				return "", fmt.Errorf("%s has no package route here; install it by hand: %s", r.ID, r.Manual)
			}
			steps = append(steps, step)
			continue
		}
		var cmds []execx.Cmd
		if !refreshed {
			switch e.Platform.PM {
			case platform.APT:
				cmds = append(cmds, e.sudo("apt-get", "update"))
			case platform.APK:
				cmds = append(cmds, e.sudo("apk", "update"))
			}
			refreshed = true
		}
		cmds = append(cmds, cmd)
		step.Command = cmd.Argv
		step.Run = func(ctx context.Context) (string, error) { return "", e.runAll(ctx, cmds) }
		steps = append(steps, step)
	}
	return steps
}

// runAll runs cmds in order and stops at the first that fails, naming it and its output.
func (e *env) runAll(ctx context.Context, cmds []execx.Cmd) error {
	for _, c := range cmds {
		r, err := e.Run(ctx, c)
		if err != nil {
			return fmt.Errorf("%s: %w", execx.Quote(c.Argv), err)
		}
		if r.Code != 0 {
			return fmt.Errorf("%s exited %d: %s", execx.Quote(c.Argv), r.Code, r.Text())
		}
	}
	return nil
}
