// Command makeit installs and keeps up to date the Claude Code build stack on this machine:
// setup, update and uninstall change it (cli/SETUP.md, cli/UPDATE-UNINSTALL.md); version, help
// and doctor change nothing (cli/READ-ONLY-COMMANDS.md).
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"slices"
	"syscall"

	"makeit/internal/buildinfo"
	"makeit/internal/catalog"
	"makeit/internal/cli"
	"makeit/internal/doctor"
	"makeit/internal/minisign"
	"makeit/internal/state"
	"makeit/internal/ui"
)

const prog = "makeit"

// catalogFlags choose where the catalog comes from (cli/CATALOG-GIT.md).
var catalogFlags = []cli.Flag{
	{Name: "catalog", Value: "src", Help: "where the plugin set comes from: embedded (the one in this makeit), a directory, owner/repo or a git URL, with #<tag or branch> or @<commit>; kept for later runs"},
	{Name: "allow-branch", Help: "let --catalog follow a #<branch>: each update takes its newest commit"},
}

// commands lists only what this build has, so usage never names a command that does not exist.
var commands = []cli.Command{
	{Name: "docs", Hidden: true, Args: "sync [dir]", Summary: "index the docs of a project's dependencies for the docs MCP server", Flags: []cli.Flag{
		{Name: "if-changed", Help: "only when a manifest or lockfile changed since the last sync (the hooks use it)"},
	}},
	{Name: "doctor", Summary: "check this machine against what makeit sets up; changes nothing"},
	{Name: "help", Args: "[command|exit-codes|environment]", Summary: "show help for a command or a topic"},
	{Name: "init", Args: "[dir]", Summary: "set up a project: CLAUDE.md and its docs, check tasks, hooks, settings, CI and its docs index", Flags: []cli.Flag{
		{Name: "update", Help: "also update files init wrote earlier that nobody changed since"},
		{Name: "rulesets", Help: "also send makeit's GitHub rulesets to the repository (needs gh and a GitHub remote)"},
	}},
	{Name: "rollback", Summary: "undo the last update that changed something", Modifies: true},
	// self install is what install.sh and install.ps1 run; update and rollback replaced the rest.
	{Name: "self", Hidden: true, Args: "install", Summary: "install this makeit binary", Flags: []cli.Flag{
		{Name: "prefix", Value: "dir", Help: "where to put makeit (default ~/.local/bin)"},
	}},
	{Name: "setup", Summary: "set up this machine: prerequisites, Claude Code, the plugin set, settings and global files", Flags: append([]cli.Flag{
		{Name: "reinstall", Value: "tool", Help: "install this tool again although you removed it (comma-separated for several)"},
	}, catalogFlags...)},
	{Name: "uninstall", Summary: "remove what makeit put on this machine and nobody changed since", Flags: []cli.Flag{
		{Name: "keep-settings", Help: "leave settings.json as it is"},
		{Name: "purge", Help: "also delete the data of the plugins makeit installed, and the makeit directory with every backup; cannot be undone"},
	}},
	{Name: "update", Summary: "update everything: makeit itself, the plugins, settings and global files (also runs daily on its own)", Flags: append([]cli.Flag{
		{Name: "to", Value: "version", Help: "move makeit to this version instead of the latest; clears a skip"},
		{Name: "allow-downgrade", Help: "allow --to an older version"},
		{Name: "auto", Help: "the daily run Claude Code starts: at most once a day, no questions, output to ~/.makeit/update.log"},
	}, catalogFlags...)},
	{Name: "version", Summary: "print the version"},
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	done := make(chan int, 1)
	go func() { done <- run(ctx, os.Args[1:], os.Stdout, os.Stderr, gather) }()
	os.Exit(waitExit(done, sigs, cancel, child.Load))
}

// waitExit waits for the run and returns its exit code, or the signal's code after an interrupt.
// While update's new binary runs as a child, an interrupt is the child's to handle (the terminal
// sends it to both), a SIGTERM is passed on to it, and the child's code is the result.
func waitExit(done <-chan int, sigs <-chan os.Signal, cancel func(), child func() *os.Process) int {
	for {
		select {
		case code := <-done:
			return code
		case sig := <-sigs:
			if p := child(); p != nil {
				if sig == syscall.SIGTERM {
					_ = p.Signal(sig)
				}
				continue
			}
			// Cancelling kills the running claude child (not processes it started); a second signal
			// does not wait for it.
			cancel()
			select {
			case <-done:
			case <-sigs:
			}
			return signalCode(sig)
		}
	}
}

func gather(ctx context.Context) (doctor.Machine, error) {
	digest, err := catalogDigest()
	if err != nil {
		return doctor.Machine{}, err
	}
	return doctor.Gather(ctx, buildinfo.Version, buildinfo.Target(), digest)
}

// signalCode is the exit code for a run ended by sig: 128 plus the signal number.
func signalCode(sig os.Signal) int {
	if sig == syscall.SIGTERM {
		return 143
	}
	return 130
}

