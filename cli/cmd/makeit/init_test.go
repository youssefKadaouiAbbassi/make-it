package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// initRepo is a git repository with a node and a Go manifest whose only library is a Go module,
// so the docs step needs no network (pkg.go.dev URLs are built, not looked up).
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	write(t, filepath.Join(dir, "package.json"), "{\n  \"name\": \"shop\"\n}\n")
	write(t, filepath.Join(dir, "go.mod"), "module x\n\ngo 1.27.3\n\nrequire golang.org/x/term v0.47.0\n")
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
	for _, k := range []string{"version", "plan", "manual", "notices", "docs", "kept", "summary", "changes", "keep"} {
		if _, ok := got[k]; !ok {
			t.Errorf("JSON lacks %q: %s", k, out)
		}
	}
	if !strings.Contains(string(got["docs"]), "indexed golang.org/x/term v0.47.0") || strings.Contains(string(got["docs"]), "go 1.27.3") {
		t.Errorf("docs %s", got["docs"])
	}
	if c := m.take(); len(c) != 2 || !strings.Contains(c[1], "scrape golang.org/x/term https://pkg.go.dev/golang.org/x/term@v0.47.0") {
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

func TestInitOffline(t *testing.T) {
	m := newDocsMachine(t)
	dir := initRepo(t)
	code, out, errOut := run2(t, "init", "--json", "--yes", "--offline", dir)
	if code != 0 || !strings.Contains(out, "--offline: docs not synced") {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	if c := m.take(); len(c) != 0 {
		t.Errorf("--offline ran the docs server: %q", c)
	}
	if code, out, _ := run2(t, "init", "--json", "--yes", "--offline", "--rulesets", dir); code != 1 || !strings.Contains(out, "--rulesets needs the network; drop --offline") {
		t.Errorf("--offline --rulesets: exit %d %s", code, out)
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
	if code, _, errOut := run2(t, "init", "--yes", busy); code != 0 || !strings.Contains(errOut, "no git repository") {
		t.Errorf("non-empty dir without git: exit %d %q", code, errOut)
	}
	if code, out, _ := run2(t, "init", "--help"); code != 0 || !strings.Contains(out, "--update") || !strings.Contains(out, "--rulesets") {
		t.Errorf("help: %d %q", code, out)
	}
}

// The manual block prints when the run changed something or its lines are new since the record; a
// re-run with nothing to do points at it in one line; --dry-run always prints it.
func TestInitManualOnce(t *testing.T) {
	newDocsMachine(t)
	dir := initRepo(t)
	const block = "for /makeit:init to apply:"
	if code, _, errOut := run2(t, "init", "--yes", dir); code != 0 || !strings.Contains(errOut, block) {
		t.Fatalf("first run: exit %d\n%s", code, errOut)
	}
	again := func(name string, wantBlock bool) {
		t.Helper()
		code, _, errOut := run2(t, "init", "--yes", dir)
		if code != 0 || strings.Contains(errOut, block) != wantBlock {
			t.Errorf("%s: exit %d, block printed %v, want %v\n%s", name, code, !wantBlock, wantBlock, errOut)
		}
		if !wantBlock && !strings.Contains(errOut, "still open: makeit init --dry-run shows") {
			t.Errorf("%s: no pointer line\n%s", name, errOut)
		}
	}
	again("re-run", false)
	if _, _, errOut := run2(t, "init", "--dry-run", dir); !strings.Contains(errOut, block) {
		t.Errorf("--dry-run hid the block\n%s", errOut)
	}
	rec := filepath.Join(dir, ".makeit.json")
	b, err := os.ReadFile(rec)
	if err != nil {
		t.Fatal(err)
	}
	stale := regexp.MustCompile(`"manualSha256": "[^"]*"`).ReplaceAll(b, []byte(`"manualSha256": "sha256:old"`))
	if string(stale) == string(b) {
		t.Fatalf("no manualSha256 in the record:\n%s", b)
	}
	write(t, rec, string(stale))
	again("manual lines changed", true)
	again("re-run after the change", false)
}

func TestInitApplied(t *testing.T) {
	newDocsMachine(t)
	dir := initRepo(t)
	if code, out, errOut := run2(t, "init", "--json", "--yes", dir); code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	if code, out, errOut := run2(t, "init", "--applied", "--keep", "CLAUDE.md: Gotchas", dir); code != 0 {
		t.Fatalf("--applied: exit %d\n%s\n%s", code, out, errOut)
	}
	code, out, _ := run2(t, "init", "--json", "--yes", dir)
	var got struct {
		Changes []json.RawMessage
		Keep    []string
	}
	if err := json.Unmarshal([]byte(out), &got); code != 0 || err != nil {
		t.Fatalf("exit %d %v %s", code, err, out)
	}
	if got.Changes == nil || len(got.Changes) != 0 || len(got.Keep) != 1 || got.Keep[0] != "CLAUDE.md: Gotchas" {
		t.Errorf("after --applied: changes %s keep %q", got.Changes, got.Keep)
	}
}
