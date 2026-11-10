package tools

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"makeit/internal/fetch"
)

// NewestNodeLTS is the newest Node release marked LTS in base's dist/index.json ("22.12.0",
// without the "v"), passing over the versions in skip (written "v22.12.0").
func NewestNodeLTS(ctx context.Context, c *http.Client, ua, base string, skip []string) (string, error) {
	var buf bytes.Buffer
	if err := fetch.Get(ctx, c, ua, base+"/dist/index.json", 8<<20, &buf); err != nil {
		return "", err
	}
	var index []struct {
		Version string `json:"version"`
		Lts     any    `json:"lts"` // the codename, or false
	}
	if err := json.Unmarshal(buf.Bytes(), &index, json.RejectUnknownMembers(false)); err != nil {
		return "", fmt.Errorf("read the Node release index: %w", err)
	}
	for _, r := range index {
		if name, ok := r.Lts.(string); ok && name != "" && !slices.Contains(skip, r.Version) {
			return strings.TrimPrefix(r.Version, "v"), nil
		}
	}
	return "", errors.New("the Node release index lists no LTS release to install")
}

// FetchNode downloads Node's linux .tar.gz for version and goarch from base and verifies it against
// the release's SHASUMS256.txt; it returns the archive and its sha256.
func FetchNode(ctx context.Context, c *http.Client, ua, base, version, goarch string) (data []byte, sum string, err error) {
	arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[goarch]
	if arch == "" {
		return nil, "", fmt.Errorf("%w: node on linux/%s", ErrNoAsset, goarch)
	}
	asset := "node-v" + version + "-linux-" + arch + ".tar.gz"
	var archive, list bytes.Buffer
	if err := fetch.Get(ctx, c, ua, base+"/dist/v"+version+"/"+asset, archiveLimit, &archive); err != nil {
		return nil, "", err
	}
	if err := fetch.Get(ctx, c, ua, base+"/dist/v"+version+"/SHASUMS256.txt", sumsLimit, &list); err != nil {
		return nil, "", err
	}
	h := sha256.Sum256(archive.Bytes())
	sum = hex.EncodeToString(h[:])
	if err := VerifyChecksum(list.Bytes(), asset, sum); err != nil {
		return nil, "", err
	}
	return archive.Bytes(), sum, nil
}

// UnpackNode unpacks Node's tarball into dir, dropping the archive's top folder. An entry that
// names a path outside dir (absolute, "..", a link pointing out, a file written through a link) or
// a hard link is refused, and dir is removed again.
func UnpackNode(data []byte, dir string) (err error) {
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
		}
	}()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	var links []string
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if h.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		if strings.HasPrefix(h.Name, "/") || slices.Contains(strings.Split(h.Name, "/"), "..") {
			return fmt.Errorf("the archive entry %q leaves the folder", h.Name)
		}
		_, rel, _ := strings.Cut(path.Clean(h.Name), "/")
		if rel == "" {
			continue
		}
		for _, l := range links {
			if rel == l || strings.HasPrefix(rel, l+"/") {
				return fmt.Errorf("the archive entry %q is written through a link", h.Name)
			}
		}
		dst := filepath.Join(dir, filepath.FromSlash(rel))
		switch h.Typeflag {
		case tar.TypeDir:
			err = os.MkdirAll(dst, 0o755)
		case tar.TypeReg:
			if err = os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			var b []byte
			if b, err = readEntry(tr, h.Name); err == nil {
				err = os.WriteFile(dst, b, os.FileMode(h.Mode).Perm()|0o600)
			}
		case tar.TypeSymlink:
			if path.IsAbs(h.Linkname) || strings.HasPrefix(path.Join(path.Dir(rel), h.Linkname), "..") {
				return fmt.Errorf("the archive link %q points outside the folder", h.Name)
			}
			if err = os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			links = append(links, rel)
			err = os.Symlink(h.Linkname, dst)
		default:
			return fmt.Errorf("the archive entry %q has an unsupported type", h.Name)
		}
		if err != nil {
			return err
		}
	}
}
