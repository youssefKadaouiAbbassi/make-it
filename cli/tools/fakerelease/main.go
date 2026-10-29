// Command fakerelease makes and serves fake signed makeit releases for the distro test
// (scripts/test-setup-distros.sh): a throwaway key from a seed, release files in GitHub's layout,
// and an HTTPS server with a throwaway certificate. It is test tooling; nothing ships it.
//
//	fakerelease pub -seed <64 hex>
//	fakerelease release -seed <hex> -repo test/makeit -version V -target linux-x64 -binary <file> -dir <site>
//	fakerelease serve -dir <site> -repo test/makeit -addr 127.0.0.1:8443 -ca <file> -latest <file>
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"makeit/internal/minisign/minisigntest"
	"makeit/internal/selfupdate/releasetest"
)

func key(seedHex string) (minisigntest.Key, error) {
	b, err := hex.DecodeString(seedHex)
	if err != nil || len(b) != 32 {
		return minisigntest.Key{}, errors.New("-seed takes 64 hex characters")
	}
	return minisigntest.KeyFromSeed([32]byte(b)), nil
}

// public is the public key line of the seed's key, for -X makeit/internal/buildinfo.KeyCurrent.
func public(seedHex string) (string, error) {
	k, err := key(seedHex)
	return k.Public(), err
}

// release writes release version of repo for target into dir: the binary, checksums.txt and its
// signature, under <repo>/releases/download/v<version>/.
func release(seedHex, repo, version, target, binary, dir string) error {
	k, err := key(seedHex)
	if err != nil {
		return err
	}
	bin, err := os.ReadFile(binary)
	if err != nil {
		return err
	}
	base := filepath.Join(dir, filepath.FromSlash(repo), "releases", "download")
	for name, body := range releasetest.Files(k, version, target, bin) {
		path := filepath.Join(base, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// serve listens on addr, writes a throwaway certificate for its IP to caFile, and serves dir in
// GitHub's release layout; releases/latest names the version in latestFile, read on every request.
func serve(dir, repo, addr, caFile, latestFile string) error {
	cert, err := selfSigned(addr)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]}), 0o644); err != nil {
		return err
	}
	base := filepath.Join(dir, filepath.FromSlash(repo), "releases", "download")
	h := releasetest.Handler(repo, func(name string) ([]byte, bool) {
		if !filepath.IsLocal(filepath.FromSlash(name)) {
			return nil, false
		}
		b, err := os.ReadFile(filepath.Join(base, filepath.FromSlash(name)))
		return b, err == nil
	}, func() string {
		b, _ := os.ReadFile(latestFile)
		return strings.TrimSpace(string(b))
	})
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}}
	return srv.ServeTLS(ln, "", "")
}

// selfSigned is a one-day certificate for addr's IP that is its own CA.
func selfSigned(addr string) (tls.Certificate, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return tls.Certificate{}, err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return tls.Certificate{}, fmt.Errorf("-addr needs an IP, not %q", host)
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fakerelease"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true, IPAddresses: []net.IP{ip}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}, nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: fakerelease pub|release|serve [flags]")
		os.Exit(2)
	}
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	seed := fs.String("seed", "", "the key's seed, 64 hex characters")
	repo := fs.String("repo", "test/makeit", "the repository, owner/name")
	version := fs.String("version", "", "release version")
	target := fs.String("target", "linux-x64", "the binary's target")
	binary := fs.String("binary", "", "the binary to release")
	dir := fs.String("dir", "", "the site directory")
	addr := fs.String("addr", "127.0.0.1:8443", "where to listen")
	ca := fs.String("ca", "", "where to write the certificate to trust")
	latest := fs.String("latest", "", "the file naming the latest version")
	_ = fs.Parse(os.Args[2:])
	var err error
	switch os.Args[1] {
	case "pub":
		var p string
		if p, err = public(*seed); err == nil {
			fmt.Println(p)
		}
	case "release":
		err = release(*seed, *repo, *version, *target, *binary, *dir)
	case "serve":
		err = serve(*dir, *repo, *addr, *ca, *latest)
	default:
		err = fmt.Errorf("unknown subcommand %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakerelease:", err)
		os.Exit(1)
	}
}
