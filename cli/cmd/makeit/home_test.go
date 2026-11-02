package main

import (
	"context"
	"strings"
	"testing"

	"makeit/internal/catalog"
	"makeit/internal/doctor"
)

func fakeGather(context.Context) (doctor.Machine, error) {
	return doctor.Machine{Version: "0.2.0", Catalog: catalog.Embedded, Getenv: func(string) string { return "" }}, nil
}

func TestHelpListsTheCommands(t *testing.T) {
	_, out, _ := call(t, noGather(t), "help")
	for _, c := range []string{"setup", "init", "update", "doctor", "rollback", "uninstall", "version"} {
		if !strings.Contains(out, "\n  "+c) {
			t.Errorf("help does not list %s:\n%s", c, out)
		}
	}
	for _, hidden := range []string{"\n  self", "\n  docs"} {
		if strings.Contains(out, hidden) {
			t.Errorf("help lists a hidden command %q:\n%s", strings.TrimSpace(hidden), out)
		}
	}
}

func TestSelfUpdateGone(t *testing.T) {
	for sub, now := range map[string]string{"update": "makeit update", "rollback": "makeit rollback"} {
		code, _, errOut := call(t, noGather(t), "self", sub)
		if code != 2 || !strings.Contains(errOut, now) {
			t.Errorf("self %s: exit %d, stderr %q; want exit 2 naming %q", sub, code, errOut, now)
		}
	}
}

func TestUpdateTakesTo(t *testing.T) {
	code, _, errOut := call(t, noGather(t), "update", "--allow-downgrade")
	if code != 2 || !strings.Contains(errOut, "--to") {
		t.Errorf("exit %d, stderr %q; want --allow-downgrade to need --to", code, errOut)
	}
}

func TestBareMakeitPlainCard(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	code, out, _ := call(t, fakeGather)
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	for _, label := range []string{"makeit", "status", "last update", "auto-update"} {
		if !strings.Contains(out, label) {
			t.Errorf("card has no %q:\n%s", label, out)
		}
	}
	if strings.Contains(out, "What next?") {
		t.Errorf("plain card asked a question:\n%s", out)
	}
}
