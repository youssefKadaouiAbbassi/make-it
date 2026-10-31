package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// initRepo is a git repository with a node and a Go manifest whose only library is the Go
// standard library, so the docs step needs no network.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	write(t, filepath.Join(dir, "package.json"), "{\n  \"name\": \"shop\"\n}\n")
	write(t, filepath.Join(dir, "go.mod"), "module x\n\ngo 1.27.3\n")
	return dir
}

func TestInitJSON(t *testing.T) {
	m := newDocsMachine(t)
	dir := initRepo(t)
	code, out, errOut := run2(t, "init", "--json", "--yes", dir)
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not one JSON object: %v\n%s", err, out)
	}
	for _, k := range []string{"version", "plan", "manual", "notices", "docs", "kept", "summary"} {
		if _, ok := got[k]; !ok {
			t.Errorf("JSON lacks %q: %s", k, out)
		}
	}
	if !strings.Contains(string(got["docs"]), "indexed go 1.27.3") {
		t.Errorf("docs %s", got["docs"])
	}
	if c := m.take(); len(c) != 2 || !strings.Contains(c[1], "scrape go https://pkg.go.dev/std@go1.27.3") {
		t.Errorf("docs calls %q", c)
	}
	for _, f := range []string{"CLAUDE.md", "CONSTRAINTS.md", "HANDOFF.md", ".gitignore", ".makeit.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	code, out, _ = run2(t, "init", "--json", "--yes", dir)
	var second struct {
		Plan []struct{ ID, Status string }
	}
	if err := json.Unmarshal([]byte(out), &second); code != 0 || err != nil || len(second.Plan) == 0 {
		t.Fatalf("second run: exit %d %v %s", code, err, out)
	}
	for _, o := range second.Plan {
		if o.Status != "already" {
			t.Errorf("second run: %s is %s", o.ID, o.Status)
		}
	}
}

func TestInitConsentAndUsage(t *testing.T) {
	newDocsMachine(t)
	dir := initRepo(t)
	if code, _, errOut := run2(t, "init", dir); code != 2 || !strings.Contains(errOut, "stdin is not a terminal") {
		t.Errorf("no terminal, no --yes: exit %d %q", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Error("written without consent")
	}
	if code, _, _ := run2(t, "init", "a", "b"); code != 2 {
		t.Errorf("two dirs: exit %d", code)
	}
	busy := t.TempDir()
	write(t, filepath.Join(busy, "main.go"), "package main\n")
	if code, _, errOut := run2(t, "init", "--yes", busy); code != 1 || !strings.Contains(errOut, "not a git repository") {
		t.Errorf("non-empty dir without git: exit %d %q", code, errOut)
	}
	if code, out, _ := run2(t, "init", "--help"); code != 0 || !strings.Contains(out, "--update") || !strings.Contains(out, "--rulesets") {
		t.Errorf("help: %d %q", code, out)
	}
}
