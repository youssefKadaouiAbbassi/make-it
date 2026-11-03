package tools

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"makeit/internal/platform"
)

func sha(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func tarGz(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, data := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write(data)
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func zipped(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(data)
	}
	_ = zw.Close()
	return buf.Bytes()
}

// release serves files under /o/r/releases/download/v1.0.0/.
func release(t *testing.T, files map[string][]byte) (*httptest.Server, platform.Release) {
	t.Helper()
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := files[strings.TrimPrefix(r.URL.Path, "/o/r/releases/download/v1.0.0/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(s.Close)
	r := platform.Release{Repo: "o/r", Version: "1.0.0", Bin: "tool", Asset: func(v, goos, goarch string) (string, string) {
		if goarch == "riscv64" {
			return "", ""
		}
		if goos == "windows" {
			return "tool.zip", "sums.txt"
		}
		return "tool.tar.gz", "sums.txt"
	}}
	return s, r
}

func TestVerifyChecksumFormats(t *testing.T) {
	bin := []byte("binary")
	for _, tc := range []struct {
		name, sums string
		wantErr    error
	}{
		{"one line, prek style", sha(bin) + "  tool.tar.gz\n", nil},
		{"bare hash", sha(bin) + "\n", nil},
		{"many lines, goreleaser style", sha([]byte("other")) + "  other.tar.gz\n" + sha(bin) + "  tool.tar.gz\n", nil},
		{"star marker", sha(bin) + " *tool.tar.gz\n", nil},
		{"asset missing from the list", sha([]byte("other")) + "  other.tar.gz\n", ErrChecksum},
		{"garbage", "not a checksum file\n", ErrChecksum},
		{"wrong hash", sha([]byte("tampered")) + "  tool.tar.gz\n", ErrChecksum},
	} {
		err := VerifyChecksum([]byte(tc.sums), "tool.tar.gz", sha(bin))
		if !errors.Is(err, tc.wantErr) {
			t.Errorf("%s: err %v, want %v", tc.name, err, tc.wantErr)
		}
	}
}

func TestInstallReleaseTarGz(t *testing.T) {
	bin := []byte("#!/bin/sh\necho tool\n")
	archive := tarGz(t, map[string][]byte{"tool-1.0.0-linux/README": []byte("x"), "tool-1.0.0-linux/tool": bin})
	s, r := release(t, map[string][]byte{"tool.tar.gz": archive, "sums.txt": []byte(sha(archive) + "  tool.tar.gz\n")})
	dir := t.TempDir()
	path, err := InstallRelease(context.Background(), s.Client(), "makeit/test", s.URL, r, "linux", "amd64", dir)
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(dir, "tool") {
		t.Errorf("path %q", path)
	}
	got, _ := os.ReadFile(path)
	fi, _ := os.Stat(path)
	if !bytes.Equal(got, bin) || fi.Mode().Perm() != 0o755 {
		t.Errorf("content %q mode %v", got, fi.Mode())
	}
}

func TestInstallReleaseZip(t *testing.T) {
	bin := []byte("MZ...")
	archive := zipped(t, map[string][]byte{"tool.exe": bin})
	s, r := release(t, map[string][]byte{"tool.zip": archive, "sums.txt": []byte(sha(archive) + "  tool.zip\n")})
	dir := t.TempDir()
	path, err := InstallRelease(context.Background(), s.Client(), "makeit/test", s.URL, r, "windows", "amd64", dir)
	if err != nil || path != filepath.Join(dir, "tool.exe") {
		t.Fatalf("%q %v", path, err)
	}
}

func TestInstallReleaseMismatchWritesNothing(t *testing.T) {
	archive := tarGz(t, map[string][]byte{"tool": []byte("x")})
	s, r := release(t, map[string][]byte{"tool.tar.gz": archive, "sums.txt": []byte(sha([]byte("other")) + "  tool.tar.gz\n")})
	dir := t.TempDir()
	if _, err := InstallRelease(context.Background(), s.Client(), "makeit/test", s.URL, r, "linux", "amd64", dir); !errors.Is(err, ErrChecksum) {
		t.Fatalf("err %v", err)
	}
	if es, _ := os.ReadDir(dir); len(es) != 0 {
		t.Errorf("wrote %v", es)
	}
}

func TestInstallReleaseNoAsset(t *testing.T) {
	s, r := release(t, nil)
	if _, err := InstallRelease(context.Background(), s.Client(), "makeit/test", s.URL, r, "linux", "riscv64", t.TempDir()); !errors.Is(err, ErrNoAsset) {
		t.Errorf("err %v", err)
	}
}

func TestInstallReleaseNoBinary(t *testing.T) {
	archive := tarGz(t, map[string][]byte{"README": []byte("x")})
	s, r := release(t, map[string][]byte{"tool.tar.gz": archive, "sums.txt": []byte(sha(archive) + "  tool.tar.gz\n")})
	if _, err := InstallRelease(context.Background(), s.Client(), "makeit/test", s.URL, r, "linux", "amd64", t.TempDir()); !errors.Is(err, ErrNoBinary) {
		t.Errorf("err %v", err)
	}
}

func TestPackageFileLandsInTmp(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("deb")) }))
	t.Cleanup(s.Close)
	path, err := FetchPackageFile(context.Background(), s.Client(), "makeit/test", s.URL+"/linux/direct/google-chrome-stable_current_amd64.deb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(path)) })
	if !strings.HasPrefix(path, os.TempDir()) || filepath.Base(path) != "google-chrome-stable_current_amd64.deb" {
		t.Errorf("path %q", path)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o644 {
		t.Errorf("mode %v", fi.Mode())
	}
}
