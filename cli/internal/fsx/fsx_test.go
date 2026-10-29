package fsx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteAtomicReplacesAndLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "settings.json")
	if err := WriteAtomic(p, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(p, []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "two" {
		t.Fatalf("content %q", b)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Fatalf("leftovers: %v", entries)
	}
}

func TestWriteAtomicFailsIntoMissingParentUnderFile(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(filepath.Join(blocker, "x"), []byte("x"), 0o600); err == nil {
		t.Fatal("want an error writing under a regular file")
	}
}

func TestReadOptionalAndHash(t *testing.T) {
	dir := t.TempDir()
	if b, ok, err := ReadOptional(filepath.Join(dir, "none")); b != nil || ok || err != nil {
		t.Fatalf("missing file: %v %v %v", b, ok, err)
	}
	if h, err := FileSha256(filepath.Join(dir, "none")); h != "" || err != nil {
		t.Fatalf("missing hash: %q %v", h, err)
	}
	p := filepath.Join(dir, "f")
	_ = os.WriteFile(p, []byte("abc"), 0o600)
	h, _ := FileSha256(p)
	if h != Sha256([]byte("abc")) || h != "sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("hash %s", h)
	}
}

func TestLock(t *testing.T) {
	p := filepath.Join(t.TempDir(), "lock")
	now := time.Unix(1_800_000_000, 0)
	l, err := Acquire(p, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(p, now.Add(time.Minute)); err == nil || !strings.Contains(err.Error(), "another makeit run") {
		t.Fatalf("second acquire: %v", err)
	}
	stale, err := Acquire(p, now.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("stale lock not taken over: %v", err)
	}
	stale.Release()
	l.Release() // already gone: must not fail
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("lock file left behind")
	}
}
