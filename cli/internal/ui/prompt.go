package ui

import (
	"errors"
	"fmt"
	"io"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"golang.org/x/term"
)

// theme is Huh's base theme with makeit's accent and no left border, so a question sits in the
// same column as the rows above it.
func (u *UI) theme(isDark bool) *huh.Styles {
	s := huh.ThemeBase(isDark)
	s.Focused.Base = lipgloss.NewStyle()
	s.Blurred.Base = lipgloss.NewStyle()
	if !u.Color {
		return s
	}
	s.Focused.Title = strong
	s.Focused.SelectSelector = u.accent().SetString("› ")
	s.Focused.SelectedOption = u.accent()
	s.Focused.FocusedButton = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(u.accent().GetForeground()).Padding(0, 1)
	s.Focused.BlurredButton = faint.Padding(0, 1)
	return s
}

// abortOnEOF hands Huh one Ctrl-C when its input ends, so a closed terminal or pipe ends the
// question the way a person pressing Ctrl-C would, instead of waiting forever.
type abortOnEOF struct {
	r    io.Reader
	sent bool
}

func (a *abortOnEOF) Read(p []byte) (int, error) {
	n, err := a.r.Read(p)
	if errors.Is(err, io.EOF) && n == 0 && !a.sent && len(p) > 0 {
		a.sent = true
		// Keys read just before the end (Enter) settle the form first.
		time.Sleep(300 * time.Millisecond)
		p[0] = 0x03
		return 1, nil
	}
	return n, err
}

// formInput is what Huh reads: a terminal as itself, so Bubble Tea can put it in raw mode (wrapped,
// keys are echoed and never reach the form); anything else wrapped in abortOnEOF.
func (u *UI) formInput() io.Reader {
	if u.file != nil && term.IsTerminal(int(u.file.Fd())) {
		return u.file
	}
	return &abortOnEOF{r: u.in}
}

// keymap is Huh's, with Esc and q leaving a question like Ctrl-C (Huh's own quits on Ctrl-C only).
func keymap() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc", "q"))
	return km
}

func (u *UI) runForm(field huh.Field) (aborted bool, err error) {
	f := huh.NewForm(huh.NewGroup(field)).
		WithInput(u.formInput()).WithOutput(u.Out).WithShowHelp(false).WithKeyMap(keymap()).
		WithTheme(huh.ThemeFunc(u.theme))
	if err := f.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

// confirmLive is the live mode's question: an inline Yes/No, Enter takes def, Esc answers no.
func (u *UI) confirmLive(q string, def bool) (bool, error) {
	yes := def
	aborted, err := u.runForm(huh.NewConfirm().Title("  " + q).Affirmative("Yes").Negative("No").Inline(true).Value(&yes))
	if err != nil {
		return false, err
	}
	yes = yes && !aborted
	// Huh clears the question when answered; keep it, with the answer, in the scrollback.
	answer := "No"
	if yes {
		answer = "Yes"
	}
	_, _ = fmt.Fprintf(u.Out, "  %s %s\n", u.style(strong, q), u.style(faint, answer))
	return yes, nil
}

// selectLive is the live mode's menu: arrows move, Enter picks, Esc leaves.
func (u *UI) selectLive(q string, opts []string, def int) (int, error) {
	choice := def
	var options []huh.Option[int]
	for i, o := range opts {
		options = append(options, huh.NewOption(o, i))
	}
	aborted, err := u.runForm(huh.NewSelect[int]().Title("  " + q).Options(options...).Value(&choice))
	if err != nil {
		return 0, err
	}
	if aborted {
		return 0, ErrCancelled
	}
	_, _ = fmt.Fprintf(u.Out, "  %s %s\n", u.style(strong, q), u.style(faint, opts[choice]))
	return choice, nil
}
