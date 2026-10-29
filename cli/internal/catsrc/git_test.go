package catsrc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"makeit/internal/catalog"
	"makeit/internal/execx"
)

var local = Git{Run: execx.Run, Protocols: "file"}

// sh runs git in dir for a fixture.
func sh(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// catalogFiles is a minimal catalog with the makeit marketplace and bundle.
func catalogFiles(version string) map[string]string {
	return map[string]string{
		"makeit.json":                     `{"schemaVersion": 1, "marketplace": "makeit", "bundle": "makeit-bundle@makeit"}`,
		".claude-plugin/marketplace.json": `{"name": "makeit", "owner": {"name": "t"}, "plugins": []}`,
		"templates/a.md":                  "a " + version + "\n",
		"README.md":                       "not part of the catalog\n",
	}
}

// fixture is a bare repository at file://<dir>/cat.git whose main has the catalog at v1.0.0 (an
// annotated tag); work is its work tree, for more commits.
type fixture struct{ work, bare, url string }

func newFixture(t *testing.T) fixture {
	t.Helper()
	root := t.TempDir()
	f := fixture{work: filepath.Join(root, "work"), bare: filepath.Join(root, "cat.git")}
	f.url = "file://" + f.bare
	if err := os.MkdirAll(f.work, 0o755); err != nil {
		t.Fatal(err)
	}
	sh(t, f.work, "init", "-q")
	f.commit(t, "1.0.0", catalogFiles("1.0.0"))
	sh(t, f.work, "tag", "-a", "v1.0.0", "-m", "v1.0.0")
	sh(t, root, "clone", "-q", "--bare", f.work, f.bare)
	return f
}

// commit writes files into the work tree and commits them; it returns the commit.
func (f fixture) commit(t *testing.T, msg string, files map[string]string) string {
	t.Helper()
	for p, body := range files {
		full := filepath.Join(f.work, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sh(t, f.work, "add", "-A")
	sh(t, f.work, "commit", "-q", "-m", msg)
	return sh(t, f.work, "rev-parse", "HEAD")
}

// push sends main and every tag to the bare repository.
func (f fixture) push(t *testing.T) {
	t.Helper()
	sh(t, f.work, "push", "-q", "-f", "--tags", f.bare, "main")
}

func TestLsRemote(t *testing.T) {
	f := newFixture(t)
	a := sh(t, f.work, "rev-parse", "HEAD")
	b := f.commit(t, "two", map[string]string{"templates/a.md": "b\n"})
	sh(t, f.work, "tag", "light")
	f.push(t)
	refs, err := LsRemote(context.Background(), local, f.url)
	if err != nil {
		t.Fatal(err)
	}
	if refs.Tags["v1.0.0"] != a || refs.Tags["light"] != b || refs.Heads["main"] != b {
		t.Errorf("refs %+v (a %s, b %s)", refs, a, b)
	}
}

func TestFetchTag(t *testing.T) {
	f := newFixture(t)
	commit := sh(t, f.work, "rev-parse", "HEAD")
	tree, err := Fetch(context.Background(), local, f.url, "refs/tags/v1.0.0", commit)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for p, body := range catalogFiles("1.0.0") {
		files[p] = []byte(body)
	}
	want, err := catalog.TreeOf(files, nil)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Digest(tree.Manifest) != catalog.Digest(want.Manifest) {
		t.Errorf("fetched %+v, want %+v", tree.Manifest.Files, want.Manifest.Files)
	}
	if _, err := tree.ReadFile("README.md"); err == nil {
		t.Error("README.md was taken")
	}
}

// The delegate's test: a server that does not allow fetching a commit by its id. Protocol v2 lets
// a client want any object, so only a server that speaks v0 or v1 refuses; forcing v0 here gives
// that exchange.
func TestFetchCommitRefused(t *testing.T) {
	global := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(global, []byte("[protocol]\n\tversion = 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	f := newFixture(t)
	old := sh(t, f.work, "rev-parse", "HEAD")
	f.commit(t, "two", map[string]string{"templates/a.md": "b\n"})
	f.push(t)
	sh(t, f.bare, "config", "uploadpack.allowReachableSHA1InWant", "false")
	_, err := Fetch(context.Background(), local, f.url, old, old)
	if err == nil || !strings.Contains(err.Error(), "does not allow fetching a commit by its id") || !strings.Contains(err.Error(), "pin a tag") {
		t.Fatalf("err %v, want the fetch-by-commit refusal", err)
	}
	sh(t, f.bare, "config", "uploadpack.allowReachableSHA1InWant", "true")
	if _, err := Fetch(context.Background(), local, f.url, old, old); err != nil {
		t.Errorf("with the server allowing it: %v", err)
	}
}

// The tag points to its first commit, while the resolved commit is a later, real one: only the
// check after the fetch stands between them.
func TestFetchMismatch(t *testing.T) {
	f := newFixture(t)
	later := f.commit(t, "two", map[string]string{"templates/a.md": "b\n"})
	f.push(t)
	if _, err := Fetch(context.Background(), local, f.url, "refs/tags/v1.0.0", later); err == nil || !strings.Contains(err.Error(), "not the resolved") {
		t.Errorf("err %v: a ref that moved between resolve and fetch was taken", err)
	}
}

func TestFetchRefusesSymlinkAndCap(t *testing.T) {
	f := newFixture(t)
	if err := os.Symlink("/etc/passwd", filepath.Join(f.work, "templates", "link")); err != nil {
		t.Fatal(err)
	}
	link := f.commit(t, "link", nil)
	f.push(t)
	if _, err := Fetch(context.Background(), local, f.url, "refs/heads/main", link); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("symlink: %v", err)
	}
	g := newFixture(t)
	saved := catalog.MaxTree
	catalog.MaxTree = 10
	t.Cleanup(func() { catalog.MaxTree = saved })
	if _, err := Fetch(context.Background(), local, g.url, "refs/tags/v1.0.0", sh(t, g.work, "rev-parse", "HEAD")); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("cap: %v", err)
	}
}

// Hooks never run: the user's global config points core.hooksPath at a reference-transaction hook
// that would write a file; the fetch's own -c override wins.
func TestFetchRunsNoHooks(t *testing.T) {
	f := newFixture(t)
	hooks, mark := t.TempDir(), filepath.Join(t.TempDir(), "hook-ran")
	if err := os.WriteFile(filepath.Join(hooks, "reference-transaction"), []byte("#!/bin/sh\ntouch "+mark+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	global := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(global, []byte("[core]\n\thooksPath = "+hooks+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	if _, err := Fetch(context.Background(), local, f.url, "refs/tags/v1.0.0", sh(t, f.work, "rev-parse", "HEAD")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(mark); err == nil {
		t.Error("a hook ran during the fetch")
	}
}

func hexOf(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }
