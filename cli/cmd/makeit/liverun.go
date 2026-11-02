package main

import (
	"context"
	"slices"
	"strings"

	"makeit/internal/execx"
)

// holder is the live view as liveRun sees it: Hold clears it off the terminal until the returned
// func is called.
type holder interface{ Hold() func() }

// liveRun wraps a runner for the live view. A command that asked for the terminal (Inherit) only
// needs it for sudo's password, since every install runs without questions: sudo -v asks first
// with the view held (instant when sudo remembers the password), then the command runs captured,
// so a package manager's output never floods the screen and a failure keeps its output. live
// returns nil while no live view runs, and then commands run exactly as asked.
func liveRun(run execx.Runner, live func() holder) execx.Runner {
	return func(ctx context.Context, c execx.Cmd) (execx.Result, error) {
		h := live()
		if !c.Inherit || h == nil {
			return run(ctx, c)
		}
		if slices.Equal(c.Argv, []string{"sudo", "-v"}) {
			defer h.Hold()()
			return run(ctx, c)
		}
		if usesSudo(c.Argv) {
			release := h.Hold()
			_, _ = run(ctx, execx.Cmd{Argv: []string{"sudo", "-v"}, Inherit: true})
			release()
		}
		c.Inherit = false
		return run(ctx, c)
	}
}

// usesSudo reports a command that runs sudo: sudo itself, or a shell script that calls it.
func usesSudo(argv []string) bool {
	if len(argv) > 0 && argv[0] == "sudo" {
		return true
	}
	return len(argv) == 3 && argv[0] == "sh" && argv[1] == "-c" && strings.Contains(argv[2], "sudo")
}
