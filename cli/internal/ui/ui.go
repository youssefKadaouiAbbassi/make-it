// Package ui prints progress to stderr and asks questions on the terminal. It never draws a
// full-screen interface: prompts are plain lines that work with screen readers, over ssh, and
// with ACCESSIBLE or TERM=dumb. Stdout stays free for --json.
package ui

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"
)

// ErrCancelled means the user ended a prompt (Ctrl-D, or q on a list).
var ErrCancelled = errors.New("cancelled")

// UI is the reporter and prompter for one run.
type UI struct {
	Out         io.Writer
	Interactive bool
	Color       bool
	Quiet       bool
	Verbose     bool
	in          *bufio.Reader
	fd          int
}

// New builds a UI. in is read only when Interactive is set.
func New(in *os.File, out io.Writer, interactive, color bool) *UI {
	return &UI{Out: out, Interactive: interactive, Color: color, in: bufio.NewReader(in), fd: int(in.Fd())}
}

// NewFromReader is New for tests: prompts read from r.
func NewFromReader(r io.Reader, out io.Writer, interactive bool) *UI {
	return &UI{Out: out, Interactive: interactive, in: bufio.NewReader(r), fd: -1}
}

// ColorWanted applies NO_COLOR, FORCE_COLOR, TERM=dumb and the --color flag.
func ColorWanted(flag string, isTTY bool) bool {
	switch flag {
	case "always":
		return true
	case "never":
		return false
	}
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" || os.Getenv("ACCESSIBLE") != "" {
		return false
	}
	if os.Getenv("FORCE_COLOR") != "" {
		return true
	}
	return isTTY
}

// IsTerminal reports whether f is a terminal.
func IsTerminal(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }

