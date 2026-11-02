package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Mode is how a run draws: Plain is today's lines (pipes, CI, --json, --plain); Live is the
// grammar of the 0.2.0 design (header, rows with a glyph, a result line, the next step).
type Mode int

const (
	Plain Mode = iota
	Live
)

// DecideMode picks Live only for a person at a terminal: both streams are terminals, colour is
// not refused, the run is not CI, and neither --json nor --plain was asked for.
func DecideMode(stdoutTTY, stderrTTY bool, getenv func(string) string, json, plain bool) Mode {
	if !stdoutTTY || !stderrTTY || json || plain {
		return Plain
	}
	if getenv("NO_COLOR") != "" || getenv("TERM") == "dumb" || getenv("CI") != "" {
		return Plain
	}
	return Live
}

// Glyph is the one-character status at the start of a row.
type Glyph int

const (
	GDone Glyph = iota
	GChange
	GWarn
	GFail
	GPending
)

// LabelWidth is the label column of a row.
const LabelWidth = 15

var (
	green  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	yellow = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	red    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	faint  = lipgloss.NewStyle().Faint(true)
	strong = lipgloss.NewStyle().Bold(true)
)

// accent is the brand blue: #4d8ef7 on dark backgrounds, #1f6feb on light ones.
func (u *UI) accent() lipgloss.Style {
	c := lipgloss.Color("#4d8ef7")
	if u.Light {
		c = lipgloss.Color("#1f6feb")
	}
	return lipgloss.NewStyle().Foreground(c)
}

func (u *UI) style(s lipgloss.Style, text string) string {
	if !u.Color {
		return text
	}
	return s.Render(text)
}

// glyph is a row's status mark, coloured.
func (u *UI) glyph(g Glyph) string {
	switch g {
	case GDone:
		return u.style(green, "✓")
	case GChange:
		return u.style(yellow, "●")
	case GWarn:
		return u.style(yellow, "!")
	case GFail:
		return u.style(red, "✗")
	}
	return u.style(faint, "·")
}

// Group is the area a step belongs to in the live view; a step outside the known areas is its own.
func Group(stepID, title string) string {
	head, _, _ := strings.Cut(stepID, ".")
	switch head {
	case "prereq":
		return "Machine"
	case "claude":
		return "Claude Code"
	case "catalog", "marketplace", "bundle", "plugin":
		return "Plugins"
	case "settings":
		return "Settings"
	case "global":
		return "Files"
	case "self":
		return "makeit"
	}
	return title
}

// fit cuts s to width display cells, ending in … when it had to cut.
func fit(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	return ansi.Truncate(s, width, "…")
}

func pad(s string, width int) string {
	if n := lipgloss.Width(s); n < width {
		return s + strings.Repeat(" ", width-n)
	}
	return s
}

// Header starts a live run: ">_ makeit  <command>" between blank lines. Plain runs have none.
func (u *UI) Header(cmd string) {
	if u.Mode != Live || u.Quiet {
		return
	}
	h := u.style(u.accent().Bold(true), ">_") + " " + u.style(strong, "makeit")
	if cmd != "" {
		h += "  " + u.style(faint, cmd)
	}
	_, _ = fmt.Fprintf(u.Out, "\n%s\n\n", h)
}

// RowText is one row as the live view draws it, without the newline.
func (u *UI) RowText(g Glyph, label, detail, note string) string {
	if g == GPending {
		return "  " + u.glyph(g) + " " + u.style(faint, label)
	}
	s := "  " + u.glyph(g) + " " + pad(label, LabelWidth)
	if detail != "" {
		s += detail
	}
	if note != "" {
		s += " " + u.style(faint, "· "+note)
	}
	return strings.TrimRight(s, " ")
}

// Row prints one status row: live, the glyph and the label column; plain, today's words.
func (u *UI) Row(g Glyph, label, detail, note string) {
	if u.Mode == Live {
		if !u.Quiet || g == GWarn || g == GFail {
			_, _ = fmt.Fprintln(u.Out, u.RowText(g, label, detail, note))
		}
		return
	}
	msg := strings.TrimSpace(label + " " + detail)
	if note != "" {
		msg += " · " + note
	}
	switch g {
	case GDone:
		u.Ok("%s", msg)
	case GChange:
		u.Info("will: %s", msg)
	case GWarn:
		u.Warn("%s", msg)
	case GFail:
		u.Fail("%s", msg)
	default:
		u.Info("%s", msg)
	}
}

// KV prints one line of a status card: a faint label column and its value; plain, "label: value".
// warn colours the label yellow (the "to fix" line).
func (u *UI) KV(label, value string, warn bool) {
	if u.Mode != Live {
		_, _ = fmt.Fprintf(u.Out, "%s: %s\n", label, value)
		return
	}
	l := u.style(faint, pad(label, 14))
	if warn {
		l = u.style(yellow, pad(label, 14))
	}
	_, _ = fmt.Fprintf(u.Out, "  %s%s\n", l, value)
}

// Dot is a coloured status dot for a card value: green when ok, yellow when not.
func (u *UI) Dot(ok bool) string {
	if ok {
		return u.style(green, "●")
	}
	return u.style(yellow, "●")
}

// Faint is secondary text.
func (u *UI) Faint(s string) string { return u.style(faint, s) }

// Result is a live run's last line: "✓ Updated in 4s · <counts>", or ✗ when it failed.
func (u *UI) Result(ok bool, verb string, elapsed time.Duration, counts []string) {
	if u.Mode != Live {
		return
	}
	mark := u.style(green.Bold(true), "✓ "+verb)
	if !ok {
		mark = u.style(red.Bold(true), "✗ "+verb)
	}
	rest := fmt.Sprintf("in %ds", int(elapsed.Round(time.Second)/time.Second))
	for _, c := range counts {
		rest += " · " + c
	}
	_, _ = fmt.Fprintf(u.Out, "\n  %s %s\n", mark, u.style(faint, rest))
}

// Summary is a result line without a duration, for a check rather than a run ("✓ Healthy · 1 warning").
func (u *UI) Summary(ok bool, verb string, notes ...string) {
	if u.Mode != Live {
		return
	}
	mark := u.style(green.Bold(true), "✓ "+verb)
	if !ok {
		mark = u.style(red.Bold(true), "✗ "+verb)
	}
	rest := ""
	for _, n := range notes {
		rest += " · " + n
	}
	_, _ = fmt.Fprintf(u.Out, "\n  %s%s\n", mark, u.style(faint, rest))
}

// Fix is the line under a problem row: "fix <command>".
func (u *UI) Fix(cmd string) {
	if u.Mode == Live {
		_, _ = fmt.Fprintf(u.Out, "    %s %s\n", u.style(faint, "fix"), cmd)
	}
}

// Next is the line under the result: the next step, then quieter hints such as the undo.
func (u *UI) Next(lines ...string) {
	if u.Mode != Live || len(lines) == 0 {
		return
	}
	s := "    " + lines[0]
	for _, l := range lines[1:] {
		s += "  " + u.style(faint, l)
	}
	_, _ = fmt.Fprintln(u.Out, s)
}
