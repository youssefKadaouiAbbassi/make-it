package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"makeit/internal/buildinfo"
	"makeit/internal/catalog"
	"makeit/internal/catsrc"
	"makeit/internal/claudecli"
	"makeit/internal/cli"
	"makeit/internal/execx"
	"makeit/internal/latest"
	"makeit/internal/paths"
	"makeit/internal/plan"
	"makeit/internal/platform"
	"makeit/internal/setup"
	"makeit/internal/state"
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
	return runModifying(ctx, p, stdout, stderr, modifying{name: "setup", question: "Apply this plan?", yesFirst: true, prepare: prepareCatalog("setup", stderr),
		run: func(ctx context.Context, e setup.Env, mode plan.Mode, confirm func([]plan.Outcome) (bool, error)) (setup.Report, error) {
			return setup.Run(ctx, e, mode, confirm, args)
		}})
}

// modifying is what differs between setup, update and uninstall.
type modifying struct {
	name     string
	question string
	// yesFirst is the prompt's default answer.
	yesFirst bool
	run      func(ctx context.Context, e setup.Env, mode plan.Mode, confirm func([]plan.Outcome) (bool, error)) (setup.Report, error)
	// notice is update's newer-release line, asked before the run; nil for the others.
	notice func(ctx context.Context) string
	// after prints the command's own closing lines, in text mode, after a run without error.
	after func(w io.Writer, rep setup.Report, live bool)
	// self is the swap update's parent made before running this binary; nil otherwise.
	self *selfMove
	// prepare picks the catalog the run plans with (setup and update); its lines join the notice.
	prepare func(ctx context.Context, p cli.Parsed, e *setup.Env) ([]string, error)
}

// catalogGit runs git for --catalog; tests allow file URLs.
var catalogGit = catsrc.Git{Run: execx.Run, Protocols: "https:ssh"}

// prepareCatalog resolves --catalog, or the recorded source, into the catalog the run plans with.
func prepareCatalog(command string, stderr io.Writer) func(ctx context.Context, p cli.Parsed, e *setup.Env) ([]string, error) {
	return func(ctx context.Context, p cli.Parsed, e *setup.Env) ([]string, error) {
		st, _, err := state.LoadState(filepath.Join(e.Roots.Stack, "state.json"))
		if err != nil {
			return nil, err
		}
		status := platform.Status(e.Platform, e.Present)
		prereq := func(id string) bool {
			for _, s := range status {
				if s.Prereq.ID == id {
					return s.Present
				}
			}
			return true // not a prerequisite here
		}
		prep, err := catsrc.Prepare(ctx, catsrc.Params{Arg: p.String["catalog"], AllowBranch: p.Bool["allow-branch"],
			Offline: p.Bool["offline"], Command: command, State: st, Stack: e.Roots.Stack, Git: catalogGit, Prereq: prereq,
			Show: func(line string) { fmt.Fprintln(stderr, line) }})
		if err != nil {
			return nil, err
		}
		e.Catalog, e.CatalogTree, e.CatalogRecord = prep.Source, prep.Tree, prep.Record
		notices := prep.Notices
		if followsReleases(prep.Record) {
			tree := prep.Tree
			if tree == nil {
				if tree, err = catalog.EmbeddedTree(); err != nil {
					return nil, err
				}
			}
			current, _ := catalog.DirTree(filepath.Join(e.Roots.Stack, "catalog"))
			// The daily run leaves releases a rollback undid; an update run by hand takes the latest.
			var skips map[string][]string
			if p.Bool["auto"] {
				skips = st.PluginSkips
			}
			// setup and update both take the newest releases; offline they fetch nothing and keep the
			// releases on disk (a nil runner) instead of writing the older pins makeit ships over them.
			var run execx.Runner
			if !p.Bool["offline"] {
				run = execx.Run
			}
			moved, pins, notes := latestApply(ctx, run, tree, current, skips)
			if moved != tree {
				e.Catalog, e.CatalogTree = moved, moved
			}
			e.Pins = pins
			notices = append(notices, notes...)
		}
		return notices, nil
	}
}

// latestApply moves plugins to their newest releases; tests replace it to stay off the network.
var latestApply = latest.Apply

// followsReleases is true for a plugin set that is not pinned: makeit's own, or a git one that
// follows its newest tag or a branch. A directory or a tag or commit pin stays exactly as given.
func followsReleases(rec state.Catalog) bool {
	switch rec.Kind {
	case "", "embedded":
		return true
	case "git":
		return rec.RefKind == "latest" || rec.RefKind == "branch"
	}
	return false
}

// decideRun is the consent rule: --yes applies unattended (and sudo runs with -n); "consented"
// (doctor's fix, after its own question) applies without asking the plan question again but keeps
// sudo able to ask for a password at the terminal, since Yes stays false.
func decideRun(p cli.Parsed, interactive bool) (yes bool, mode plan.Mode) {
	yes = p.Bool["yes"]
	return yes, plan.Decide(interactive, yes || p.Bool["consented"], p.Bool["dry-run"])
}