func (u *UI) paint(code, s string) string {
	if !u.Color {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// Dim, Bold and the status marks are the only styling.
func (u *UI) Dim(s string) string  { return u.paint("2", s) }
func (u *UI) Bold(s string) string { return u.paint("1", s) }

func (u *UI) line(mark, msg string) {
	_, _ = fmt.Fprintf(u.Out, "%s %s\n", mark, msg)
}

// Title prints a heading line.
func (u *UI) Title(msg string) {
	if !u.Quiet {
		_, _ = fmt.Fprintln(u.Out, u.Bold(msg))
	}
}

// Info prints a neutral line; hidden by --quiet.
func (u *UI) Info(format string, a ...any) {
	if !u.Quiet {
		u.line(" ", fmt.Sprintf(format, a...))
	}
}

// Debug prints only with --verbose.
func (u *UI) Debug(format string, a ...any) {
	if u.Verbose {
		u.line(u.Dim("."), u.Dim(fmt.Sprintf(format, a...)))
	}
}

// Ok prints a success line.
func (u *UI) Ok(format string, a ...any) {
	if !u.Quiet {
		u.line(u.paint("32", "ok"), fmt.Sprintf(format, a...))
	}
}

// Warn prints a warning; shown even with --quiet.
func (u *UI) Warn(format string, a ...any) { u.line(u.paint("33", "!!"), fmt.Sprintf(format, a...)) }

// Fail prints an error line; shown even with --quiet.
func (u *UI) Fail(format string, a ...any) { u.line(u.paint("31", "xx"), fmt.Sprintf(format, a...)) }

// Plain prints text as is.
func (u *UI) Plain(s string) {
	if !u.Quiet {
		_, _ = fmt.Fprint(u.Out, s)
	}
}

func (u *UI) readLine() (string, error) {
	s, err := u.in.ReadString('\n')
	if err != nil && (s == "" || !errors.Is(err, io.EOF)) {
		if errors.Is(err, io.EOF) {
			return "", ErrCancelled
		}
		return "", err
	}
	return strings.TrimSpace(s), nil
}

func (u *UI) need() error {
	if !u.Interactive {
		return errors.New("a question needs an answer but there is no terminal; pass --yes or the flag for it")
	}
	return nil
}

// Confirm asks a yes/no question. def is the answer on Enter.
func (u *UI) Confirm(q string, def bool) (bool, error) {
	if err := u.need(); err != nil {
		return false, err
	}
	hint := "[y/N]"
	if def {
		hint = "[Y/n]"
	}
	for {
		_, _ = fmt.Fprintf(u.Out, "? %s %s ", q, hint)
		s, err := u.readLine()
		if err != nil {
			return false, err
		}
		switch strings.ToLower(s) {
		case "":
			return def, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		_, _ = fmt.Fprintln(u.Out, "  answer y or n")
	}
}

// Select asks for one of opts by number; def is the index chosen on Enter.
func (u *UI) Select(q string, opts []string, def int) (int, error) {
	if err := u.need(); err != nil {
		return 0, err
	}
	_, _ = fmt.Fprintf(u.Out, "? %s\n", q)
	for i, o := range opts {
		mark := " "
		if i == def {
			mark = "*"
		}
		_, _ = fmt.Fprintf(u.Out, "  %s %d) %s\n", mark, i+1, o)
	}
	for {
		_, _ = fmt.Fprintf(u.Out, "  number [%d]: ", def+1)
		s, err := u.readLine()
		if err != nil {
			return 0, err
		}
		if s == "" {
			return def, nil
		}
		if s == "q" {
			return 0, ErrCancelled
		}
		if n, err := strconv.Atoi(s); err == nil && n >= 1 && n <= len(opts) {
			return n - 1, nil
		}
		_, _ = fmt.Fprintf(u.Out, "  pick 1 to %d\n", len(opts))
	}
}

// Multi asks which of opts apply; preset are the ones ticked on Enter. The answer is a list of
// numbers separated by commas or spaces, or "none".
func (u *UI) Multi(q string, opts []string, preset []bool) ([]bool, error) {
	if err := u.need(); err != nil {
		return nil, err
	}
	_, _ = fmt.Fprintf(u.Out, "? %s\n", q)
	for i, o := range opts {
		mark := "[ ]"
		if preset[i] {
			mark = "[x]"
		}
		_, _ = fmt.Fprintf(u.Out, "  %s %d) %s\n", mark, i+1, o)
	}
	for {
		_, _ = fmt.Fprint(u.Out, "  numbers, Enter keeps the ticks, none for none: ")
		s, err := u.readLine()
		if err != nil {
			return nil, err
		}
		if s == "" {
			return append([]bool(nil), preset...), nil
		}
		out := make([]bool, len(opts))
		if s == "none" {
			return out, nil
		}
		ok := true
		for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
			n, err := strconv.Atoi(f)
			if err != nil || n < 1 || n > len(opts) {
				ok = false
				break
			}
			out[n-1] = true
		}
		if ok {
			return out, nil
		}
		_, _ = fmt.Fprintf(u.Out, "  use numbers 1 to %d\n", len(opts))
	}
}

// Text asks for a line; def is used on Enter.
func (u *UI) Text(q, def string) (string, error) {
	if err := u.need(); err != nil {
		return "", err
	}
	if def != "" {
		_, _ = fmt.Fprintf(u.Out, "? %s [%s]: ", q, def)
	} else {
		_, _ = fmt.Fprintf(u.Out, "? %s: ", q)
	}
	s, err := u.readLine()
	if err != nil {
		return "", err
	}
	if s == "" {
		return def, nil
	}
	return s, nil
}

// Secret asks for a value without echoing it.
func (u *UI) Secret(q string) (string, error) {
	if err := u.need(); err != nil {
		return "", err
	}
	_, _ = fmt.Fprintf(u.Out, "? %s (hidden): ", q)
	if u.fd < 0 {
		return u.readLine()
	}
	b, err := term.ReadPassword(u.fd)
	_, _ = fmt.Fprintln(u.Out)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
