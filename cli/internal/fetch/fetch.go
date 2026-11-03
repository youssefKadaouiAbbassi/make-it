// Package fetch downloads over HTTPS with the limits every makeit download keeps.
package fetch

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// systemBundles are the CA bundle files Go's crypto/x509 reads on Linux.
var systemBundles = []string{
	"/etc/ssl/certs/ca-certificates.crt", "/etc/pki/tls/certs/ca-bundle.crt", "/etc/ssl/ca-bundle.pem",
	"/etc/pki/tls/cacert.pem", "/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem", "/etc/ssl/cert.pem",
}

// BundlePaths are the CA bundle files to read, SSL_CERT_FILE first, then the files of
// SSL_CERT_DIR, then the system list.
func BundlePaths(getenv func(string) string) []string {
	var out []string
	if f := getenv("SSL_CERT_FILE"); f != "" {
		out = append(out, f)
	}
	if d := getenv("SSL_CERT_DIR"); d != "" {
		if entries, err := os.ReadDir(d); err == nil {
			var names []string
			for _, e := range entries {
				if !e.IsDir() {
					names = append(names, filepath.Join(d, e.Name()))
				}
			}
			sort.Strings(names)
			out = append(out, names...)
		}
	}
	return append(out, systemBundles...)
}

// Client is an HTTP client whose roots are read from the bundle files now, not from Go's pool,
// which loads once per process: a run that installs ca-certificates and then downloads something
// makes a client after the install and sees the new bundle. Where no file holds a certificate
// (macOS and Windows keep theirs in a store), the platform's own verifier is used.
func Client(timeout time.Duration, bundles []string) *http.Client {
	pool := x509.NewCertPool()
	found := false
	for _, p := range bundles {
		if b, err := os.ReadFile(p); err == nil && pool.AppendCertsFromPEM(b) {
			found = true
		}
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	if found {
		t.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	return &http.Client{Timeout: timeout, Transport: t}
}

// Get copies url's body into w: https only, redirects only to https and at most ten, status 200,
// at most limit bytes, and the User-Agent is all it sends.
func Get(ctx context.Context, c *http.Client, userAgent, url string, limit int64, w io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if req.URL.Scheme != "https" {
		return fmt.Errorf("refusing %s: not https", url)
	}
	req.Header.Set("User-Agent", userAgent)
	cl := *c
	cl.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if r.URL.Scheme != "https" {
			return fmt.Errorf("refusing a redirect to %s: not https", r.URL)
		}
		if len(via) >= 10 {
			return errors.New("too many redirects")
		}
		return nil
	}
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %s", url, resp.Status)
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return err
	}
	if n > limit {
		return fmt.Errorf("%s is larger than %d bytes", url, limit)
	}
	return nil
}
