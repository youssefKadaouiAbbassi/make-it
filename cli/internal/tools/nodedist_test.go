package tools

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const nodeIndex = `[
{"version":"v25.1.0","lts":false},
{"version":"v24.11.0","lts":"Krypton"},
{"version":"v22.21.0","lts":"Jod"}]`

func TestNewestNodeLTS(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/dist/index.json" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(nodeIndex))
	}))
	defer s.Close()
	v, err := NewestNodeLTS(context.Background(), s.Client(), "t", s.URL, nil)
	if err != nil || v != "24.11.0" {
		t.Fatalf("got %q, %v; want the first lts entry, 24.11.0", v, err)
	}
	// An LTS rollback undid is passed over.
	if v, err = NewestNodeLTS(context.Background(), s.Client(), "t", s.URL, []string{"v24.11.0"}); err != nil || v != "22.21.0" {
		t.Fatalf("with skip got %q, %v; want 22.21.0", v, err)
	}
	if _, err = NewestNodeLTS(context.Background(), s.Client(), "t", s.URL, []string{"v24.11.0", "v22.21.0"}); err == nil {
		t.Fatal("every LTS skipped: want an error")
	}
}

func nodeTar(t *testing.T, entries ...tar.Header) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, h := range entries {
		body := []byte(h.PAXRecords["body"])
		h.PAXRecords = nil
		h.Size = int64(len(body))
		if h.Typeflag != tar.TypeReg {
			h.Size = 0
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			_, _ = tw.Write(body)
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func reg(name, body string, mode int64) tar.Header {
	return tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: mode, PAXRecords: map[string]string{"body": body}}
}

func TestUnpackNode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "22.0.0")
	err := UnpackNode(nodeTar(t,
		tar.Header{Name: "node-v22.0.0-linux-x64/", Typeflag: tar.TypeDir, Mode: 0o755},
		reg("node-v22.0.0-linux-x64/bin/node", "ELF", 0o755),
		reg("node-v22.0.0-linux-x64/lib/node_modules/npm/bin/npm-cli.js", "js", 0o644),
		tar.Header{Name: "node-v22.0.0-linux-x64/bin/npm", Typeflag: tar.TypeSymlink, Linkname: "../lib/node_modules/npm/bin/npm-cli.js", Mode: 0o777},
	), dir)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "bin", "node")); string(b) != "ELF" {
		t.Errorf("bin/node %q", b)
	}
	if fi, _ := os.Stat(filepath.Join(dir, "bin", "node")); fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("bin/node mode %v, want executable", fi.Mode())
	}
	if b, err := os.ReadFile(filepath.Join(dir, "bin", "npm")); err != nil || string(b) != "js" {
		t.Errorf("bin/npm link read %q, %v", b, err)
	}
}

// An entry that would land outside the folder is refused, and nothing it comes with stays.
func TestUnpackNodeRefusesEscapes(t *testing.T) {
	for name, h := range map[string]tar.Header{
		"dotdot":              reg("node-v1/../../evil", "x", 0o644),
		"absolute":            reg("/etc/evil", "x", 0o644),
		"absolute link":       {Name: "node-v1/bin/l", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
		"link escaping":       {Name: "node-v1/bin/l", Typeflag: tar.TypeSymlink, Linkname: "../../../x"},
		"hard link":           {Name: "node-v1/bin/h", Typeflag: tar.TypeLink, Linkname: "node-v1/bin/node"},
		"write via link":      reg("node-v1/l/evil", "x", 0o644),
		"link through a link": {Name: "node-v1/x", Typeflag: tar.TypeSymlink, Linkname: "a/b/s/../../.."},
	} {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			dir := filepath.Join(parent, "1.0.0")
			entries := []tar.Header{reg("node-v1/bin/node", "ELF", 0o755)}
			if name == "link through a link" {
				entries = append(entries, tar.Header{Name: "node-v1/a/b/s", Typeflag: tar.TypeSymlink, Linkname: "../.."})
			}
			if name == "write via link" {
				entries = append(entries, tar.Header{Name: "node-v1/l", Typeflag: tar.TypeSymlink, Linkname: "."})
			}
			if err := UnpackNode(nodeTar(t, append(entries, h)...), dir); err == nil {
				t.Fatal("want a refusal")
			}
			if _, err := os.Stat(filepath.Join(parent, "evil")); err == nil {
				t.Error("a file escaped the folder")
			}
		})
	}
}

