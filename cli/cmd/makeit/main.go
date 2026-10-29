// Command makeit installs and keeps up to date the Claude Code build stack on this machine.
// This build has the commands that change nothing: version, help and doctor
// (cli/READ-ONLY-COMMANDS.md).
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
	"makeit/internal/state"
	"makeit/internal/ui"
)

const prog = "makeit"

// commands lists only what this build has, so usage never names a command that does not exist.
var commands = []cli.Command{
	{Name: "doctor", Summary: "check this machine against what the stack expects; changes nothing"},
	{Name: "help", Args: "[command|exit-codes|environment]", Summary: "show help for a command or a topic"},
	{Name: "setup", Summary: "install the stack on this machine: prerequisites, Claude Code, the catalog, settings and global files"},
	{Name: "version", Summary: "print the version"},
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	done := make(chan int, 1)
	go func() { done <- run(ctx, os.Args[1:], os.Stdout, os.Stderr, gather) }()
	select {
	case code := <-done:
		os.Exit(code)
	case sig := <-sigs:
		// Cancelling kills the running claude child (not processes it started); a second signal
		// does not wait for it.
		cancel()
		select {
		case <-done:
		case <-sigs:
		}
		os.Exit(signalCode(sig))
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
		return "", fmt.Errorf("the embedded catalog: %w", err)
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
		fmt.Fprint(stderr, cli.Usage(prog, buildinfo.Version, commands))
		return 2
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
	case "setup":
		if len(p.Positionals) > 0 {
			fmt.Fprintf(stderr, "%s: setup takes no arguments\n", prog)
			return 2
		}
		return runSetup(ctx, p, args, stdout, stderr)
	case "version":
		if len(p.Positionals) > 0 {
			fmt.Fprintf(stderr, "%s: version takes no arguments\n", prog)
			return 2
		}
		return version(p.Bool["json"], stdout, stderr)
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

func version(asJSON bool, stdout, stderr io.Writer) int {
	if !asJSON {
		fmt.Fprintf(stdout, "%s %s\n", prog, buildinfo.Version)
		return 0
	}
	digest, err := catalogDigest()
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", prog, err)
		return 1
	}
	return emit(stdout, stderr, struct {
		Version       string `json:"version"`
		Commit        string `json:"commit"`
		Repo          string `json:"repo"`
		Target        string `json:"target"`
		Go            string `json:"go"`
		CatalogDigest string `json:"catalogDigest"`
	}{buildinfo.Version, buildinfo.Commit, buildinfo.Repo, buildinfo.Target(), runtime.Version(), digest}, 0)
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
	u := ui.New(os.Stdin, stderr, false, ui.ColorWanted(p.String["color"], isTTY(stderr)))
	u.Quiet, u.Verbose = p.Bool["quiet"], p.Bool["verbose"]
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
	return code
}

// isTTY reports whether w is a terminal.
func isTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && ui.IsTerminal(f)
}
