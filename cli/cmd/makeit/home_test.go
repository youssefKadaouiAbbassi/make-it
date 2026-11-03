package main

import (
	"bytes"
	"context"
	"errors"
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
	if strings.Contains(out, "checking") || strings.Contains(out, "\x1b[") {
		t.Errorf("plain card has a checking line or an escape sequence:\n%q", out)
	}
}

// The home prints its header and version before it checks, with a faint "checking" line that the
// result replaces (cursor up, clear line).
func TestHomeHeaderBeforeChecks(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fakeTTYPrompt(t, "q")
	var out, errb bytes.Buffer
	headerFirst := false
	gather := func(context.Context) (doctor.Machine, error) {
		headerFirst = strings.Contains(out.String(), "makeit")
		return fakeGather(context.Background())
	}
	if code := run(context.Background(), nil, &out, &errb, gather); code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out.String(), errb.String())
	}
	s := out.String()
	if !headerFirst {
		t.Error("the checks ran before the header was printed")
	}
	i, j, k := strings.Index(s, "checking"), strings.Index(s, "\x1b[1A\x1b[2K"), strings.Index(s, "not set up")
	if i < 0 || j < i || k < j {
		t.Errorf("want checking, then the erase sequence, then the status; got %d %d %d:\n%q", i, j, k, s)
	}
}

// A gather error in plain mode: the error on stderr, exit 1, and nothing on stdout, as before.
func TestHomeGatherErrorPlain(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	code, out, errOut := call(t, func(context.Context) (doctor.Machine, error) { return doctor.Machine{}, errors.New("no state dir") })
	if code != 1 || out != "" || !strings.Contains(errOut, "no state dir") {
		t.Errorf("exit %d stdout %q stderr %q", code, out, errOut)
	}
}
