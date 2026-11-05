package fetch

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func server(t *testing.T, body string) (*httptest.Server, *http.Client) {
	t.Helper()
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s, s.Client()
}

func TestGet(t *testing.T) {
	s, c := server(t, "hello")
	var b bytes.Buffer
	if err := Get(context.Background(), c, "makeit/test", s.URL+"/x", 100, &b); err != nil || b.String() != "hello" {
		t.Errorf("%q %v", b.String(), err)
	}
}

func TestGetRefusesHTTP(t *testing.T) {
	_, c := server(t, "hello")
	if err := Get(context.Background(), c, "makeit/test", "http://example.invalid/x", 100, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "not https") {
		t.Errorf("err %v", err)
	}
}

func TestGetLimit(t *testing.T) {
	s, c := server(t, strings.Repeat("x", 50))
	if err := Get(context.Background(), c, "makeit/test", s.URL+"/x", 10, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "larger than 10 bytes") {
		t.Errorf("err %v", err)
	}
}

func TestGetStatus(t *testing.T) {
	s, c := server(t, "")
	if err := Get(context.Background(), c, "makeit/test", s.URL+"/missing", 10, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("err %v", err)
	}
}

// A client's roots come from the bundle files at creation, never from Go's one-shot cache: a
// run that installs ca-certificates then downloads something gets a client that sees them.
func TestClientUsesBundleFiles(t *testing.T) {
	s, _ := server(t, "hello")
	dir := t.TempDir()
	bundle := filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(bundle, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw}), 0o644); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty.crt")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := Get(context.Background(), Client(time.Minute, []string{empty, bundle}), "makeit/test", s.URL+"/x", 100, &b); err != nil || b.String() != "hello" {
		t.Errorf("with the bundle: %q %v", b.String(), err)
	}
	if err := Get(context.Background(), Client(time.Minute, []string{empty}), "makeit/test", s.URL+"/x", 100, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Errorf("without the bundle: %v, want a certificate error", err)
	}
}

func TestBundlePaths(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a.pem", "b.pem"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := BundlePaths(func(k string) string {
		return map[string]string{"SSL_CERT_FILE": "/x/bundle.pem", "SSL_CERT_DIR": dir}[k]
	})
	if len(got) < 3 || got[0] != "/x/bundle.pem" || got[1] != filepath.Join(dir, "a.pem") || got[2] != filepath.Join(dir, "b.pem") {
		t.Errorf("paths %v, want SSL_CERT_FILE, then SSL_CERT_DIR's files, then the system list", got)
	}
	if got := BundlePaths(func(string) string { return "" }); len(got) == 0 || got[0] != "/etc/ssl/certs/ca-certificates.crt" {
		t.Errorf("default paths %v", got)
	}
}

// Where the platform verifies certificates (macOS Keychain, the Windows store), the bundle files
// never replace it: a file there would drop the roots an administrator installed.
func TestClientKeepsPlatformVerifier(t *testing.T) {
	saved := platformVerifier
	t.Cleanup(func() { platformVerifier = saved })
	s, _ := server(t, "hello")
	bundle := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(bundle, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw}), 0o644); err != nil {
		t.Fatal(err)
	}
	// A cloned DefaultTransport always carries a TLS config (the h2 protocol list); the roots are
	// what the platform branch must leave alone.
	roots := func(c *http.Client) *x509.CertPool {
		if cfg := c.Transport.(*http.Transport).TLSClientConfig; cfg != nil {
			return cfg.RootCAs
		}
		return nil
	}
	platformVerifier = true
	if roots(Client(time.Minute, []string{bundle})) != nil {
		t.Error("the bundle file replaced the platform verifier")
	}
	platformVerifier = false
	if roots(Client(time.Minute, []string{bundle})) == nil {
		t.Error("the bundle file was not used where Go reads files")
	}
}

// flaky answers each request with the next status in codes (200 once they run out) and counts them.
func flaky(t *testing.T, codes ...int) (*httptest.Server, *int) {
	t.Helper()
	old := retryWait
	t.Cleanup(func() { retryWait = old })
	n := 0
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if n <= len(codes) {
			w.WriteHeader(codes[n-1])
			return
		}
		_, _ = w.Write([]byte("hello"))
	}))
	t.Cleanup(s.Close)
	return s, &n
}

// A server error on GitHub's side (the 2026-11-05 distro run got a 504 for prek) is retried, so
// one bad answer does not fail setup and skip every step after it.
func TestGetRetriesServerErrors(t *testing.T) {
	s, n := flaky(t, http.StatusGatewayTimeout, http.StatusBadGateway)
	retryWait = time.Millisecond
	var b bytes.Buffer
	if err := Get(context.Background(), s.Client(), "makeit/test", s.URL+"/x", 100, &b); err != nil || b.String() != "hello" || *n != 3 {
		t.Errorf("%q %v after %d requests", b.String(), err, *n)
	}
}

func TestGetGivesUpAfterThreeTries(t *testing.T) {
	s, n := flaky(t, 503, 503, 503, 503)
	retryWait = time.Millisecond
	if err := Get(context.Background(), s.Client(), "makeit/test", s.URL+"/x", 100, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "503") || *n != 3 {
		t.Errorf("err %v after %d requests", err, *n)
	}
}

// A client error is the answer, not a hiccup: no second request.
func TestGetDoesNotRetryNotFound(t *testing.T) {
	s, n := flaky(t, http.StatusNotFound)
	retryWait = time.Millisecond
	if err := Get(context.Background(), s.Client(), "makeit/test", s.URL+"/x", 100, &bytes.Buffer{}); err == nil || *n != 1 {
		t.Errorf("err %v after %d requests", err, *n)
	}
}

// A cancelled run stops waiting between tries.
func TestGetRetryStopsOnCancel(t *testing.T) {
	s, _ := flaky(t, 504, 504)
	retryWait = time.Hour
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := Get(ctx, s.Client(), "makeit/test", s.URL+"/x", 100, &bytes.Buffer{}); err == nil || time.Since(start) > 5*time.Second {
		t.Errorf("err %v after %s", err, time.Since(start))
	}
}
