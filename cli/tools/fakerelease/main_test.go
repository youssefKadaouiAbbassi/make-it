package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"io/fs"
	"net/http/httptest"
	"os"
	"os/exec"
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

func TestServeGitClones(t *testing.T) {
	root, work := t.TempDir(), t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "init.defaultBranch=main"}, args...)...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(work, "makeit.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(work, "init", "-q")
	run(work, "add", "-A")
	run(work, "commit", "-q", "-m", "one")
	run(root, "clone", "-q", "--bare", work, filepath.Join(root, "cat.git"))
	h, err := gitHandler(root)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewTLSServer(h)
	defer srv.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "clone")
	cmd := exec.Command("git", "clone", "-q", "--depth", "1", srv.URL+"/cat.git", dst)
	cmd.Env = append(os.Environ(), "GIT_SSL_CAINFO="+ca)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dst, "makeit.json")); err != nil {
		t.Errorf("clone has no makeit.json: %v", err)
	}
}
