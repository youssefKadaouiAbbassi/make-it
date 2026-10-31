package cli

import (
	"fmt"
	"strings"
)

// Suggest returns the name in names closest to name within an edit distance of 2, the earlier
// one on a tie, or "" when none is that close. It only names a guess; callers never run it.
func Suggest(name string, names []string) string {
	if name == "" {
		return ""
	}
	best, bestDist := "", 3
	for _, n := range names {
		if d := distance(name, n); d < bestDist {
			best, bestDist = n, d
		}
	}
	return best
}

// distance is the Levenshtein distance between a and b, counted in runes.
func distance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

// ExitCode is one row of `help exit-codes`.
type ExitCode struct {
	Code    int
	Meaning string
}

// ExitCodes are the exit codes of every command (DESIGN.md section 3).
var ExitCodes = []ExitCode{
	{0, "ok"},
	{1, "a step failed, or doctor found an error"},
	{2, "usage error, or consent needed"},
	{130, "interrupted (SIGINT)"},
	{143, "terminated (SIGTERM)"},
}

// EnvVar is one row of `help environment`.
type EnvVar struct {
	Name, Meaning string
}

// Environment lists every variable makeit reads; a test keeps it in step with the code.
var Environment = []EnvVar{
	{"CLAUDE_CONFIG_DIR", "Claude Code's configuration directory (default ~/.claude)"},
	{"MAKEIT_DIR", "where makeit keeps its copy of the plugin set, state and generations (default ~/.makeit)"},
	{"MAKEIT_RELEASES_URL", "where makeit looks for releases (https only; default https://github.com)"},
	{"MAKEIT_REEXEC", "set by update for the new makeit it runs: the version it replaced"},
	{"GIT_SSH_COMMAND", "used for a --catalog git source over ssh when set; otherwise ssh runs in batch mode"},
	{"GIT_SSH", "used for a --catalog git source over ssh when set, as is core.sshCommand; makeit then leaves ssh as you set it"},
	{"HOME", "the home directory (USERPROFILE on Windows)"},
	{"PATH", "where claude, npm, git and the package managers are looked up"},
	{"NO_COLOR", "when set, no colour"},
	{"FORCE_COLOR", "when set, colour even without a terminal (NO_COLOR, TERM=dumb and ACCESSIBLE win)"},
	{"TERM", "dumb turns colour off"},
	{"ACCESSIBLE", "when set, no colour"},
	{"WSL_DISTRO_NAME", "when set, the machine is taken as WSL"},
	{"WSL_INTEROP", "when set, the machine is taken as WSL"},
}

// Topic renders a help topic: exit-codes or environment.
func Topic(prog, name string) (string, bool) {
	var b strings.Builder
	switch name {
	case "exit-codes":
		fmt.Fprintf(&b, "%s exit codes:\n", prog)
		for _, e := range ExitCodes {
			fmt.Fprintf(&b, "  %-5d %s\n", e.Code, e.Meaning)
		}
	case "environment":
		fmt.Fprintf(&b, "Environment variables %s reads:\n", prog)
		for _, e := range Environment {
			fmt.Fprintf(&b, "  %-19s %s\n", e.Name, e.Meaning)
		}
	default:
		return "", false
	}
	return b.String(), true
}
