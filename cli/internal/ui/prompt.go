package ui

import (
	"errors"
	"fmt"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
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

func (u *UI) runForm(field huh.Field) (aborted bool, err error) {
	f := huh.NewForm(huh.NewGroup(field)).
		WithInput(u.in).WithOutput(u.Out).WithShowHelp(false).
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
