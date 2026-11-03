package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	"makeit/internal/buildinfo"
	"makeit/internal/cli"
	"makeit/internal/execx"
	"makeit/internal/paths"
	"makeit/internal/plan"
	"makeit/internal/selfupdate"
	"makeit/internal/ui"
)

// selfEnv reads this machine for the self commands; tests replace it.
var selfEnv = defaultSelfEnv

func defaultSelfEnv(context.Context) (selfupdate.Env, error) {
	roots, err := paths.Current()
	if err != nil {
		return selfupdate.Env{}, err
	}
	exe, err := os.Executable()
	if err != nil {
		return selfupdate.Env{}, err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	base, err := selfupdate.Base(os.Getenv, releaseBase)
	if err != nil {
		return selfupdate.Env{}, err
	}
	return selfupdate.Env{Roots: roots, Exe: exe, Version: buildinfo.Version, Target: buildinfo.Target(), Repo: buildinfo.Repo,
		Keys: buildinfo.Keys(), Base: base, Client: http.DefaultClient, Run: execx.Run, Windows: runtime.GOOS == "windows",
		Getenv: os.Getenv, Now: time.Now}, nil
}

// selfReport is the one JSON object a self command prints; lines are its text-mode output.
type selfReport struct {
	Command string `json:"command"`
	From    string `json:"from,omitempty"`
	To      string `json:"to,omitempty"`
	Path    string `json:"path,omitempty"`
	// Outcome is done, already, would, declined, skipped, refused or failed.
	Outcome string `json:"outcome"`
	Error   string `json:"error,omitempty"`
	lines   []string
	code    int
}

func (r selfReport) stop(outcome string, err error) selfReport {
	r.Outcome, r.Error, r.code = outcome, err.Error(), 1
	return r
}

func (r selfReport) say(outcome string, lines ...string) selfReport {
	r.Outcome, r.lines = outcome, lines
	return r
}

// consent applies the consent rule to one question. It returns "" to go ahead, or what stops here:
// would (a dry run, or no consent and no terminal) or declined.
func consent(mode plan.Mode, u *ui.UI, question string) (stop string, needConsent bool, err error) {
	switch mode {
	case plan.DryRunOnly:
		return "would", false, nil
	case plan.NeedConsent:
		return "would", true, nil
	case plan.Prompt:
		ok, err := u.Confirm(question, true)
		if err != nil || !ok {
			return "declined", false, err
		}
	}
	return "", false, nil
}

// asked turns a consent stop into the report: the would line, exit 2 when consent was needed.
func asked(r selfReport, stop string, needConsent bool, would string) selfReport {
	if stop == "declined" {
		return r.say("declined")
	}
	r = r.say("would", would)
	if needConsent {
		r.lines, r.code = append(r.lines, plan.ConsentMessage), 2
	}
	return r
}

func runSelf(ctx context.Context, p cli.Parsed, stdout, stderr io.Writer) int {
	usage := func(msg string) int {
		fmt.Fprintf(stderr, "%s: %s\n", prog, msg)
		return 2
	}
	if len(p.Positionals) == 1 && slices.Contains([]string{"update", "rollback"}, p.Positionals[0]) {
		return usage(fmt.Sprintf(`"self %s" is now "makeit %s"`, p.Positionals[0], p.Positionals[0]))
	}
	if len(p.Positionals) != 1 || p.Positionals[0] != "install" {
		return usage("self takes install")
	}
	e, err := selfEnv(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", prog, err)
		return 1
	}
	asJSON := p.Bool["json"]
	interactive := isTTY(os.Stdin) && isTTY(stderr) && !p.Bool["no-input"] && !asJSON
	mode := plan.Decide(interactive, p.Bool["yes"], p.Bool["dry-run"])
	u := ui.New(os.Stdin, stderr, interactive, ui.ColorWanted(p.String["color"], isTTY(stderr)))
	rep := selfInstall(ctx, e, p.String["prefix"], mode, u)
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

func selfRollback(ctx context.Context, e selfupdate.Env, mode plan.Mode, u *ui.UI) selfReport {
	r := selfReport{Command: "self rollback", From: e.Version}
	prev, err := e.Previous()
	if err != nil {
		return r.stop("refused", err)
	}
	r.To = prev
	stop, need, err := consent(mode, u, fmt.Sprintf("Roll makeit back from %s to %s?", e.Version, prev))
	if err != nil {
		return r.stop("failed", err)
	}
	if stop != "" {
		return asked(r, stop, need, fmt.Sprintf("would roll makeit back from %s to %s", e.Version, prev))
	}
	res, err := e.Rollback(ctx)
	if err != nil {
		return r.stop("failed", err)
	}
	r.Path = res.BinPath
	return r.say("done", fmt.Sprintf("makeit rolled back from %s to %s; %s is skipped until a newer release or `makeit update --to %s`",
		res.From, res.To, res.From, res.From))
}

func selfInstall(ctx context.Context, e selfupdate.Env, prefix string, mode plan.Mode, u *ui.UI) selfReport {
	r := selfReport{Command: "self install", To: e.Version}
	if e.Dev() {
		return r.stop("refused", selfupdate.ErrDev)
	}
	target, err := filepath.Abs(e.InstallTarget(prefix))
	if err != nil {
		return r.stop("failed", err)
	}
	r.Path = target
	stop, need, err := consent(mode, u, fmt.Sprintf("Install makeit %s to %s?", e.Version, target))
	if err != nil {
		return r.stop("failed", err)
	}
	if stop != "" {
		return asked(r, stop, need, fmt.Sprintf("would install makeit %s to %s", e.Version, target))
	}
	res, err := e.Install(ctx, prefix)
	switch {
	case errors.Is(err, selfupdate.ErrForeign), errors.Is(err, selfupdate.ErrDev):
		return r.stop("refused", err)
	case err != nil:
		return r.stop("failed", err)
	}
	r.From = res.Previous
	lines := []string{fmt.Sprintf("makeit %s installed at %s", e.Version, res.Path)}
	if dir := filepath.Dir(res.Path); !res.OnPath {
		lines = append(lines, fmt.Sprintf("add %s to PATH, for example: export PATH=\"%s:$PATH\"", dir, dir))
	}
	if res.OldPath != "" {
		lines = append(lines, fmt.Sprintf("the makeit at %s stays; delete it by hand", res.OldPath))
	}
	return r.say("done", lines...)
}
