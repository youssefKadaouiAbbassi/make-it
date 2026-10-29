package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"makeit/internal/buildinfo"
	"makeit/internal/catalog"
	"makeit/internal/claudecli"
	"makeit/internal/cli"
	"makeit/internal/execx"
	"makeit/internal/paths"
	"makeit/internal/plan"
	"makeit/internal/platform"
	"makeit/internal/setup"
	"makeit/internal/ui"
)

// setupEnv reads this machine for setup; tests replace it.
var setupEnv = func(context.Context) (setup.Env, error) {
	roots, err := paths.Current()
	if err != nil {
		return setup.Env{}, err
	}
	present := func(name string) bool { _, ok := execx.LookPath(name); return ok }
	return setup.Env{
		Roots:    roots,
		Platform: platform.Detect(runtime.GOOS, runtime.GOARCH, present, os.Getenv),
		Present:  present,
		Run:      execx.Run,
		Catalog:  catalog.Embedded,
		Locate: func(ctx context.Context) []claudecli.Install {
			return claudecli.Locate(claudecli.MachineProbe(ctx, roots.Home))
		},
		// Geteuid is -1 on Windows, where there is no sudo.
		Root:   os.Geteuid() == 0,
		Getenv: os.Getenv,
	}, nil
}

func runSetup(ctx context.Context, p cli.Parsed, args []string, stdout, stderr io.Writer) int {
	e, err := setupEnv(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", prog, err)
		return 1
	}
	asJSON := p.Bool["json"]
	e.Interactive = isTTY(os.Stdin) && isTTY(stderr) && !p.Bool["no-input"] && !asJSON
	e.Yes = p.Bool["yes"]
	mode := plan.Decide(e.Interactive, e.Yes, p.Bool["dry-run"])
	u := ui.New(os.Stdin, stderr, e.Interactive, ui.ColorWanted(p.String["color"], isTTY(stderr)))
	u.Quiet, u.Verbose = p.Bool["quiet"], p.Bool["verbose"]
	shown := false
	confirm := func(outs []plan.Outcome) (bool, error) {
		printOutcomes(u, outs)
		shown = true
		return u.Confirm("Apply this plan?", true)
	}
	rep, err := setup.Run(ctx, e, mode, confirm, args)
	if ctx.Err() != nil {
		fmt.Fprintf(stderr, "%s: setup interrupted\n", prog)
		return 1
	}
	code := 0
	switch {
	case errors.Is(err, setup.ErrConsent):
		code = 2
	case errors.Is(err, setup.ErrDeclined):
	case err != nil:
		code = 1
	case rep.Plan.Failed():
		code = 1
	}
	if asJSON {
		out := struct {
			Version    string          `json:"version"`
			Plan       []plan.Outcome  `json:"plan"`
			Skipped    []setup.Skipped `json:"skipped"`
			Generation int             `json:"generation"`
			Summary    string          `json:"summary"`
			Error      string          `json:"error,omitempty"`
		}{buildinfo.Version, rep.Plan.Outcomes, rep.Skipped, rep.Generation, rep.Plan.Summary(), errText(err)}
		return emit(stdout, stderr, out, code)
	}
	if !shown || rep.Generation > 0 {
		printOutcomes(u, rep.Plan.Outcomes)
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", prog, err)
	}
	fmt.Fprintln(stderr, rep.Plan.Summary())
	for _, s := range rep.Skipped {
		fmt.Fprintf(stderr, "install by hand: %s (%s)\n", s.ID, s.Manual)
	}
	return code
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// printOutcomes shows a plan or what a run did: one line per step, the commands and diffs of
// steps that would change under it.
func printOutcomes(u *ui.UI, outs []plan.Outcome) {
	for _, o := range outs {
		switch o.Status {
		case plan.Done:
			u.Ok("%s", o.Title)
		case plan.Already:
			u.Info("already: %s", o.Title)
		case plan.WouldChange:
			u.Info("will: %s", o.Title)
		case plan.Failed:
			u.Fail("%s: %s", o.Title, o.Detail)
			continue
		default:
			u.Warn("%s: %s", o.Title, o.Status)
		}
		if o.Status == plan.WouldChange {
			if len(o.Command) > 0 {
				u.Plain("    $ " + execx.Quote(o.Command) + "\n")
			}
			if o.Detail != "" {
				u.Plain(u.Dim("    "+strings.ReplaceAll(o.Detail, "\n", "\n    ")) + "\n")
			}
		}
	}
}
