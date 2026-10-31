package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"makeit/internal/buildinfo"
	"makeit/internal/catalog"
	"makeit/internal/cli"
	"makeit/internal/docs"
	"makeit/internal/execx"
	"makeit/internal/initrepo"
	"makeit/internal/paths"
	"makeit/internal/plan"
	"makeit/internal/setup"
	"makeit/internal/state"
	"makeit/internal/ui"
)

// initRun runs git and gh for init; tests stand in for it.
var initRun execx.Runner = execx.Run

// runInit is `makeit init [dir]` (cli/INIT.md).
func runInit(ctx context.Context, p cli.Parsed, stdout, stderr io.Writer) int {
	if len(p.Positionals) > 1 {
		fmt.Fprintf(stderr, "%s: init takes at most one directory\n", prog)
		return 2
	}
	dir := "."
	if len(p.Positionals) == 1 {
		dir = p.Positionals[0]
	}
	roots, err := paths.Current()
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", prog, err)
		return 1
	}
	asJSON := p.Bool["json"]
	interactive := isTTY(os.Stdin) && isTTY(stderr) && !p.Bool["no-input"] && !asJSON
	mode := plan.Decide(interactive, p.Bool["yes"], p.Bool["dry-run"])
	u := ui.New(os.Stdin, stderr, interactive, ui.ColorWanted(p.String["color"], isTTY(stderr)))
	u.Quiet, u.Verbose = p.Bool["quiet"], p.Bool["verbose"]

	e, err := initEnv(roots.Stack, dir)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", prog, err)
		return 1
	}
	shown := false
	confirm := func(outs []plan.Outcome) (bool, error) {
		printOutcomes(u, outs)
		shown = true
		return u.Confirm("Apply this plan?", true)
	}
	rep, err := initrepo.Run(ctx, e, mode, confirm, initrepo.Options{Update: p.Bool["update"], Rulesets: p.Bool["rulesets"], Offline: p.Bool["offline"]})
	if ctx.Err() != nil {
		fmt.Fprintf(stderr, "%s: init interrupted\n", prog)
		return 1
	}
	code := 0
	switch {
	case errors.Is(err, setup.ErrConsent):
		code = 2
	case errors.Is(err, setup.ErrDeclined):
	case err != nil, rep.Plan.Failed():
		code = 1
	}
	if asJSON {
		out := struct {
			Version string         `json:"version"`
			Plan    []plan.Outcome `json:"plan"`
			Kept    []setup.Kept   `json:"kept"`
			Manual  []string       `json:"manual"`
			Notices []string       `json:"notices"`
			Docs    []string       `json:"docs"`
			Summary string         `json:"summary"`
			Error   string         `json:"error,omitempty"`
		}{buildinfo.Version, rep.Plan.Outcomes, orEmpty(rep.Kept), orEmpty(rep.Manual), orEmpty(rep.Notices), orEmpty(rep.Docs), rep.Plan.Summary(), errText(err)}
		return emit(stdout, stderr, out, code)
	}
	if !shown || mode == plan.Apply {
		printOutcomes(u, rep.Plan.Outcomes)
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", prog, err)
	}
	fmt.Fprintln(stderr, rep.Plan.Summary())
	for _, k := range rep.Kept {
		fmt.Fprintf(stderr, "kept: %s: %s\n", k.Item, k.Reason)
	}
	for _, l := range rep.Docs {
		fmt.Fprintf(stderr, "docs: %s\n", l)
	}
	for _, n := range rep.Notices {
		fmt.Fprintln(stderr, n)
	}
	// The record keeps the manual steps' hash, so new ones make the run change something: a re-run
	// with nothing to do points at them instead of printing them again.
	changed := slices.ContainsFunc(rep.Plan.Outcomes, func(o plan.Outcome) bool { return o.Status == plan.Done })
	switch {
	case len(rep.Manual) == 0:
	case p.Bool["dry-run"] || changed:
		for _, m := range rep.Manual {
			fmt.Fprintf(stderr, "for /makeit:init to apply: %s\n", m)
		}
	case len(rep.Manual) == 1:
		fmt.Fprintln(stderr, "1 manual step still open: makeit init --dry-run shows it")
	default:
		fmt.Fprintf(stderr, "%d manual steps still open: makeit init --dry-run shows them\n", len(rep.Manual))
	}
	return code
}

// initEnv seeds from the catalog setup synced when there is one (a --catalog source included),
// else from the catalog in this binary.
func initEnv(stack, dir string) (initrepo.Env, error) {
	var src catalog.Source = catalog.Embedded
	kind, resolved := "embedded", ""
	if m, err := catalog.EmbeddedManifest(); err == nil {
		resolved = catalog.Digest(m)
	}
	if synced := filepath.Join(stack, "catalog"); catalog.ReadMarker(synced).Digest != "" {
		src = catalog.Dir(synced)
		if st, _, err := state.LoadState(filepath.Join(stack, "state.json")); err == nil && st.Catalog.Kind != "" {
			kind, resolved = st.Catalog.Kind, st.Catalog.Resolved
		}
	}
	req, err := catalog.LoadRequires(src)
	if err != nil {
		return initrepo.Env{}, err
	}
	o, err := docsOptions(stack)
	if err != nil {
		// No docs server is known: every library is listed as not indexed, and init goes on.
		o = docs.Options{Run: func(context.Context, execx.Cmd) (execx.Result, error) { return execx.Result{}, err }}
	}
	return initrepo.Env{Dir: dir, Stack: stack, Catalog: src, Req: req, Run: initRun, Docs: o,
		Present: func(name string) bool { _, ok := execx.LookPath(name); return ok },
		Now:     time.Now, CatalogKind: kind, CatalogResolved: resolved}, nil
}

// orEmpty keeps an empty list as [] in the JSON report.
func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
