package ui

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestDecideMode(t *testing.T) {
	env := func(kv ...string) func(string) string {
		m := map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return func(k string) string { return m[k] }
	}
	for _, c := range []struct {
		name           string
		stdout, stderr bool
		getenv         func(string) string
		json, plain    bool
		want           Mode
	}{
		{"terminal", true, true, env(), false, false, Live},
		{"stdout piped", false, true, env(), false, false, Plain},
		{"stderr piped", true, false, env(), false, false, Plain},
		{"NO_COLOR", true, true, env("NO_COLOR", "1"), false, false, Plain},
		{"TERM=dumb", true, true, env("TERM", "dumb"), false, false, Plain},
		{"CI", true, true, env("CI", "true"), false, false, Plain},
		{"json", true, true, env(), true, false, Plain},
		{"plain flag", true, true, env(), false, true, Plain},
	} {
		if got := DecideMode(c.stdout, c.stderr, c.getenv, c.json, c.plain); got != c.want {
			t.Errorf("%s: mode %v, want %v", c.name, got, c.want)
		}
	}
}

func TestGroup(t *testing.T) {
	for id, want := range map[string]string{
		"prereq.git":                       "Machine",
		"claude":                           "Claude Code",
		"catalog":                          "Plugins",
		"marketplace.official":             "Plugins",
		"bundle.deps":                      "Plugins",
		"plugin.update.superpowers@makeit": "Plugins",
		"plugins.adopted":                  "Plugins",
		"settings":                         "Settings",
		"global.CLAUDE.md":                 "Files",
		"self.rollback":                    "makeit",
	} {
		if got := Group(id, "title"); got != want {
			t.Errorf("Group(%q) = %q, want %q", id, got, want)
		}
	}
	if got := Group("docs.sync", "Index the docs"); got != "Index the docs" {
		t.Errorf("unknown step grouped as %q, want its title", got)
	}
}

func TestRowPlain(t *testing.T) {
	var out bytes.Buffer
	u := NewFromReader(nil, &out, false)
	u.Row(GDone, "Plugins", "12 installed", "")
	u.Row(GChange, "Plugins", "install 12", "")
	u.Row(GWarn, "gopls", "missing", "Go code intelligence")
	u.Row(GFail, "Settings", "not written", "")
	want := "ok Plugins 12 installed\n" +
		"  will: Plugins install 12\n" +
		"!! gopls missing · Go code intelligence\n" +
		"xx Settings not written\n"
	if out.String() != want {
		t.Errorf("plain rows:\n%q\nwant\n%q", out.String(), want)
	}
}

func TestRowLiveNoColor(t *testing.T) {
	var out bytes.Buffer
	u := NewFromReader(nil, &out, false)
	u.Mode = Live
	u.Row(GDone, "Plugins", "12 installed", "")
	u.Row(GPending, "Files", "", "")
	want := "  ✓ Plugins        12 installed\n  · Files\n"
	if out.String() != want {
		t.Errorf("live rows:\n%q\nwant\n%q", out.String(), want)
	}
}

func TestFitNarrow(t *testing.T) {
	got := fit("chrome-devtools-mcp 1.12.0 → 1.13.0", 20)
	if w := lipgloss.Width(got); w != 20 {
		t.Errorf("width %d, want 20: %q", w, got)
	}
	if got[len(got)-len("…"):] != "…" {
		t.Errorf("no ellipsis: %q", got)
	}
	if s := fit("short", 20); s != "short" {
		t.Errorf("short text changed: %q", s)
	}
}

func TestResultLine(t *testing.T) {
	var out bytes.Buffer
	u := NewFromReader(nil, &out, false)
	u.Mode = Live
	u.Result(true, "Updated", 4*time.Second, []string{"makeit and 2 plugins moved to new releases"})
	u.Next("Restart Claude Code to load them.", "undo: makeit rollback")
	want := "\n  ✓ Updated in 4s · makeit and 2 plugins moved to new releases\n" +
		"    Restart Claude Code to load them.  undo: makeit rollback\n"
	if out.String() != want {
		t.Errorf("result:\n%q\nwant\n%q", out.String(), want)
	}
}

func TestHeader(t *testing.T) {
	var out bytes.Buffer
	u := NewFromReader(nil, &out, false)
	u.Mode = Live
	u.Header("update")
	if out.String() != "\n>_ makeit  update\n\n" {
		t.Errorf("header %q", out.String())
	}
	out.Reset()
	u.Mode = Plain
	u.Header("update")
	if out.String() != "" {
		t.Errorf("plain mode printed a header: %q", out.String())
	}
}

// A label longer than its column still leaves a space before the detail.
func TestRowLongLabelKeepsASpace(t *testing.T) {
	u := liveUI()
	if row := u.RowText(GDone, "a label far longer than the column", "up to date", ""); !strings.Contains(row, "column up to date") {
		t.Errorf("row %q", row)
	}
}

// Nothing wraps: rows and card lines are cut to the terminal width; a fix wraps under itself.
func TestLinesFitTheTerminal(t *testing.T) {
	var out bytes.Buffer
	u := NewFromReader(nil, &out, false)
	u.Mode, u.Width = Live, 40
	u.Row(GWarn, "gh is missing: branch rulesets and the sha-pinning setting are applied through the GitHub API", "", "")
	u.KV("7 to fix", "gh is missing: branch rulesets and the sha-pinning setting are applied", true)
	u.Fix("go install github.com/vshulcz/deja-vu/cmd/deja@latest, or a release binary from github.com/vshulcz/deja-vu")
	ls := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	for _, l := range ls {
		if w := ansi.StringWidth(l); w > 40 {
			t.Errorf("line of %d cells: %q", w, l)
		}
	}
	if !strings.HasSuffix(ls[0], "…") || !strings.HasSuffix(ls[1], "…") {
		t.Errorf("cut lines do not end in …: %q", ls[:2])
	}
	if len(ls) < 4 || !strings.HasPrefix(ls[3], "        ") || !strings.Contains(out.String(), "deja-vu") {
		t.Errorf("the fix does not wrap under itself:\n%s", out.String())
	}
}
