package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"makeit/internal/buildinfo"
	"makeit/internal/cli"
	"makeit/internal/plan"
	"makeit/internal/release"
	"makeit/internal/setup"
)

// releaseBase is where the newer-release check goes; tests point it elsewhere.
var releaseBase = "https://github.com"

func runUpdate(ctx context.Context, p cli.Parsed, args []string, stdout, stderr io.Writer) int {
	return runModifying(ctx, p, stdout, stderr, modifying{name: "update", question: "Apply this plan?", yesFirst: true,
		run: func(ctx context.Context, e setup.Env, mode plan.Mode, confirm func([]plan.Outcome) (bool, error)) (setup.Report, error) {
			return setup.Update(ctx, e, mode, confirm, args)
		},
		notice: func(ctx context.Context) string {
			return release.Notice(ctx, http.DefaultClient, releaseBase, buildinfo.Repo, buildinfo.Version)
		},
		after: func(w io.Writer, rep setup.Report) {
			if loads(rep.Plan.Outcomes) {
				fmt.Fprintln(w, "restart Claude Code or run /reload-plugins to load the changes")
			}
		}})
}

// loads reports whether a step that changes what Claude Code loads was done.
func loads(outs []plan.Outcome) bool {
	for _, o := range outs {
		if o.Status == plan.Done && (o.ID == "catalog" || o.ID == "settings" || o.ID == "bundle" || o.ID == "bundle.deps" ||
			strings.HasPrefix(o.ID, "plugin") || strings.HasPrefix(o.ID, "marketplace")) {
			return true
		}
	}
	return false
}

func runUninstall(ctx context.Context, p cli.Parsed, args []string, stdout, stderr io.Writer) int {
	opts := setup.UninstallOptions{KeepSettings: p.Bool["keep-settings"], Purge: p.Bool["purge"]}
	question := "Remove these?"
	if opts.Purge {
		question = "This also deletes plugin data and every backup; it cannot be undone. Remove these?"
	}
	return runModifying(ctx, p, stdout, stderr, modifying{name: "uninstall", question: question,
		run: func(ctx context.Context, e setup.Env, mode plan.Mode, confirm func([]plan.Outcome) (bool, error)) (setup.Report, error) {
			return setup.Uninstall(ctx, e, mode, confirm, args, opts)
		},
		after: func(w io.Writer, rep setup.Report) {
			if exe, err := os.Executable(); err == nil && rep.Generation > 0 && !rep.Plan.Failed() {
				fmt.Fprintf(w, "makeit itself is still at %s: delete it, or remove it the way you installed it\n", exe)
			}
		}})
}
