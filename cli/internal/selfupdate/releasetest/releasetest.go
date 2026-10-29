// Package releasetest fakes makeit's GitHub releases for tests and the fake release tool: the files
// of a signed release, and a handler with GitHub's URL layout. Nothing that ships imports it.
package releasetest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"makeit/internal/minisign/minisigntest"
)

// Files is release v of makeit for target: the binary bin, checksums.txt and its signature by k with
// the trusted comment "makeit v checksums.txt". The keys are "v<v>/<file>".
func Files(k minisigntest.Key, v, target string, bin []byte) map[string][]byte {
	name := "makeit-" + v + "-" + target
	if strings.HasPrefix(target, "windows-") {
		name += ".exe"
	}
	sum := sha256.Sum256(bin)
	list := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), name)
	return map[string][]byte{
		"v" + v + "/" + name:               bin,
		"v" + v + "/checksums.txt":         []byte(list),
		"v" + v + "/checksums.txt.minisig": []byte(k.Sign([]byte(list), "makeit "+v+" checksums.txt")),
	}
}

// Handler serves GitHub's release layout for repo ("owner/name"): <repo>/releases/latest redirects
// to the tag v<latest()>, and <repo>/releases/download/<tag>/<name> serves file("<tag>/<name>").
func Handler(repo string, file func(name string) ([]byte, bool), latest func() string) http.Handler {
	prefix := "/" + repo + "/releases/"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rest, ok := strings.CutPrefix(r.URL.Path, prefix)
		switch {
		case ok && rest == "latest":
			http.Redirect(w, r, prefix+"tag/v"+latest(), http.StatusFound)
			return
		case ok && strings.HasPrefix(rest, "download/"):
			if body, found := file(strings.TrimPrefix(rest, "download/")); found {
				_, _ = w.Write(body)
				return
			}
		}
		http.NotFound(w, r)
	})
}

// Server is an httptest TLS server with GitHub's release layout for the repository "o/makeit". It
// records every request as "METHOD path".
type Server struct {
	*httptest.Server
	mu       sync.Mutex
	files    map[string][]byte
	latest   string
	requests []string
}

// NewServer starts a Server whose latest release is latest. Close it when done.
func NewServer(latest string) *Server {
	s := &Server{files: map[string][]byte{}, latest: latest}
	h := Handler("o/makeit", func(name string) ([]byte, bool) {
		s.mu.Lock()
		defer s.mu.Unlock()
		b, ok := s.files[name]
		return b, ok
	}, func() string {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.latest
	})
	s.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests = append(s.requests, r.Method+" "+r.URL.Path)
		s.mu.Unlock()
		h.ServeHTTP(w, r)
	}))
	return s
}

// Add serves files (see Files).
func (s *Server) Add(files map[string][]byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range files {
		s.files[k] = v
	}
}

// SetLatest changes the version releases/latest names.
func (s *Server) SetLatest(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latest = v
}

// Requests are the requests so far.
func (s *Server) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}
