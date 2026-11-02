package main

import (
	"bytes"
	"strings"
	"testing"

	"makeit/internal/doctor"
	"makeit/internal/ui"
)

func TestDoctorLiveSummary(t *testing.T) {
	var checks []doctor.Check
	for i := 0; i < 31; i++ {
		checks = append(checks, doctor.Check{ID: "x", Level: doctor.OK, Message: "fine"})
	}
	checks = append(checks, doctor.Check{ID: "gopls", Level: doctor.Warn, Message: "gopls is missing", Fix: "go install golang.org/x/tools/gopls@latest"})
	var out bytes.Buffer
	u := ui.NewFromReader(nil, &out, false)
	u.Mode = ui.Live
	printDoctorLive(u, checks)
	s := out.String()
	for _, want := range []string{"✓ 31 checks passed", "! gopls is missing", "fix go install golang.org/x/tools/gopls@latest", "Healthy", "1 warning"} {
		if !strings.Contains(s, want) {
			t.Errorf("doctor output has no %q:\n%s", want, s)
		}
	}
	if strings.Count(s, "fine") != 0 {
		t.Errorf("passing checks listed one by one:\n%s", s)
	}
}

func TestDoctorLiveErrors(t *testing.T) {
	checks := []doctor.Check{{Level: doctor.OK, Message: "fine"}, {Level: doctor.Error, Message: "Claude Code is missing", Fix: "makeit setup"}}
	var out bytes.Buffer
	u := ui.NewFromReader(nil, &out, false)
	u.Mode = ui.Live
	printDoctorLive(u, checks)
	if s := out.String(); !strings.Contains(s, "✗ Claude Code is missing") || !strings.Contains(s, "1 problem") {
		t.Errorf("output:\n%s", s)
	}
}
