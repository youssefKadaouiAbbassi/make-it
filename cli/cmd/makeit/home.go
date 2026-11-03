package main

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"makeit/internal/buildinfo"
	"makeit/internal/doctor"
	"makeit/internal/fsx"
	"makeit/internal/state"
	"makeit/internal/ui"
)

// autoUpdateOn reads ~/.makeit/config.json; the daily update is on unless "autoUpdate" is false.
func autoUpdateOn(stack string) bool {
	data, found, err := fsx.ReadOptional(filepath.Join(stack, "config.json"))
	if err != nil || !found {
		return true
	}
	var c struct {
		AutoUpdate *bool `json:"autoUpdate"`
	}
	if json.Unmarshal(data, &c) != nil || c.AutoUpdate == nil {
		return true
	}
	return *c.AutoUpdate
}

// lastChange describes the newest generation: when, which command, and the plugins it moved.
func lastChange(stack string, now time.Time) string {
	gens, err := state.LoadGenerations(stack)
	if err != nil || len(gens) == 0 {
		return "never"
	}
	m := gens[0].Manifest
	at := m.FinishedAt
	if at.IsZero() {
		at = m.StartedAt
	}
	when := at.Local().Format("Jan 2 15:04")
	if y, mo, d := now.Date(); at.Local().Year() == y && at.Local().Month() == mo && at.Local().Day() == d {
		when = "today " + at.Local().Format("15:04")
	}
	s := when + " · " + m.Command
	if moved := m.Plugins.Updated; len(moved) > 0 {
		s += ": " + strings.Join(moved, ", ")
	}
	return s
}

// runHome is bare `makeit`: a status card, then, at a terminal, what to do next.
func runHome(ctx context.Context, stdout, stderr io.Writer, gather func(context.Context) (doctor.Machine, error)) int {
	m, err := gather(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", prog, err)
		return 1
	}
	checks := doctor.Run(ctx, m)
	errs, warns := doctor.Count(checks)
	interactive := isTTY(os.Stdin) && isTTY(stdout) && isTTY(stderr)
	u := ui.New(os.Stdin, stdout, interactive, ui.ColorWanted("", isTTY(stdout)))
	u.Mode = ui.DecideMode(isTTY(stdout), isTTY(stderr), os.Getenv, false, false)
	stack := filepath.Dir(m.StatePath)
	_, statErr := os.Stat(m.StatePath)
	setUp := m.StatePath != "" && statErr == nil

	u.Header("")
	u.KV("makeit", buildinfo.Version, false)
	switch {
	case !setUp:
		u.KV("status", u.Dot(false)+" not set up "+u.Faint("· makeit setup sets this machine up"), false)
	case errs == 0:
		u.KV("status", u.Dot(true)+" ready "+u.Faint(fmt.Sprintf("· %d checks passed", passed(checks))), false)
	default:
		u.KV("status", u.Dot(false)+fmt.Sprintf(" %d problems", errs), false)
	}
	u.KV("last update", lastChange(stack, time.Now()), false)
	auto := "on " + u.Faint("· daily, when Claude Code starts")
	if !autoUpdateOn(stack) {
		auto = "off " + u.Faint("· autoUpdate is false in "+filepath.Join(stack, "config.json"))
	}
	u.KV("auto-update", auto, false)
	if n := errs + warns; setUp && n > 0 {
		for _, c := range checks {
			if c.Level == doctor.Error || c.Level == doctor.Warn {
				u.KV(fmt.Sprintf("%d to fix", n), c.Message+" "+u.Faint("· see makeit doctor"), true)
				break
			}
		}
	}
	if u.Mode != ui.Live || !interactive {
		return 0
	}
	_, _ = fmt.Fprintln(stdout)
	type choice struct{ label, cmd string }
	choices := []choice{{"Update now", "update"}, {"Doctor: every check, with fixes", "doctor"}, {"Set up this project", "init"}, {"Rollback the last update", "rollback"}, {"Nothing", ""}}
	if !setUp {
		choices = append([]choice{{"Set up this machine", "setup"}}, choices[1:3]...)
		choices = append(choices, choice{"Nothing", ""})
	}
	labels := make([]string, len(choices))
	for i, c := range choices {
		labels[i] = c.label
	}
	i, err := u.Select("What next?", labels, 0)
	if err != nil || choices[i].cmd == "" {
		return 0
	}
	return run(ctx, []string{choices[i].cmd}, stdout, stderr, gather)
}