// runModifying is what setup, update and uninstall share: the consent rule, the plan shown before
// the prompt, the outcomes, the exit code and the JSON report.
func runModifying(ctx context.Context, p cli.Parsed, stdout, stderr io.Writer, m modifying) int {
	e, err := setupEnv(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", prog, err)
		return 1
	}
	asJSON := p.Bool["json"]
	e.Interactive = isTTY(os.Stdin) && isTTY(stderr) && !p.Bool["no-input"] && !asJSON
	var mode plan.Mode
	e.Yes, mode = decideRun(p, e.Interactive)
	e.Background = p.Bool["auto"]
	for _, id := range strings.Split(p.String["reinstall"], ",") {
		if id = strings.TrimSpace(id); id != "" {
			e.Reinstall = append(e.Reinstall, id)
		}
	}
	if err := setup.CheckReinstall(e.Reinstall); err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", prog, err)
		return 2
	}
	u := ui.New(os.Stdin, stderr, e.Interactive, ui.ColorWanted(p.String["color"], isTTY(stderr)))
	u.Quiet, u.Verbose = p.Bool["quiet"], p.Bool["verbose"]
	u.Mode = ui.DecideMode(isTTY(os.Stdout), isTTY(stderr), os.Getenv, asJSON, p.Bool["plain"] || p.Bool["verbose"])
	started := time.Now()
	// The live view starts once the run applies: after the question, or at once with --yes.
	var live *ui.LiveView
	startLive := func(outs []plan.Outcome) {
		if u.Mode == ui.Live && live == nil && !p.Bool["dry-run"] {
			live = ui.NewLive(u, outs)
		}
	}
	e.Run = liveRun(e.Run, func() holder {
		if live == nil {
			return nil // a nil *LiveView in the interface would not compare to nil
		}
		return live
	})
	e.Progress = func(ev plan.Event) {
		startLive(nil)
		if live != nil {
			live.Event(ev)
		}
	}
	u.Header(m.name)
	var catalogNotices []string
	if m.prepare != nil {
		if catalogNotices, err = m.prepare(ctx, p, &e); err != nil {
			if asJSON {
				// The normal report's shape, with nothing planned.
				return emit(stdout, stderr, struct {
					Version    string         `json:"version"`
					Plan       []plan.Outcome `json:"plan"`
					Self       *selfMove      `json:"self,omitempty"`
					Generation int            `json:"generation"`
					Summary    string         `json:"summary"`
					Error      string         `json:"error"`
				}{buildinfo.Version, []plan.Outcome{}, m.self, 0, "nothing applied", err.Error()}, 1)
			}
			fmt.Fprintf(stderr, "%s: %v\n", prog, err)
			return 1
		}
	}
	if m.self != nil && !asJSON {
		fmt.Fprintf(stderr, "makeit updated from %s to %s\n", m.self.From, m.self.To)
	}
	notice := ""
	if m.notice != nil && !p.Bool["offline"] {
		notice = m.notice(ctx)
	}
	notice = strings.TrimPrefix(strings.Join(append([]string{notice}, catalogNotices...), "\n"), "\n")
	shown := false
	confirm := func(outs []plan.Outcome) (bool, error) {
		printOutcomes(u, outs)
		shown = true
		yes, err := u.Confirm(m.question, m.yesFirst)
		if yes && err == nil {
			if u.Mode == ui.Live {
				_, _ = fmt.Fprintln(stderr)
			}
			startLive(outs)
		}
		return yes, err
	}
	rep, err := m.run(ctx, e, mode, confirm)
	if live != nil {
		live.Stop(ctx.Err() != nil)
	}
	if ctx.Err() != nil {
		fmt.Fprintf(stderr, "%s: %s interrupted\n", prog, m.name)
		return 1
	}
	code := 0
	switch {
	case errors.Is(err, setup.ErrConsent):
		code = 2
	case errors.Is(err, setup.ErrDeclined), errors.Is(err, setup.ErrNothing):
	case err != nil, rep.Plan.Failed():
		code = 1
	}
	if asJSON {
		out := struct {
			Version    string           `json:"version"`
			Plan       []plan.Outcome   `json:"plan"`
			Hints      []setup.Hint     `json:"hints,omitempty"`
			NeedsYou   []setup.NeedsYou `json:"needsYou,omitempty"`
			Kept       []setup.Kept     `json:"kept,omitempty"`
			Notice     string           `json:"notice,omitempty"`
			Self       *selfMove        `json:"self,omitempty"`
			Generation int              `json:"generation"`
			Summary    string           `json:"summary"`
			Error      string           `json:"error,omitempty"`
		}{buildinfo.Version, rep.Plan.Outcomes, rep.Hints, rep.NeedsYou, rep.Kept, notice, m.self, rep.Generation, rep.Plan.Summary(), errText(err)}
		return emit(stdout, stderr, out, code)
	}
	if live == nil && (!shown || rep.Generation > 0) {
		printOutcomes(u, rep.Plan.Outcomes)
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", prog, err)
	}
	if u.Mode == ui.Live {
		printNotesLive(u, rep)
		finishLive(u, m.name, rep, m.self, time.Since(started))
	} else {
		fmt.Fprintln(stderr, rep.Plan.Summary())
		for _, n := range rep.NeedsYou {
			fmt.Fprintf(stderr, "%s needs you: %s\n", n.ID, n.Manual)
		}
		if slices.ContainsFunc(rep.Plan.Outcomes, func(o plan.Outcome) bool { return o.ID == "path.profile" && o.Status == plan.Done }) {
			fmt.Fprintln(stderr, "open a new terminal to pick up PATH")
		}
		for _, k := range rep.Kept {
			fmt.Fprintf(stderr, "kept: %s: %s\n", k.Item, k.Reason)
		}
		for _, h := range rep.Hints {
			fmt.Fprintf(stderr, "%s: %s\n", h.ID, h.Text)
		}
	}
	if notice != "" {
		fmt.Fprintln(stderr, notice)
	}
	if m.after != nil && err == nil {
		m.after(stderr, rep, u.Mode == ui.Live)
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
	if u.Mode == ui.Live {
		printAreas(u, outs)
		return
	}
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