func catalogDigest() (string, error) {
	m, err := catalog.EmbeddedManifest()
	if err != nil {
		return "", fmt.Errorf("the embedded plugin set: %w", err)
	}
	return catalog.Digest(m), nil
}

func names() []string {
	var out []string
	for _, c := range commands {
		out = append(out, c.Name)
	}
	return out
}

// run is the whole program; main adds the process and signal plumbing.
func run(ctx context.Context, args []string, stdout, stderr io.Writer, gather func(context.Context) (doctor.Machine, error)) int {
	if len(args) == 0 {
		return runHome(ctx, stdout, stderr, gather)
	}
	name, rest := args[0], args[1:]
	if name == "-h" || name == "--help" {
		name = "help"
	}
	i := slices.IndexFunc(commands, func(c cli.Command) bool { return c.Name == name })
	if i < 0 {
		msg := fmt.Sprintf("unknown command '%s'", name)
		if guess := cli.Suggest(name, names()); guess != "" {
			msg += fmt.Sprintf("; did you mean '%s'?", guess)
		}
		fmt.Fprintf(stderr, "%s: %s\nRun '%s help' for the commands.\n", prog, msg, prog)
		return 2
	}
	cmd := commands[i]
	p, err := cli.Parse(cmd, rest)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", prog, err)
		return 2
	}
	if p.Bool["help"] && cmd.Name != "help" {
		fmt.Fprint(stdout, cli.CommandUsage(prog, cmd))
		return 0
	}
	if c := p.String["color"]; c != "" && c != "auto" && c != "always" && c != "never" {
		fmt.Fprintf(stderr, "%s: --color takes auto, always or never, not %q\n", prog, c)
		return 2
	}
	switch cmd.Name {
	case "help":
		return help(p.Positionals, stdout, stderr)
	case "setup", "update", "uninstall":
		if len(p.Positionals) > 0 {
			fmt.Fprintf(stderr, "%s: %s takes no arguments\n", prog, cmd.Name)
			return 2
		}
		switch cmd.Name {
		case "update":
			return runUpdate(ctx, p, args, stdout, stderr)
		case "uninstall":
			return runUninstall(ctx, p, args, stdout, stderr)
		}
		return runSetup(ctx, p, args, stdout, stderr)
	case "self":
		return runSelf(ctx, p, stdout, stderr)
	case "rollback":
		if len(p.Positionals) > 0 {
			fmt.Fprintf(stderr, "%s: rollback takes no arguments\n", prog)
			return 2
		}
		return runRollback(ctx, p, stdout, stderr)
	case "docs":
		return runDocs(ctx, p, stdout, stderr)
	case "init":
		return runInit(ctx, p, stdout, stderr)
	case "version":
		if len(p.Positionals) > 0 {
			fmt.Fprintf(stderr, "%s: version takes no arguments\n", prog)
			return 2
		}
		return version(ctx, p.Bool["json"], stdout, stderr)
	default:
		if len(p.Positionals) > 0 {
			fmt.Fprintf(stderr, "%s: doctor takes no arguments yet; repository checks come with init\n", prog)
			return 2
		}
		return runDoctor(ctx, p, stdout, stderr, gather)
	}
}

func help(args []string, stdout, stderr io.Writer) int {
	switch len(args) {
	case 0:
		fmt.Fprint(stdout, cli.Usage(prog, buildinfo.Version, commands))
		return 0
	case 1:
	default:
		fmt.Fprintf(stderr, "%s: help takes one command or topic\n", prog)
		return 2
	}
	if text, ok := cli.Topic(prog, args[0]); ok {
		fmt.Fprint(stdout, text)
		return 0
	}
	if i := slices.IndexFunc(commands, func(c cli.Command) bool { return c.Name == args[0] }); i >= 0 {
		fmt.Fprint(stdout, cli.CommandUsage(prog, commands[i]))
		return 0
	}
	msg := fmt.Sprintf("no help for '%s'", args[0])
	if guess := cli.Suggest(args[0], append(names(), "exit-codes", "environment")); guess != "" {
		msg += fmt.Sprintf("; did you mean '%s'?", guess)
	}
	fmt.Fprintf(stderr, "%s: %s\n", prog, msg)
	return 2
}

func version(ctx context.Context, asJSON bool, stdout, stderr io.Writer) int {
	// A rollback's skip, from the receipt of this copy only; a receipt that cannot be read shows none.
	skipped := ""
	if e, err := selfEnv(ctx); err == nil {
		if s := e.Status(); s.Ours {
			skipped = s.Skipped
		}
	}
	if !asJSON {
		if skipped != "" {
			fmt.Fprintf(stdout, "%s %s (%s skipped: you rolled back from it)\n", prog, buildinfo.Version, skipped)
		} else {
			fmt.Fprintf(stdout, "%s %s\n", prog, buildinfo.Version)
		}
		return 0
	}
	digest, err := catalogDigest()
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", prog, err)
		return 1
	}
	// The release key IDs: a self update refuses a new binary without any (cli/SELF-UPDATE.md, step 7).
	keys := []string{}
	for _, k := range buildinfo.Keys() {
		if pk, err := minisign.ParsePublicKey(k); err == nil {
			keys = append(keys, pk.IDText())
		}
	}
	return emit(stdout, stderr, struct {
		Version       string   `json:"version"`
		Commit        string   `json:"commit"`
		Repo          string   `json:"repo"`
		Keys          []string `json:"keys"`
		Target        string   `json:"target"`
		Go            string   `json:"go"`
		CatalogDigest string   `json:"catalogDigest"`
		Skipped       string   `json:"skipped,omitempty"`
	}{buildinfo.Version, buildinfo.Commit, buildinfo.Repo, keys, buildinfo.Target(), runtime.Version(), digest, skipped}, 0)
}

