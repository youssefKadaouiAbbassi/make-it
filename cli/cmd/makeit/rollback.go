package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"makeit/internal/claudecli"
	"makeit/internal/cli"
	"makeit/internal/plan"
	"makeit/internal/selfupdate"
	"makeit/internal/setup"
	"makeit/internal/state"
	"makeit/internal/ui"
)

// rollbackFloor is the oldest makeit a rollback brings back: older ones have no update --auto.
const rollbackFloor = "0.2.0"

// runRollback undoes the last update that changed something: its files and plugin releases, and
// makeit itself when that same update installed it.
func runRollback(ctx context.Context, p cli.Parsed, stdout, stderr io.Writer) int {
	se, err := selfEnv(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", prog, err)
		return 1
	}
	g, gerr := setup.LastUndoable(se.Roots.Stack)
	if gerr != nil && !errors.Is(gerr, setup.ErrNothingToRollBack) {
		fmt.Fprintf(stderr, "%s: %v\n", prog, gerr)
		return 1
	}
	if binaryIsLast(se, g) {
		return rollbackBinary(ctx, se, p, stdout, stderr, false)
	}
	if gerr == nil {
		question := fmt.Sprintf("Undo the %s of %s?", g.Manifest.Command, g.Manifest.StartedAt.Local().Format("Jan 2 15:04"))
		code := runModifying(ctx, p, stdout, stderr, modifying{name: "rollback", question: question, yesFirst: true,
			run: func(ctx context.Context, e setup.Env, mode plan.Mode, confirm func([]plan.Outcome) (bool, error)) (setup.Report, error) {
				return setup.Rollback(ctx, e, mode, confirm, []string{"rollback"})
			}})
		// Declined or stopped, the run is not marked rolled back: makeit itself stays too.
		if code != 0 || p.Bool["dry-run"] || !rolledBack(se.Roots.Stack, g.Manifest.Generation) || !binaryMovedIn(se, g.Manifest) {
			return code
		}
	}
	return rollbackBinary(ctx, se, p, stdout, stderr, gerr == nil)
}

// binaryMovedIn reports whether the update in m installed the makeit running now: its swap is
// recorded just before the run started, and the version it brought is the one that ran.
// rolledBack reports whether generation n is now marked undone.
func rolledBack(stack string, n int) bool {
	gens, err := state.LoadGenerations(stack)
	if err != nil {
		return false
	}
	for _, g := range gens {
		if g.Manifest.Generation == n {
			return g.Manifest.RolledBack
		}
	}
	return false
}

// binaryIsLast reports that the last change was makeit itself, replaced after the newest run that
// wrote files (an update with nothing else to do writes no generation). After a rollback of the
// binary its receipt skips the version it left, so a second rollback goes on to the files.
func binaryIsLast(e selfupdate.Env, g *state.Generation) bool {
	rec, err := e.Gate()
	if err != nil || rec.Previous == nil || rec.InstalledAt.IsZero() || rec.Skipped == *rec.Previous {
		return false
	}
	return g == nil || rec.InstalledAt.After(g.Manifest.StartedAt.Add(time.Minute))
}

func binaryMovedIn(e selfupdate.Env, m state.Manifest) bool {
	rec, err := e.Gate()
	if err != nil || rec.Previous == nil || rec.Version != m.CLIVersion {
		return false
	}
	return !rec.InstalledAt.Before(m.StartedAt.Add(-15*time.Minute)) && !rec.InstalledAt.After(m.StartedAt.Add(time.Minute))
}

// rollbackBinary brings back the previous makeit, never one older than rollbackFloor. consented is
// true when the rollback question was already answered.
func rollbackBinary(ctx context.Context, e selfupdate.Env, p cli.Parsed, stdout, stderr io.Writer, consented bool) int {
	asJSON := p.Bool["json"]
	interactive := isTTY(os.Stdin) && isTTY(stderr) && !p.Bool["no-input"] && !asJSON
	mode := plan.Decide(interactive, p.Bool["yes"], p.Bool["dry-run"])
	if consented {
		mode = plan.Apply
	}
	if prev, err := e.Previous(); err == nil {
		if n, cerr := claudecli.Compare(prev, rollbackFloor); cerr == nil && n < 0 {
			fmt.Fprintf(stderr, "makeit stays at %s: %s is older than %s, which has no daily update\n", e.Version, prev, rollbackFloor)
			return 0
		}
	}
	u := ui.New(os.Stdin, stderr, interactive, ui.ColorWanted(p.String["color"], isTTY(stderr)))
	rep := selfRollback(ctx, e, mode, u)
	rep.Command = "rollback"
	if asJSON {
		return emit(stdout, stderr, rep, rep.code)
	}
	for _, l := range rep.lines {
		fmt.Fprintln(stderr, l)
	}
	if rep.Error != "" {
		fmt.Fprintf(stderr, "%s: %s\n", prog, rep.Error)
	}
	return rep.code
}
