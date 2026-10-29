package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"makeit/internal/execx"
	"makeit/internal/minisign/minisigntest"
)

func hexSha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// server is a TLS server with GitHub's release layout. files maps "v1.1.0/<name>" to a body; a
// body starting with "redirect:" redirects there instead. latest is the version releases/latest
// names. It records each request as "METHOD path" and the last User-Agent.
type server struct {
	*httptest.Server
	mu       sync.Mutex
	files    map[string][]byte
	latest   string
	requests []string
	agent    string
}

func serve(t *testing.T, files map[string][]byte, latest string) *server {
	t.Helper()
	s := &server{files: files, latest: latest}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests = append(s.requests, r.Method+" "+r.URL.Path)
		s.agent = r.UserAgent()
		latest, body, ok := s.latest, s.files[strings.TrimPrefix(r.URL.Path, "/o/makeit/releases/download/")], false
		s.mu.Unlock()
		switch {
		case r.URL.Path == "/o/makeit/releases/latest":
			http.Redirect(w, r, "/o/makeit/releases/tag/v"+latest, http.StatusFound)
			return
		case strings.HasPrefix(r.URL.Path, "/o/makeit/releases/download/"):
			ok = body != nil
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		if to, found := strings.CutPrefix(string(body), "redirect:"); found {
			http.Redirect(w, r, to, http.StatusFound)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *server) seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

// release is the files of release v: a binary for linux-x64 and one for darwin-arm64, checksums.txt
// and its signature by k with the trusted comment comment ("" for the right one).
func release(k minisigntest.Key, v, comment string, bin []byte) map[string][]byte {
	if comment == "" {
		comment = "makeit " + v + " checksums.txt"
	}
	name := "makeit-" + v + "-linux-x64"
	list := fmt.Sprintf("%s  makeit-%s-darwin-arm64\n%s  %s\n", hexSha([]byte("mac")), v, hexSha(bin), name)
	return map[string][]byte{
		"v" + v + "/" + name:                       bin,
		"v" + v + "/makeit-" + v + "-darwin-arm64": []byte("mac"),
		"v" + v + "/checksums.txt":                 []byte(list),
		"v" + v + "/checksums.txt.minisig":         []byte(k.Sign([]byte(list), comment)),
	}
}

// online is machine(t) pointed at s, trusting k.
func online(t *testing.T, s *server, k minisigntest.Key) Env {
	e := machine(t)
	e.Client, e.Base, e.Keys = s.Client(), s.URL, []string{k.Public()}
	return e
}

func newFiles(t *testing.T, dir string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(dir, ".makeit.new-*"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestLimits(t *testing.T) {
	if maxList != 1<<20 || maxBinary != 64<<20 {
		t.Errorf("limits %d and %d, want 1 MiB and 64 MiB", maxList, maxBinary)
	}
}

func TestAsset(t *testing.T) {
	for target, want := range map[string]string{"linux-x64": "makeit-1.1.0-linux-x64", "windows-arm64": "makeit-1.1.0-windows-arm64.exe"} {
		if got := asset("1.1.0", target); got != want {
			t.Errorf("%s: %q, want %q", target, got, want)
		}
	}
}

func TestSignedSha(t *testing.T) {
	k, other := minisigntest.NewKey(), minisigntest.NewKey()
	bin := []byte("new makeit")
	noLine := release(k, "1.1.0", "", bin)
	list := strings.ReplaceAll(string(noLine["v1.1.0/checksums.txt"]), "linux-x64", "linux-arm64")
	noLine["v1.1.0/checksums.txt"], noLine["v1.1.0/checksums.txt.minisig"] = []byte(list), []byte(k.Sign([]byte(list), "makeit 1.1.0 checksums.txt"))
	big := release(k, "1.1.0", "", bin)
	bigList := string(big["v1.1.0/checksums.txt"]) + strings.Repeat("#", 64)
	big["v1.1.0/checksums.txt"], big["v1.1.0/checksums.txt.minisig"] = []byte(bigList), []byte(k.Sign([]byte(bigList), "makeit 1.1.0 checksums.txt"))
	toHTTP := release(k, "1.1.0", "", bin)
	toHTTP["v1.1.0/checksums.txt"] = []byte("redirect:http://127.0.0.1:1/checksums.txt")
	cases := []struct {
		name  string
		files map[string][]byte
		keys  []string
		want  string // "" for success, else part of the error
	}{
		{"good", release(k, "1.1.0", "", bin), nil, ""},
		{"another key", release(other, "1.1.0", "", bin), nil, "made by a key this binary does not trust"},
		{"older version in the comment", release(k, "1.1.0", "makeit 1.0.0 checksums.txt", bin), nil, "trusted comment"},
		{"another file in the comment", release(k, "1.1.0", "makeit 1.1.0 install.sh", bin), nil, "trusted comment"},
		{"no line for this target", noLine, nil, "no line for makeit-1.1.0-linux-x64"},
		{"list over the limit", big, nil, "larger than"},
		{"redirect to http", toHTTP, nil, "not https"},
		{"not found", map[string][]byte{}, nil, "404"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := serve(t, c.files, "1.1.0")
			e := online(t, s, k)
			if c.name == "list over the limit" {
				saved := maxList
				maxList = int64(len(bigList) - 1)
				t.Cleanup(func() { maxList = saved })
			}
			sha, err := e.signedSha(context.Background(), "1.1.0")
			switch {
			case c.want == "" && (err != nil || sha != hexSha(bin)):
				t.Fatalf("sha %q, %v; want %q", sha, err, hexSha(bin))
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Fatalf("err %v, want one saying %q", err, c.want)
			}
			if s.agent != "makeit/1.1.0" {
				t.Errorf("User-Agent %q", s.agent)
			}
		})
	}
}

func TestDownload(t *testing.T) {
	k := minisigntest.NewKey()
	bin := []byte("new makeit")
	cases := []struct {
		name  string
		sha   string
		limit int64
		want  string
	}{
		{"good", hexSha(bin), maxBinary, ""},
		{"sha mismatch", hexSha([]byte("other")), maxBinary, "sha256"},
		{"over the limit", hexSha(bin), int64(len(bin) - 1), "larger than"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := serve(t, release(k, "1.1.0", "", bin), "1.1.0")
			e := online(t, s, k)
			saved := maxBinary
			maxBinary = c.limit
			t.Cleanup(func() { maxBinary = saved })
			dir := filepath.Dir(e.Exe)
			path, err := e.download(context.Background(), "1.1.0", c.sha, dir)
			if c.want != "" {
				if err == nil || !strings.Contains(err.Error(), c.want) {
					t.Fatalf("err %v, want one saying %q", err, c.want)
				}
				if left := newFiles(t, dir); len(left) > 0 {
					t.Errorf("left %v", left)
				}
				return
			}
			b, rerr := os.ReadFile(path)
			fi, serr := os.Stat(path)
			if err != nil || rerr != nil || serr != nil || string(b) != string(bin) || fi.Mode().Perm() != 0o755 ||
				filepath.Dir(path) != dir || !strings.HasPrefix(filepath.Base(path), ".makeit.new-") {
				t.Fatalf("path %q err %v: %q %v %v", path, err, b, rerr, serr)
			}
		})
	}
}

func TestCheck(t *testing.T) {
	good := `{"version":"1.1.0","target":"linux-x64","repo":"o/makeit","keys":["ABC"]}`
	cases := []struct {
		name, out string
		code      int
		want      string
	}{
		{"good", good, 0, ""},
		{"wrong version", strings.Replace(good, `"1.1.0"`, `"1.0.0"`, 1), 0, "version"},
		{"wrong target", strings.Replace(good, "linux-x64", "darwin-arm64", 1), 0, "target"},
		{"no repo", strings.Replace(good, `"o/makeit"`, `""`, 1), 0, "repo"},
		{"no keys", strings.Replace(good, `["ABC"]`, `[]`, 1), 0, "keys"},
		{"exit 1", good, 1, "exited 1"},
		{"not JSON", "makeit 1.1.0", 0, "version --json"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := machine(t)
			var got execx.Cmd
			e.Run = func(_ context.Context, cmd execx.Cmd) (execx.Result, error) {
				got = cmd
				return execx.Result{Code: c.code, Stdout: c.out}, nil
			}
			err := e.check(context.Background(), "/bin/new", "1.1.0")
			if c.want == "" && err != nil || c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
				t.Fatalf("err %v, want %q", err, c.want)
			}
			if strings.Join(got.Argv, " ") != "/bin/new version --json" || got.Timeout != 10*time.Second {
				t.Errorf("ran %v with timeout %s", got.Argv, got.Timeout)
			}
		})
	}
}
