package catalog

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"makeit/internal/catalog/allowlist"
	"makeit/internal/fsx"
)

// MaxTree caps a catalog read from a directory or a git commit; the embedded one is far smaller.
var MaxTree int64 = 64 << 20

// Tree is a catalog read into memory: what a sync writes and what a run plans with.
type Tree struct {
	Manifest Manifest
	Files    map[string][]byte
}

// ReadFile returns a catalog file, so a Tree is a Source.
func (t *Tree) ReadFile(p string) ([]byte, error) {
	b, ok := t.Files[p]
	if !ok {
		return nil, fmt.Errorf("catalog has no %s: %w", p, fs.ErrNotExist)
	}
	return b, nil
}

// TreeOf builds a tree from files, keeping only allow-listed paths; exec marks the files that get
// mode 0755. Their total size is capped at MaxTree.
func TreeOf(files map[string][]byte, exec map[string]bool) (*Tree, error) {
	t := &Tree{Manifest: Manifest{SchemaVersion: 1}, Files: map[string][]byte{}}
	var total int64
	for p, b := range files {
		if !allowlist.Allowed(p) {
			continue
		}
		if total += int64(len(b)); total > MaxTree {
			return nil, fmt.Errorf("catalog is larger than %d bytes", MaxTree)
		}
		mode := "0644"
		if exec[p] {
			mode = "0755"
		}
		t.Files[p] = b
		t.Manifest.Files = append(t.Manifest.Files, Entry{Path: p, Size: int64(len(b)), Sha256: fsx.Sha256(b), Mode: mode})
	}
	sort.Slice(t.Manifest.Files, func(i, j int) bool { return t.Manifest.Files[i].Path < t.Manifest.Files[j].Path })
	return t, nil
}

// EmbeddedTree is the catalog compiled into this binary.
func EmbeddedTree() (*Tree, error) {
	m, err := EmbeddedManifest()
	if err != nil {
		return nil, err
	}
	t := &Tree{Manifest: m, Files: map[string][]byte{}}
	for _, e := range m.Files {
		if t.Files[e.Path], err = Embedded.ReadFile(e.Path); err != nil {
			return nil, err
		}
	}
	return t, nil
}

// DirTree reads the allow-listed files under dir. A symlink there is refused: a catalog is files.
func DirTree(dir string) (*Tree, error) {
	files, exec := map[string][]byte{}, map[string]bool{}
	var total int64
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil || rel == "." {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if !allowlist.Allowed(rel+"/") && !prefixOfAllowed(rel+"/") {
				return filepath.SkipDir
			}
			return nil
		}
		if !allowlist.Allowed(rel) {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("catalog %s has a symlink: %s", dir, rel)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("catalog %s has a file that is not a regular file: %s", dir, rel)
		}
		if total += info.Size(); total > MaxTree {
			return fmt.Errorf("catalog is larger than %d bytes", MaxTree)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files[rel], exec[rel] = b, info.Mode().Perm()&0o111 != 0
		return nil
	})
	if err != nil {
		return nil, err
	}
	return TreeOf(files, exec)
}

// prefixOfAllowed reports whether dir (with a trailing slash) leads to an allow-listed path.
func prefixOfAllowed(dir string) bool {
	for _, a := range allowlist.Paths {
		if len(a) > len(dir) && a[:len(dir)] == dir {
			return true
		}
	}
	return false
}

// UpToDateTree reports whether dir holds t: its marker names t's digest.
func UpToDateTree(dir string, t *Tree) bool { return ReadMarker(dir).Digest == Digest(t.Manifest) }

// SyncTree writes t to dir. It builds the new tree beside dir and swaps it in, so a Claude Code
// session never loads a half-written catalog. Where the swap fails (Windows, while a hook holds a
// file open), it falls back to replacing changed files one by one and removing only files the
// previous sync wrote; the marker goes last either way.
func SyncTree(dir string, t *Tree) (int, error) {
	next := dir + ".next"
	old := dir + ".old"
	_ = os.RemoveAll(next)
	_ = os.RemoveAll(old)
	if err := writeTree(next, t); err != nil {
		_ = os.RemoveAll(next)
		return 0, err
	}
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		if err := os.Rename(next, dir); err != nil {
			return 0, err
		}
		return len(t.Manifest.Files), nil
	}
	if err := os.Rename(dir, old); err == nil {
		if err := os.Rename(next, dir); err != nil {
			_ = os.Rename(old, dir)
			_ = os.RemoveAll(next)
			return 0, err
		}
		_ = os.RemoveAll(old)
		return len(t.Manifest.Files), nil
	}
	_ = os.RemoveAll(next)
	return syncInPlace(dir, t)
}
