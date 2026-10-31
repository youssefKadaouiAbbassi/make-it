package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"makeit/internal/catalog"
	"makeit/internal/catsrc"
	"makeit/internal/execx"
	"makeit/internal/setup"
	"makeit/internal/state"
)

// catMachine is the round trip's fake-claude machine, with the real git on PATH, and a bare
// repository at url holding the embedded catalog (plus extra files) tagged v1.0.0.
type catMachine struct{ home, stack, work, bare, url string }

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newCatMachine(t *testing.T, extra map[string]string) catMachine {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("a shell script stands in for claude")
	}
	home, bin, stack, fake := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	for _, tool := range []string{"node", "jq"} {
		write(t, filepath.Join(bin, tool), "#!/bin/sh\n")
	}
	write(t, filepath.Join(bin, "claude"), fakeClaude)
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("MAKEIT_DIR", stack)
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	t.Setenv("FAKE_STATE", fake)
	t.Setenv("CALLS", filepath.Join(fake, "calls"))
	saved := catalogGit
	catalogGit = catsrc.Git{Run: execx.Run, Protocols: "file"}
	t.Cleanup(func() { catalogGit = saved })
	emb, err := catalog.EmbeddedTree()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	m := catMachine{home: home, stack: stack, work: filepath.Join(root, "work"), bare: filepath.Join(root, "cat.git")}
	m.url = "file://" + m.bare
	for _, e := range emb.Manifest.Files {
		p := filepath.Join(m.work, filepath.FromSlash(e.Path))
		write(t, p, string(emb.Files[e.Path]))
		mode := os.FileMode(0o644)
		if e.Mode == "0755" {
			mode = 0o755
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
	}
	for p, body := range extra {
		write(t, filepath.Join(m.work, filepath.FromSlash(p)), body)
	}
	gitIn(t, m.work, "init", "-q")
	gitIn(t, m.work, "add", "-A")
	gitIn(t, m.work, "commit", "-q", "-m", "catalog")
	gitIn(t, m.work, "tag", "-a", "v1.0.0", "-m", "v1.0.0")
	gitIn(t, root, "clone", "-q", "--bare", m.work, m.bare)
	return m
}

// release commits a change to the catalog, tags it and pushes it.
func (m catMachine) release(t *testing.T, tag string, files map[string]string, force bool) string {
	t.Helper()
	for p, body := range files {
		write(t, filepath.Join(m.work, filepath.FromSlash(p)), body)
	}
	gitIn(t, m.work, "add", "-A")
	gitIn(t, m.work, "commit", "-q", "-m", tag)
	args := []string{"tag", "-a", tag, "-m", tag}
	if force {
		args = []string{"tag", "-f", "-a", tag, "-m", "moved"}
	}
	gitIn(t, m.work, args...)
	gitIn(t, m.work, "push", "-q", "-f", "--tags", m.bare, "main")
	return gitIn(t, m.work, "rev-parse", "HEAD")
}

