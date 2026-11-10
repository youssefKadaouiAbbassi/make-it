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
	"regexp"
	"slices"
	"strings"

	"makeit/internal/fetch"
)

var nodeVersion = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

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
			v := strings.TrimPrefix(r.Version, "v")
			if !nodeVersion.MatchString(v) {
				return "", fmt.Errorf("the Node release index has a version that is not x.y.z: %q", r.Version)
			}
			return v, nil
		}
	}
	return "", errors.New("the Node release index lists no LTS release to install")
}

// FetchNode downloads Node's linux .tar.gz for version and goarch from base and verifies it against
// the release's SHASUMS256.txt; it returns the archive and its sha256.
func FetchNode(ctx context.Context, c *http.Client, ua, base, version, goarch string) (data []byte, sum string, err error) {
	if !nodeVersion.MatchString(version) {
		return nil, "", fmt.Errorf("%q is not a Node version (x.y.z)", version)
	}
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
	if err := verifyNodeSums(list.Bytes(), asset, sum); err != nil {
		return nil, "", err
	}
	return archive.Bytes(), sum, nil
}

// verifyNodeSums is VerifyChecksum for SHASUMS256.txt, which names every file: a bare hash with no
// file name proves nothing about this asset, so only the line for the exact name counts.
func verifyNodeSums(sums []byte, asset, sum string) error {
	for _, l := range strings.Split(string(sums), "\n") {
		if f := strings.Fields(l); len(f) == 2 && f[1] == asset {
			return VerifyChecksum([]byte(l), asset, sum)
		}
	}
	return fmt.Errorf("%w: no line for %s in the checksum file", ErrChecksum, asset)
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
	links := map[string]string{} // entry -> target, for the links written so far
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
		for l := range links {
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
			if path.IsAbs(h.Linkname) || !within(links, rel, h.Linkname) {
				return fmt.Errorf("the archive link %q points outside the folder", h.Name)
			}
			if err = os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			links[rel] = h.Linkname
			err = os.Symlink(h.Linkname, dst)
		default:
			return fmt.Errorf("the archive entry %q has an unsupported type", h.Name)
		}
		if err != nil {
			return err
		}
	}
}

// within reports whether the link at rel, pointing at target, stays inside the unpacked folder once
// the links written before it are followed (a/b/s -> ../.. then x -> a/b/s/../../.. leaves it).
func within(links map[string]string, rel, target string) bool {
	todo := append(strings.Split(path.Dir(rel), "/"), strings.Split(target, "/")...)
	var stack []string
	for steps := 0; len(todo) > 0; steps++ {
		if steps > 1000 {
			return false
		}
		c := todo[0]
		todo = todo[1:]
		switch c {
		case "", ".":
		case "..":
			if len(stack) == 0 {
				return false
			}
			stack = stack[:len(stack)-1]
		default:
			stack = append(stack, c)
			if t, ok := links[strings.Join(stack, "/")]; ok {
				stack = stack[:len(stack)-1]
				todo = append(strings.Split(t, "/"), todo...)
			}
		}
	}
	return true
}
