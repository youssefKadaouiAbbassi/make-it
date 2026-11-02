package main

import (
	"fmt"

	"makeit/internal/doctor"
	"makeit/internal/ui"
)

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// printDoctorLive is doctor at a terminal: the passing checks counted, every problem spelled out
// with its fix, one summary line. --verbose and plain mode list every check instead.
func printDoctorLive(u *ui.UI, checks []doctor.Check) {
	passed := 0
	for _, c := range checks {
		if c.Level == doctor.OK {
			passed++
		}
	}
	u.Row(ui.GDone, plural(passed, "check passed", "checks passed"), "", "")
	errs, warns := doctor.Count(checks)
	for _, c := range checks {
		switch c.Level {
		case doctor.Warn:
			u.Row(ui.GWarn, c.Message, "", "")
		case doctor.Error:
			u.Row(ui.GFail, c.Message, "", "")
		default:
			continue
		}
		if c.Fix != "" {
			u.Fix(c.Fix)
		}
	}
	switch {
	case errs > 0:
		u.Summary(false, plural(errs, "problem", "problems"), "every check: makeit doctor --verbose")
	case warns > 0:
		u.Summary(true, "Healthy", plural(warns, "warning", "warnings"), "every check: makeit doctor --verbose")
	default:
		u.Summary(true, "Healthy")
	}
}