// emit writes v as one JSON document on stdout and returns code.
func emit(stdout, stderr io.Writer, v any, code int) int {
	b, err := state.Encode(v)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", prog, err)
		return 1
	}
	if _, err := stdout.Write(b); err != nil {
		return 1
	}
	return code
}

func runDoctor(ctx context.Context, p cli.Parsed, stdout, stderr io.Writer, gather func(context.Context) (doctor.Machine, error)) int {
	m, err := gather(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", prog, err)
		return 1
	}
	checks := doctor.Run(ctx, m)
	if ctx.Err() != nil {
		// The checks it cut short would show as errors; main exits with the signal's code.
		fmt.Fprintf(stderr, "%s: doctor interrupted\n", prog)
		return 1
	}
	errs, warns := doctor.Count(checks)
	code := 0
	if errs > 0 {
		code = 1
	}
	if p.Bool["json"] {
		return emit(stdout, stderr, struct {
			Version  string         `json:"version"`
			Checks   []doctor.Check `json:"checks"`
			Errors   int            `json:"errors"`
			Warnings int            `json:"warnings"`
		}{buildinfo.Version, checks, errs, warns}, code)
	}
	interactive := isTTY(os.Stdin) && isTTY(stderr) && !p.Bool["no-input"]
	u := ui.New(os.Stdin, stderr, interactive, ui.ColorWanted(p.String["color"], isTTY(stderr)))
	u.Quiet, u.Verbose = p.Bool["quiet"], p.Bool["verbose"]
	u.Mode = ui.DecideMode(isTTY(os.Stdout), isTTY(stderr), os.Getenv, false, p.Bool["plain"] || p.Bool["verbose"])
	fix, n := fixCommand(checks)
	if u.Mode == ui.Live {
		u.Header("doctor")
		printDoctorLive(u, checks)
		if interactive && fix != "" {
			return doctorFix(ctx, p, stdout, stderr, fix, n, u, code)
		}
		return code
	}
	for _, c := range checks {
		switch c.Level {
		case doctor.OK:
			u.Ok("%s", c.Message)
		case doctor.Warn:
			u.Warn("%s", c.Message)
		case doctor.Error:
			u.Fail("%s", c.Message)
		case doctor.Skipped:
			u.Info("skipped: %s", c.Message)
		default:
			u.Info("%s", c.Message)
		}
		// --quiet hides ok, info and skipped lines; a fix shows under every line that printed.
		if c.Fix != "" && (!u.Quiet || c.Level == doctor.Warn || c.Level == doctor.Error) {
			fmt.Fprintf(stderr, "   %s\n", u.Dim("fix: "+c.Fix))
		}
	}
	fmt.Fprintf(stderr, "%d errors, %d warnings\n", errs, warns)
	if interactive && fix != "" {
		return doctorFix(ctx, p, stdout, stderr, fix, n, u, code)
	}
	return code
}

// fixCommand says which of setup or update to run for checks, and how many problems it covers:
// update when any problem's fix is "makeit update" (which includes setup's own steps), else
// setup when any is "makeit setup", else "" when every fix needs the user's own hands.
func fixCommand(checks []doctor.Check) (cmd string, n int) {
	setup := false
	for _, c := range checks {
		switch c.Fix {
		case "makeit update":
			cmd = "update"
		case "makeit setup":
			setup = true
		default:
			continue
		}
		n++
	}
	if cmd == "" && setup {
		cmd = "setup"
	}
	return cmd, n
}

// doctorFix asks once, at a terminal, whether to run the fixing command now; Yes runs it with
// --yes and returns its exit code, No or EOF leaves doctor's own code.
func doctorFix(ctx context.Context, p cli.Parsed, stdout, stderr io.Writer, cmd string, n int, u *ui.UI, code int) int {
	yes, err := u.Confirm(fmt.Sprintf("Fix %s now?", plural(n, "problem", "problems")), true)
	if err != nil || !yes {
		return code
	}
	fp := cli.Parsed{Bool: map[string]bool{"yes": true}, String: p.String}
	if cmd == "update" {
		return runUpdate(ctx, fp, nil, stdout, stderr)
	}
	return runSetup(ctx, fp, nil, stdout, stderr)
}

// isTTY reports whether w is a terminal.
// isTTY reports whether w is a terminal; tests replace it.
var isTTY = func(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && ui.IsTerminal(f)
}
