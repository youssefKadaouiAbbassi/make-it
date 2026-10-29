package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"

	"makeit/internal/buildinfo"
	"makeit/internal/cli"
	"makeit/internal/plan"
	"makeit/internal/release"
	"makeit/internal/selfupdate"
	"makeit/internal/setup"
	"makeit/internal/ui"
)

// stackUpdate is the stack's update run; tests replace it to reach states the fakes cannot.
var stackUpdate = setup.Update

// releaseBase is where the newer-release check goes; tests point it elsewhere.
var releaseBase = "https://github.com"

func runUpdate(ctx context.Context, p cli.Parsed, args []string, stdout, stderr io.Writer) int {
	var moved *selfMove
	notice, needConsent := "", false
	if from := os.Getenv("MAKEIT_REEXEC"); from != "" {
		// This is the new makeit, run by the one it replaced: no second check for a release.
		moved = &selfMove{From: from, To: buildinfo.Version}
	} else {
		o := selfStep(ctx, p, args, stdout, stderr)
		if o.exited {
			return o.code
		}
		notice, needConsent = o.notice, o.needConsent
	}
	code := runModifying(ctx, p, stdout, stderr, modifying{name: "update", question: "Apply this plan?", yesFirst: true, self: moved, prepare: prepareCatalog("update"),
		run: func(ctx context.Context, e setup.Env, mode plan.Mode, confirm func([]plan.Outcome) (bool, error)) (setup.Report, error) {
			return stackUpdate(ctx, e, mode, confirm, args)
		},
		notice: func(context.Context) string { return notice },
		after: func(w io.Writer, rep setup.Report) {
			if rep.Reload {
				fmt.Fprintln(w, "restart Claude Code or run /reload-plugins to load the changes")
			}
		}})
	// A newer makeit that waits for consent needs it even when the stack has nothing to do.
	if code == 0 && needConsent {
		if !p.Bool["json"] {
			fmt.Fprintln(stderr, plan.ConsentMessage)
		}
		return 2
	}
	return code
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

// selfMove is the swap that update's parent made before running this binary.
type selfMove struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// selfOutcome is what update's self step decided before the stack run.
type selfOutcome struct {
	// notice is the line for the run's notice.
	notice string
	// exited: the new binary ran the update, and code is its exit code.
	code   int
	exited bool
	// needConsent: the swap waits for --yes or a terminal.
	needConsent bool
}

// selfStep is update's first step (cli/SELF-UPDATE.md, update). With a receipt for this binary and
// a newer release, it swaps the release in and runs the new binary with the same arguments. Every
// other case ends in a notice line, and the update goes on with this binary.
func selfStep(ctx context.Context, p cli.Parsed, args []string, stdout, stderr io.Writer) selfOutcome {
	if p.Bool["offline"] {
		return selfOutcome{}
	}
	e, err := selfEnv(ctx)
	if err != nil {
		return selfOutcome{notice: "could not check for a newer makeit: " + err.Error()}
	}
	rec, err := e.Gate()
	if err != nil {
		// Not this binary's to update: today's notice, silent in a development build.
		notice := release.Notice(ctx, e.Client, e.Base, e.Repo, e.Version)
		if errors.Is(err, selfupdate.ErrOtherPath) {
			notice = strings.TrimPrefix(notice+"\n"+err.Error(), "\n")
		}
		return selfOutcome{notice: notice}
	}
	pick, err := e.Pick(ctx, rec, "", false)
	switch {
	case err != nil:
		return selfOutcome{notice: "could not check for a newer makeit: " + err.Error()}
	case pick.Skipped:
		return selfOutcome{notice: fmt.Sprintf("makeit %s is skipped (you rolled back from it): `makeit self update --to %s` takes it", pick.Latest, pick.Latest)}
	case pick.To == "":
		return selfOutcome{}
	}
	asJSON := p.Bool["json"]
	interactive := isTTY(os.Stdin) && isTTY(stderr) && !p.Bool["no-input"] && !asJSON
	u := ui.New(os.Stdin, stderr, interactive, ui.ColorWanted(p.String["color"], isTTY(stderr)))
	stop, need, err := consent(plan.Decide(interactive, p.Bool["yes"], p.Bool["dry-run"]), u,
		fmt.Sprintf("Update makeit %s → %s first?", e.Version, pick.To))
	switch {
	case err != nil:
		return selfOutcome{notice: fmt.Sprintf("could not update makeit to %s: %v; continuing with %s", pick.To, err, e.Version)}
	case stop == "would":
		return selfOutcome{notice: fmt.Sprintf("would update makeit %s → %s first", e.Version, pick.To), needConsent: need}
	case stop == "declined":
		return selfOutcome{notice: fmt.Sprintf("makeit %s is available (this is %s)", pick.To, e.Version)}
	}
	res, err := e.Update(ctx, pick.To)
	if ctx.Err() != nil {
		// Interrupted: neither the new binary nor this one goes on with the update.
		if err == nil {
			fmt.Fprintf(stderr, "%s: makeit %s is in place, but the update was interrupted before it ran\n", prog, res.To)
		}
		return selfOutcome{code: 1, exited: true}
	}
	if err != nil {
		return selfOutcome{notice: fmt.Sprintf("could not update makeit to %s: %v; continuing with %s", pick.To, err, e.Version)}
	}
	return selfOutcome{code: reexec(res.BinPath, args, res.From, stdout, stderr), exited: true}
}

// child is the new makeit that update runs after a swap, while it runs; main's signal loop reads it.
var child atomic.Pointer[os.Process]

// reexec runs the new binary with the same arguments and MAKEIT_REEXEC=from, the terminal passed
// through, and returns its exit code. It has no context: an interrupt reaches the child from the
// terminal, and the parent waits for it.
func reexec(bin string, args []string, from string, stdout, stderr io.Writer) int {
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "MAKEIT_REEXEC="+from)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, stdout, stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(stderr, "%s: makeit was updated, but the new binary did not start: %v\n", prog, err)
		return 1
	}
	child.Store(cmd.Process)
	defer child.Store(nil)
	err := cmd.Wait()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ee) && ee.ExitCode() >= 0:
		return ee.ExitCode()
	default:
		fmt.Fprintf(stderr, "%s: the new makeit: %v\n", prog, err)
		return 1
	}
}
