// Package cli parses arguments: GNU-style long and short flags, accepted before or after the
// command and its positionals, with "--" ending the flags.
package cli

import (
	"fmt"
	"sort"
	"strings"
)

// Flag describes one option.
type Flag struct {
	Name  string
	Short string
	// Value names the argument of a string flag ("path"); empty means a boolean flag.
	Value string
	Help  string
}

// Command describes one subcommand.
type Command struct {
	Name     string
	Args     string // usage of the positionals, such as "[dir]"
	Summary  string
	Flags    []Flag
	Modifies bool
}

// Parsed is the result of Parse.
type Parsed struct {
	Bool        map[string]bool
	String      map[string]string
	Positionals []string
}

// Global are accepted by every command.
var Global = []Flag{
	{Name: "help", Short: "h", Help: "show help for the command"},
	{Name: "yes", Short: "y", Help: "apply the printed plan and take the recommended answer to every question; the only way to apply without a terminal"},
	{Name: "dry-run", Short: "n", Help: "print the plan and the diffs; write nothing, ask nothing"},
	{Name: "no-input", Help: "never ask; a missing answer exits 2 naming its flag"},
	{Name: "json", Help: "print one JSON report on stdout (implies --no-input); progress stays on stderr"},
	{Name: "quiet", Short: "q", Help: "print only warnings and errors"},
	{Name: "verbose", Help: "print the commands run and their output"},
	{Name: "color", Value: "auto|always|never", Help: "colour output (default auto; NO_COLOR is honoured)"},
	{Name: "offline", Help: "makeit itself makes no network request"},
}

// Parse reads args for cmd.
func Parse(cmd Command, args []string) (Parsed, error) {
	p := Parsed{Bool: map[string]bool{}, String: map[string]string{}}
	flags := append(append([]Flag(nil), Global...), cmd.Flags...)
	byName := map[string]Flag{}
	for _, f := range flags {
		byName["--"+f.Name] = f
		if f.Short != "" {
			byName["-"+f.Short] = f
		}
		if f.Value == "" {
			p.Bool[f.Name] = false
		}
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			p.Positionals = append(p.Positionals, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			p.Positionals = append(p.Positionals, a)
			continue
		}
		name, value, hasValue := strings.Cut(a, "=")
		if !strings.HasPrefix(name, "--") && len(name) > 2 && !hasValue {
			// Grouped short booleans: -yn.
			for _, c := range name[1:] {
				f, ok := byName["-"+string(c)]
				if !ok || f.Value != "" {
					return p, fmt.Errorf("unknown flag -%c in %s", c, a)
				}
				p.Bool[f.Name] = true
			}
			continue
		}
		f, ok := byName[name]
		if !ok {
			return p, fmt.Errorf("unknown flag %s for %s%s", name, cmd.Name, suggest(name, flags))
		}
		if f.Value == "" {
			if hasValue {
				return p, fmt.Errorf("%s takes no value", name)
			}
			p.Bool[f.Name] = true
			continue
		}
		if !hasValue {
			if i+1 >= len(args) {
				return p, fmt.Errorf("%s needs a value (%s)", name, f.Value)
			}
			i++
			value = args[i]
		}
		p.String[f.Name] = value
	}
	return p, nil
}

func suggest(name string, flags []Flag) string {
	n := strings.TrimLeft(name, "-")
	for _, f := range flags {
		if strings.HasPrefix(f.Name, n) || strings.HasPrefix(n, f.Name) {
			return "; did you mean --" + f.Name + "?"
		}
	}
	return ""
}

func flagLines(flags []Flag) []string {
	sorted := append([]Flag(nil), flags...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	var lines []string
	for _, f := range sorted {
		left := "--" + f.Name
		if f.Value != "" {
			left += " <" + f.Value + ">"
		}
		if f.Short != "" {
			left += ", -" + f.Short
		}
		lines = append(lines, fmt.Sprintf("  %-34s %s", left, f.Help))
	}
	return lines
}

// Usage is the top-level help text.
func Usage(prog, version string, cmds []Command) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s: set up Claude Code the same way on every machine and every project.\n\n", prog, version)
	fmt.Fprintf(&b, "Usage: %s <command> [flags]\n\nCommands:\n", prog)
	for _, c := range cmds {
		fmt.Fprintf(&b, "  %-22s %s\n", strings.TrimSpace(c.Name+" "+c.Args), c.Summary)
	}
	b.WriteString("\nFlags for every command:\n")
	b.WriteString(strings.Join(flagLines(Global), "\n"))
	fmt.Fprintf(&b, "\n\nRun '%s help <command>' for its flags, '%s help exit-codes' or '%s help environment'.\n", prog, prog, prog)
	return b.String()
}

// CommandUsage is the help text of one command.
func CommandUsage(prog string, c Command) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Usage: %s %s [flags]\n\n%s\n", prog, strings.TrimSpace(c.Name+" "+c.Args), c.Summary)
	if len(c.Flags) > 0 {
		b.WriteString("\nFlags:\n")
		b.WriteString(strings.Join(flagLines(c.Flags), "\n"))
		b.WriteString("\n")
	}
	b.WriteString("\nFlags for every command:\n")
	b.WriteString(strings.Join(flagLines(Global), "\n"))
	b.WriteString("\n")
	return b.String()
}
