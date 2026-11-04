// Package tools installs third-party programs makeit needs: a binary from a project's GitHub
// release, verified against the checksum file published beside it, or a package file a vendor
// publishes for the package manager.
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
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"makeit/internal/fetch"
	"makeit/internal/fsx"
	"makeit/internal/platform"
)

var (
	// ErrNoAsset: the project ships nothing for this machine.
	ErrNoAsset = errors.New("no release asset for this machine")
	// ErrChecksum: the download does not match the project's checksum file, or the file has no
	// line for it.
	ErrChecksum = errors.New("checksum mismatch")
	// ErrNoBinary: the archive holds no file by the program's name.
	ErrNoBinary = errors.New("the archive has no such program")
)

// archiveLimit bounds a download and an entry inside it; a variable for the tests.
var archiveLimit int64 = 200 << 20

const sumsLimit = 1 << 20

var hexLine = regexp.MustCompile(`^([0-9a-fA-F]{64})(?:\s+\*?(\S+))?\s*$`)

// VerifyChecksum checks sha256hex against sums, a checksum file in either common form: lines of
// `<hex>  <name>` (one per asset, a `*` before the name allowed), or one bare `<hex>` for a file
// that covers one asset.
func VerifyChecksum(sums []byte, asset, sha256hex string) error {
	lines := strings.Split(strings.TrimSpace(string(sums)), "\n")
	for _, l := range lines {
		m := hexLine.FindStringSubmatch(strings.TrimSpace(l))
		if m == nil {
			continue
		}
		if (m[2] == "" && len(lines) == 1) || path.Base(m[2]) == asset {
			if strings.EqualFold(m[1], sha256hex) {
				return nil
			}
			return fmt.Errorf("%w: %s", ErrChecksum, asset)
		}
	}
	return fmt.Errorf("%w: no line for %s in the checksum file", ErrChecksum, asset)
}

// FetchRelease downloads r's archive for the target from base, verifies it against the project's
// checksum file, and returns the program's file name (bin, .exe on Windows) and bytes.
func FetchRelease(ctx context.Context, c *http.Client, ua, base string, r platform.Release, version, goos, goarch string) (name string, data []byte, err error) {
	asset, sums := r.Asset(version, goos, goarch)
	if asset == "" {
		return "", nil, fmt.Errorf("%w: %s on %s/%s", ErrNoAsset, r.Repo, goos, goarch)
	}
	var archive, list bytes.Buffer
	if err := fetch.Get(ctx, c, ua, r.URL(base, version, asset), archiveLimit, &archive); err != nil {
		return "", nil, err
	}
	if err := fetch.Get(ctx, c, ua, r.URL(base, version, sums), sumsLimit, &list); err != nil {
		return "", nil, err
	}
	h := sha256.Sum256(archive.Bytes())
	if err := VerifyChecksum(list.Bytes(), asset, hex.EncodeToString(h[:])); err != nil {
		return "", nil, err
	}
	name = r.Bin
	if goos == "windows" {
		name += ".exe"
	}
	if strings.HasSuffix(asset, ".zip") {
		data, err = fromZip(archive.Bytes(), name)
	} else {
		data, err = fromTarGz(archive.Bytes(), name)
	}
	if err != nil {
		return "", nil, err
	}
	return name, data, nil
}

// InstallRelease is FetchRelease then a write of dir/<name> (0755); it returns the path.
func InstallRelease(ctx context.Context, c *http.Client, ua, base string, r platform.Release, version, goos, goarch, dir string) (string, error) {
	name, data, err := FetchRelease(ctx, c, ua, base, r, version, goos, goarch)
	if err != nil {
		return "", err
	}
	dst := filepath.Join(dir, name)
	if err := fsx.WriteAtomic(dst, data, 0o755); err != nil {
		return "", err
	}
	return dst, nil
}

func fromTarGz(b []byte, name string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%w: %s", ErrNoBinary, name)
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && path.Base(h.Name) == name {
			return readEntry(tr, name)
		}
	}
}

func fromZip(b []byte, name string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return nil, err
	}
	for _, f := range zr.File {
		if path.Base(f.Name) != name || !f.Mode().IsRegular() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer func() { _ = rc.Close() }()
		return readEntry(rc, name)
	}
	return nil, fmt.Errorf("%w: %s", ErrNoBinary, name)
}

// readEntry reads one archive entry, refusing one past the limit instead of cutting it.
func readEntry(r io.Reader, name string) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, archiveLimit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > archiveLimit {
		return nil, fmt.Errorf("%s is larger than %d bytes", name, archiveLimit)
	}
	return data, nil
}

// FetchPackageFile downloads a vendor's .deb or .rpm into a fresh directory under the system
// temporary directory (so apt's sandboxed user can read it) and returns its path.
func FetchPackageFile(ctx context.Context, c *http.Client, ua, url string) (string, error) {
	dir, err := os.MkdirTemp("", "makeit-pkg-")
	if err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		return "", err
	}
	var b bytes.Buffer
	if err := fetch.Get(ctx, c, ua, url, archiveLimit, &b); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	dst := filepath.Join(dir, path.Base(url))
	if err := fsx.WriteAtomic(dst, b.Bytes(), 0o644); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return dst, nil
}
