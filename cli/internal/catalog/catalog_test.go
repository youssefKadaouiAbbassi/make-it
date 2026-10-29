package catalog

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// The embedded set must equal what synccatalog would produce from the live tree now.
func TestEmbeddedMatchesTree(t *testing.T) {
	repo, _ := filepath.Abs("../../..")
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		t.Skip("not inside the catalog repository")
	}
	m, err := EmbeddedManifest()
	if err != nil {
		t.Fatal(err)
	}
	var live Manifest
	tmp := t.TempDir()
	if out, err := exec.Command("go", "run", "../../tools/synccatalog", "-repo", repo, "-out", tmp).CombinedOutput(); err != nil {
		t.Fatalf("synccatalog: %v\n%s", err, out)
	}
	b, _ := os.ReadFile(filepath.Join(tmp, "MANIFEST.json"))
	if err := json.Unmarshal(b, &live); err != nil {
		t.Fatal(err)
	}
	if Digest(live) != Digest(m) {
		t.Fatalf("embedded catalog is stale (%d files embedded, %d in the tree): run go generate ./...", len(m.Files), len(live.Files))
	}
}

func TestManifestRules(t *testing.T) {
	m, err := EmbeddedManifest()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range m.Files {
		if e.Mode == "0755" && filepath.Ext(e.Path) != ".sh" && filepath.Ext(e.Path) != ".mjs" && filepath.Ext(e.Path) != ".py" {
			t.Errorf("unexpected executable %s", e.Path)
		}
		if filepath.Ext(e.Path) == ".out" {
			t.Errorf("build output embedded: %s", e.Path)
		}
		seen[e.Path] = true
	}
	for _, must := range []string{".claude-plugin/marketplace.json", "makeit.json", "settings.baseline.json", "templates/global/CLAUDE.md"} {
		if !seen[must] {
			t.Errorf("%s missing", must)
		}
	}
	r, err := LoadRequires(Embedded)
	if err != nil || r.Requires.ClaudeCode.Hard == "" || r.Bundle == "" {
		t.Fatalf("requires: %+v %v", r, err)
	}
}

func TestSyncFreshUpdateAndInPlace(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "catalog")
	n, err := SyncTree(dir, embeddedTree(t))
	if err != nil || n == 0 {
		t.Fatalf("sync: %d %v", n, err)
	}
	if ok, _ := upToDate(t, dir); !ok {
		t.Fatal("not up to date after sync")
	}
	if err := Valid(dir); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		st, err := os.Stat(filepath.Join(dir, "plugins", "makeit", "hooks", "ntfy.sh"))
		if err != nil || st.Mode().Perm()&0o111 == 0 {
			t.Fatalf("executable bit lost: %v %v", st, err)
		}
	}
	// A user file and a stale catalog file: the swap replaces the whole tree.
	stale := filepath.Join(dir, "templates", "gone.md")
	_ = os.WriteFile(stale, []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, MarkerName), []byte(`{"digest":"old","files":["templates/gone.md"]}`), 0o644)
	if ok, _ := upToDate(t, dir); ok {
		t.Fatal("old marker read as current")
	}
	if _, err := SyncTree(dir, embeddedTree(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale file kept")
	}
	for _, leftover := range []string{dir + ".next", dir + ".old"} {
		if _, err := os.Stat(leftover); !os.IsNotExist(err) {
			t.Fatalf("%s left behind", leftover)
		}
	}
	// In-place fallback: only files the previous marker listed are removed.
	m := embeddedTree(t)
	mine := filepath.Join(dir, "mine.txt")
	_ = os.WriteFile(mine, []byte("keep"), 0o644)
	_ = os.WriteFile(stale, []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, MarkerName), []byte(`{"digest":"old","files":["templates/gone.md"]}`), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "makeit.json"), []byte("{}"), 0o644)
	n, err = syncInPlace(dir, m)
	if err != nil || n != 1 {
		t.Fatalf("in place: %d %v", n, err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("in place kept a file the old sync wrote")
	}
	if _, err := os.Stat(mine); err != nil {
		t.Fatal("in place removed a file it never wrote")
	}
	if ok, _ := upToDate(t, dir); !ok {
		t.Fatal("in place not up to date")
	}
}

func embeddedTree(t *testing.T) *Tree {
	t.Helper()
	tr, err := EmbeddedTree()
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

// upToDate is the old UpToDate(dir) against the embedded catalog.
func upToDate(t *testing.T, dir string) (bool, error) { return UpToDateTree(dir, embeddedTree(t)), nil }