func (m catMachine) catalogRecord(t *testing.T) state.Catalog {
	t.Helper()
	st, _, err := state.LoadState(filepath.Join(m.stack, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	return st.Catalog
}

func run2(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	return call(t, noGather(t), args...)
}

func TestSetupCatalogGit(t *testing.T) {
	m := newCatMachine(t, nil)
	commit := gitIn(t, m.work, "rev-parse", "HEAD")
	if code, _, errOut := run2(t, "setup", "--yes", "--catalog", m.url); code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	r := m.catalogRecord(t)
	if r.Kind != "git" || r.Location != m.url || r.Requested == nil || *r.Requested != "latest" || r.Tag != "v1.0.0" || r.Resolved != commit {
		t.Errorf("record %+v", r)
	}
	if !slices.ContainsFunc(calls(t), func(l string) bool {
		return strings.HasPrefix(l, "plugin marketplace add "+filepath.Join(m.stack, "catalog"))
	}) {
		t.Errorf("the marketplace is not the synced directory: %q", calls(t))
	}
}

func TestCatalogSwitchKeepsMarketplace(t *testing.T) {
	m := newCatMachine(t, map[string]string{"templates/extra.md": "extra\n"})
	if code, _, errOut := run2(t, "setup", "--yes"); code != 0 {
		t.Fatalf("setup: exit %d\n%s", code, errOut)
	}
	marker := filepath.Join(m.home, ".claude", "plugins", "data", setup.DataID("superpowers@makeit"), "marker")
	write(t, marker, "keep")
	extra := filepath.Join(m.stack, "catalog", "templates", "extra.md")
	if code, _, errOut := run2(t, "update", "--yes", "--catalog", m.url); code != 0 || m.catalogRecord(t).Kind != "git" {
		t.Fatalf("to git: exit %d, record %+v\n%s", code, m.catalogRecord(t), errOut)
	}
	if _, err := os.Stat(extra); err != nil {
		t.Errorf("the git catalog was not synced: %v", err)
	}
	if code, _, errOut := run2(t, "update", "--yes", "--catalog", "embedded"); code != 0 || m.catalogRecord(t).Kind != "embedded" {
		t.Fatalf("back to embedded: exit %d, record %+v\n%s", code, m.catalogRecord(t), errOut)
	}
	if _, err := os.Stat(extra); err == nil {
		t.Error("the git catalog's extra file stayed after the switch back")
	}
	for _, l := range calls(t) {
		if strings.Contains(l, "marketplace remove") {
			t.Errorf("a switch removed a marketplace: %s", l)
		}
	}
	if content(t, marker) != "keep" {
		t.Error("a plugin's data changed")
	}
}

// Review Focus 2: the same files from a git source still record the switch.
func TestCatalogSwitchIdenticalRecords(t *testing.T) {
	m := newCatMachine(t, nil)
	if code, _, errOut := run2(t, "setup", "--yes"); code != 0 {
		t.Fatalf("setup: exit %d\n%s", code, errOut)
	}
	if code, _, errOut := run2(t, "update", "--yes", "--catalog", m.url); code != 0 {
		t.Fatalf("exit %d\n%s", code, errOut)
	}
	if r := m.catalogRecord(t); r.Kind != "git" || r.Tag != "v1.0.0" {
		t.Errorf("record %+v", r)
	}
}

func TestUpdateCatalogNotices(t *testing.T) {
	m := newCatMachine(t, nil)
	if code, _, errOut := run2(t, "setup", "--yes", "--catalog", m.url+"#v1.0.0"); code != 0 {
		t.Fatalf("setup: exit %d\n%s", code, errOut)
	}
	m.release(t, "v1.1.0", map[string]string{"templates/extra.md": "1.1\n"}, false)
	if code, _, errOut := run2(t, "update", "--yes"); code != 0 || !strings.Contains(errOut, "catalog v1.1.0 is newer than v1.0.0") {
		t.Errorf("newer: exit %d\n%s", code, errOut)
	}
	_, out, _ := run2(t, "update", "--yes", "--json")
	if !strings.Contains(out, "is newer than v1.0.0") {
		t.Errorf("JSON notice: %s", out)
	}
	m.release(t, "v1.0.0", map[string]string{"templates/extra.md": "moved\n"}, true)
	if code, _, errOut := run2(t, "update", "--yes"); code != 0 || !strings.Contains(errOut, "catalog tag v1.0.0 now points to") {
		t.Errorf("moved: exit %d\n%s", code, errOut)
	}
}

func TestUpdateCatalogUnreachable(t *testing.T) {
	m := newCatMachine(t, nil)
	if code, _, errOut := run2(t, "setup", "--yes", "--catalog", m.url); code != 0 {
		t.Fatalf("setup: exit %d\n%s", code, errOut)
	}
	if err := os.RemoveAll(m.bare); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := run2(t, "update", "--yes"); code != 0 || !strings.Contains(errOut, "could not refresh the catalog") {
		t.Errorf("plain: exit %d\n%s", code, errOut)
	}
	if code, _, errOut := run2(t, "update", "--yes", "--catalog", "file:///no/such/cat.git"); code != 1 || !strings.Contains(errOut, credentialHint) {
		t.Errorf("explicit: exit %d, want 1 with the credential hint\n%s", code, errOut)
	}
}

// owner/repo.git with no such local directory is read as GitHub: the full URL is printed before
// the fetch, with --yes and with --json alike, so a mistyped path is visible.
func TestCatalogGuessedGitHubShown(t *testing.T) {
	m := newCatMachine(t, nil)
	if code, _, errOut := run2(t, "setup", "--yes", "--catalog", m.url); code != 0 {
		t.Fatalf("setup: exit %d\n%s", code, errOut)
	}
	const line = "catalog: https://github.com/owner/repo.git (no local path owner/repo.git)"
	code, _, errOut := run2(t, "update", "--yes", "--catalog", "owner/repo.git")
	if i, j := strings.Index(errOut, line), strings.Index(errOut, "could not fetch"); code != 1 || i < 0 || j < 0 || i > j {
		t.Errorf("--yes: exit %d, want the source line before the fetch error\n%s", code, errOut)
	}
	// With --json the error is in the JSON document on stdout; the line stays on stderr.
	code, out, errOut := run2(t, "update", "--yes", "--json", "--catalog", "owner/repo.git")
	if code != 1 || !strings.Contains(errOut, line) || !strings.Contains(out, "could not fetch") {
		t.Errorf("--json: exit %d\nstdout %s\nstderr %s", code, out, errOut)
	}
}

// --json keeps its promise when the catalog cannot be prepared: one JSON document with the error.
func TestCatalogPrepareErrorJSON(t *testing.T) {
	m := newCatMachine(t, nil)
	if code, _, errOut := run2(t, "setup", "--yes", "--catalog", m.url); code != 0 {
		t.Fatalf("setup: exit %d\n%s", code, errOut)
	}
	code, out, _ := run2(t, "update", "--yes", "--json", "--catalog", "file:///no/such/cat.git")
	var got struct {
		Plan  []json.RawMessage `json:"plan"`
		Error string            `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &got); code != 1 || err != nil || got.Plan == nil || !strings.Contains(got.Error, credentialHint) {
		t.Errorf("exit %d, not one JSON document with the error: %v\n%s", code, err, out)
	}
}

const credentialHint = "a private repository needs a git credential helper or an SSH key"

// A deleted ~/.makeit/catalog is fetched again from the recorded git source, not replaced by the
// embedded catalog.
func TestDeletedCatalogKeepsGitSource(t *testing.T) {
	m := newCatMachine(t, map[string]string{"templates/extra.md": "extra\n"})
	if code, _, errOut := run2(t, "setup", "--yes", "--catalog", m.url); code != 0 {
		t.Fatalf("setup: exit %d\n%s", code, errOut)
	}
	if err := os.RemoveAll(filepath.Join(m.stack, "catalog")); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := run2(t, "update", "--yes"); code != 0 {
		t.Fatalf("update: exit %d\n%s", code, errOut)
	}
	if rec := m.catalogRecord(t); rec.Kind != "git" {
		t.Errorf("the git source was forgotten: %+v", rec)
	}
	if _, err := os.Stat(filepath.Join(m.stack, "catalog", "templates", "extra.md")); err != nil {
		t.Errorf("the git catalog was not written back: %v", err)
	}
}

// Review Focus 4: a dry run with --catalog writes nothing and plans with the new catalog.
func TestCatalogDryRunWritesNothing(t *testing.T) {
	m := newCatMachine(t, map[string]string{"templates/extra.md": "extra\n"})
	if code, _, errOut := run2(t, "setup", "--yes"); code != 0 {
		t.Fatalf("setup: exit %d\n%s", code, errOut)
	}
	before := hashes(t, m.stack)
	code, _, errOut := run2(t, "update", "--dry-run", "--catalog", m.url)
	if code != 0 || !strings.Contains(errOut, "Write the catalog to") {
		t.Errorf("exit %d\n%s", code, errOut)
	}
	if after := hashes(t, m.stack); !slices.Equal(before, after) {
		t.Errorf("the dry run changed ~/.makeit:\n%v\n%v", before, after)
	}
}

func TestCatalogAdoptedRefused(t *testing.T) {
	_, stack := handInstalled(t, nil)
	if code, _ := setupYes(t); code != 0 {
		t.Fatalf("setup exit %d", code)
	}
	before := hashes(t, stack)
	code, _, errOut := run2(t, "update", "--yes", "--catalog", "embedded")
	if code != 1 || !strings.Contains(errOut, "nothing was changed") {
		t.Errorf("exit %d\n%s", code, errOut)
	}
	if after := hashes(t, stack); !slices.Equal(before, after) {
		t.Error("the refusal changed ~/.makeit")
	}
}

func TestCatalogFlagsInHelp(t *testing.T) {
	for _, cmd := range []string{"setup", "update"} {
		_, out, _ := run2(t, "help", cmd)
		if !strings.Contains(out, "--catalog") || !strings.Contains(out, "--allow-branch") {
			t.Errorf("help %s lacks the catalog flags:\n%s", cmd, out)
		}
	}
}

// hashes lists every file under dir with its sha256.
func hashes(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		out = append(out, p+" "+hex.EncodeToString(sum[:]))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
