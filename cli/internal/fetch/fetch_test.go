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