func TestFetchNode(t *testing.T) {
	archive := []byte("tarball bytes")
	serve := func(sums string) (*httptest.Server, string) {
		s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/dist/v22.0.0/node-v22.0.0-linux-arm64.tar.gz":
				_, _ = w.Write(archive)
			case "/dist/v22.0.0/SHASUMS256.txt":
				_, _ = w.Write([]byte(sums))
			default:
				http.NotFound(w, r)
			}
		}))
		return s, s.URL
	}
	good := sha(archive) + "  node-v22.0.0-linux-arm64.tar.gz\n"
	s, base := serve(good)
	defer s.Close()
	data, sum, err := FetchNode(context.Background(), s.Client(), "t", base, "22.0.0", "arm64")
	if err != nil || !bytes.Equal(data, archive) || sum != sha(archive) {
		t.Fatalf("got %q %s %v", data, sum, err)
	}
	bad, base2 := serve(sha([]byte("other")) + "  node-v22.0.0-linux-arm64.tar.gz\n")
	defer bad.Close()
	if _, _, err := FetchNode(context.Background(), bad.Client(), "t", base2, "22.0.0", "arm64"); !errors.Is(err, ErrChecksum) {
		t.Errorf("err %v, want ErrChecksum", err)
	}
	if _, _, err := FetchNode(context.Background(), s.Client(), "t", base, "22.0.0", "386"); !errors.Is(err, ErrNoAsset) {
		t.Errorf("err %v, want ErrNoAsset", err)
	}
}

// A version from the index that is not x.y.z (a path, say) is never put in a URL or a folder name.
func TestNodeVersionMustBeNumeric(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"version":"v../../evil","lts":"Jod"}]`))
	}))
	defer s.Close()
	if v, err := NewestNodeLTS(context.Background(), s.Client(), "t", s.URL, nil); err == nil {
		t.Errorf("accepted %q", v)
	}
	if _, _, err := FetchNode(context.Background(), s.Client(), "t", s.URL, "../../evil", "amd64"); err == nil {
		t.Error("FetchNode accepted a path as a version")
	}
}

// A checksum file with one bare hash and no file name proves nothing about this asset.
func TestFetchNodeNeedsTheFileName(t *testing.T) {
	archive := []byte("tarball bytes")
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "SHASUMS256.txt") {
			_, _ = w.Write([]byte(sha(archive) + "\n"))
			return
		}
		_, _ = w.Write(archive)
	}))
	defer s.Close()
	if _, _, err := FetchNode(context.Background(), s.Client(), "t", s.URL, "22.0.0", "amd64"); !errors.Is(err, ErrChecksum) {
		t.Errorf("err %v, want ErrChecksum", err)
	}
}

// The link check does not depend on the order of the entries: A -> q/.. is fine until q -> a/b/s
// (which reaches ../.. from a/b) makes it leave the folder.
func TestUnpackNodeRefusesLinksCompletedLater(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "1.0.0")
	err := UnpackNode(nodeTar(t,
		reg("node-v1/bin/node", "ELF", 0o755),
		tar.Header{Name: "node-v1/A", Typeflag: tar.TypeSymlink, Linkname: "q/.."},
		tar.Header{Name: "node-v1/a/b/s", Typeflag: tar.TypeSymlink, Linkname: "../.."},
		tar.Header{Name: "node-v1/q", Typeflag: tar.TypeSymlink, Linkname: "a/b/s"},
	), dir)
	if err == nil {
		t.Fatal("want a refusal")
	}
	if _, serr := os.Stat(dir); serr == nil {
		t.Error("the folder stayed after a refusal")
	}
}
