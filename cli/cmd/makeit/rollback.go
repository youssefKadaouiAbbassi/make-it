package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"makeit/internal/cli"
	"makeit/internal/plan"
	"makeit/internal/ui"
)

// runRollback undoes the last update that changed something.
func runRollback(ctx context.Context, p cli.Parsed, stdout, stderr io.Writer) int {
	e, err := selfEnv(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", prog, err)
		return 1
	}
	asJSON := p.Bool["json"]
	interactive := isTTY(os.Stdin) && isTTY(stderr) && !p.Bool["no-input"] && !asJSON
	mode := plan.Decide(interactive, p.Bool["yes"], p.Bool["dry-run"])
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
