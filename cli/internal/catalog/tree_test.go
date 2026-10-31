package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDirTreeMatchesEmbedded(t *testing.T) {
	emb, err := EmbeddedTree()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "catalog")
	if _, err := SyncTree(dir, emb); err != nil {
		t.Fatal(err)
	}
	got, err := DirTree(dir)
	if err != nil {
		t.Fatal(err)
	}
	m, err := EmbeddedManifest()
	if err != nil {
		t.Fatal(err)
	}
	if Digest(got.Manifest) != Digest(m) || Digest(emb.Manifest) != Digest(m) {
		t.Errorf("digests: dir %s, embedded tree %s, manifest %s", Digest(got.Manifest), Digest(emb.Manifest), Digest(m))
	}
}

func TestTreeOfAllowlist(t *testing.T) {
	tr, err := TreeOf(map[string][]byte{"README.md": []byte("x"), "makeit.json": []byte("{}"), "plugins/x/run.sh": []byte("#!/bin/sh\n")},
		map[string]bool{"plugins/x/run.sh": true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tr.ReadFile("README.md"); err == nil {
		t.Error("README.md was taken")
	}
	modes := map[string]string{}
	for _, e := range tr.Manifest.Files {
		modes[e.Path] = e.Mode
	}
	if len(modes) != 2 || modes["plugins/x/run.sh"] != "0755" || modes["makeit.json"] != "0644" {
		t.Errorf("manifest %+v", tr.Manifest.Files)
	}
}

func TestTreeCap(t *testing.T) {
	saved := MaxTree
	MaxTree = 10
	t.Cleanup(func() { MaxTree = saved })
	if _, err := TreeOf(map[string][]byte{"makeit.json": []byte("01234567890")}, nil); err == nil || !strings.Contains(err.Error(), "10 bytes") {
		t.Errorf("err %v, want one naming the cap", err)
	}
}

func TestDirTreeRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "makeit.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "templates", "x")); err != nil {
		t.Fatal(err)
	}
	if _, err := DirTree(dir); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("err %v, want a symlink refusal", err)
	}
}

func TestSyncTreeUpToDate(t *testing.T) {
	tr, err := TreeOf(map[string][]byte{"makeit.json": []byte("{}"), "templates/a": []byte("a")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "catalog")
	if n, err := SyncTree(dir, tr); err != nil || n != 2 {
		t.Fatalf("synced %d, %v", n, err)
	}
	if !UpToDateTree(dir, tr) {
		t.Error("not up to date after the sync")
	}
	changed, err := TreeOf(map[string][]byte{"makeit.json": []byte("{}"), "templates/a": []byte("b")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if UpToDateTree(dir, changed) {
		t.Error("up to date with a changed tree")
	}
}

// A path that climbs out of the catalog, or that Windows would read as one, is refused.
func TestTreeOfRefusesUnsafePaths(t *testing.T) {
	for _, p := range []string{"plugins/../../evil", "templates/../../../x", "plugins/./x", "plugins//x", "plugins/x/",
		`plugins/a\..\..\x`, "plugins/c:x"} {
		if _, err := TreeOf(map[string][]byte{p: []byte("x")}, nil); err == nil {
			t.Errorf("%q was taken", p)
		}
	}
}
