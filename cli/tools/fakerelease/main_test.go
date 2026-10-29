package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"makeit/internal/minisign"
)

const seed = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

func TestReleaseVerifies(t *testing.T) {
	dir, bin := t.TempDir(), filepath.Join(t.TempDir(), "makeit")
	if err := os.WriteFile(bin, []byte("makeit 1.1.0 binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := release(seed, "test/makeit", "1.1.0", "linux-x64", bin, dir); err != nil {
		t.Fatal(err)
	}
	pub, err := public(seed)
	if err != nil {
		t.Fatal(err)
	}
	at := filepath.Join(dir, "test", "makeit", "releases", "download", "v1.1.0")
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join(at, name))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	list := read("checksums.txt")
	if c, err := minisign.Verify([]string{pub}, string(read("checksums.txt.minisig")), list); err != nil || c != "makeit 1.1.0 checksums.txt" {
		t.Fatalf("signature: %q %v", c, err)
	}
	sum := sha256.Sum256(read("makeit-1.1.0-linux-x64"))
	if want := hex.EncodeToString(sum[:]) + "  makeit-1.1.0-linux-x64\n"; string(list) != want {
		t.Errorf("checksums.txt %q, want %q", list, want)
	}
}

// The test-only packages never reach a shipped binary: nothing under cmd/ or internal/ imports them
// outside tests, except the two packages themselves.
func TestTestPackagesNotShipped(t *testing.T) {
	helpers := []string{"makeit/internal/minisign/minisigntest", "makeit/internal/selfupdate/releasetest"}
	walked := 0
	for _, root := range []string{"../../cmd", "../../internal"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			dir := filepath.ToSlash(filepath.Dir(path))
			if strings.HasSuffix(dir, "internal/minisign/minisigntest") || strings.HasSuffix(dir, "internal/selfupdate/releasetest") {
				return nil
			}
			walked++
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, h := range helpers {
				if strings.Contains(string(b), `"`+h+`"`) {
					t.Errorf("%s imports %s", path, h)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if walked < 20 {
		t.Fatalf("walked %d files: the walk is looking in the wrong place", walked)
	}
}
