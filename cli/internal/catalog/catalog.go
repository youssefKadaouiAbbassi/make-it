// Package catalog carries the catalog inside the binary and writes it to disk.
//
// The data directory is generated: run `go generate ./...` (tools/synccatalog) before building.
package catalog

//go:generate go run ../../tools/synccatalog -repo ../../.. -out data

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"makeit/internal/fsx"
)

//go:embed all:data
var data embed.FS

// Entry is one catalog file.
type Entry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Sha256 string `json:"sha256"`
	Mode   string `json:"mode"`
}

// Manifest lists the catalog files.
type Manifest struct {
	SchemaVersion int     `json:"schemaVersion"`
	Files         []Entry `json:"files"`
}

// Requires is the catalog's makeit.json.
type Requires struct {
	SchemaVersion int `json:"schemaVersion"`
	Requires      struct {
		ClaudeCode struct {
			Hard string `json:"hard"`
			Soft string `json:"soft"`
		} `json:"claudeCode"`
	} `json:"requires"`
	Marketplace   string `json:"marketplace"`
	Bundle        string `json:"bundle"`
	ProjectBundle string `json:"projectBundle"`
}

// Source is a catalog to read files from: the embedded copy or a directory on disk.
type Source interface {
	ReadFile(p string) ([]byte, error)
}

type embedded struct{}

func (embedded) ReadFile(p string) ([]byte, error) { return data.ReadFile("data/files/" + p) }

// Embedded is the catalog compiled into this binary.
var Embedded Source = embedded{}

// Dir reads catalog files from a directory.
type Dir string

// ReadFile reads a catalog-relative path.
func (d Dir) ReadFile(p string) ([]byte, error) {
	return os.ReadFile(filepath.Join(string(d), filepath.FromSlash(p)))
}

// EmbeddedManifest returns the manifest of the compiled-in catalog.
func EmbeddedManifest() (Manifest, error) {
	var m Manifest
	b, err := data.ReadFile("data/MANIFEST.json")
	if err != nil {
		return m, errors.New("this binary was built without a catalog (run go generate ./... before go build)")
	}
	return m, json.Unmarshal(b, &m)
}

// Digest identifies a manifest: the sha256 of its sorted path, hash and mode lines.
func Digest(m Manifest) string {
	lines := make([]string, len(m.Files))
	for i, e := range m.Files {
		lines[i] = e.Path + " " + e.Sha256 + " " + e.Mode
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// LoadRequires reads makeit.json from a catalog.
func LoadRequires(src Source) (Requires, error) {
	var r Requires
	b, err := src.ReadFile("makeit.json")
	if err != nil {
		return r, fmt.Errorf("catalog has no makeit.json: %w", err)
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return r, fmt.Errorf("makeit.json: %w", err)
	}
	if r.SchemaVersion != 1 {
		return r, fmt.Errorf("makeit.json schemaVersion %d is newer than this makeit understands; update makeit", r.SchemaVersion)
	}
	return r, nil
}

// MarkerName is written last into a synced catalog, so a partial sync is never taken for done.
const MarkerName = ".makeit-catalog.json"

// Marker records what was synced.
type Marker struct {
	Digest string   `json:"digest"`
	Files  []string `json:"files"`
}

// ReadMarker returns the marker of a synced directory, or a zero Marker.
func ReadMarker(dir string) Marker {
	var m Marker
	b, err := os.ReadFile(filepath.Join(dir, MarkerName))
	if err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

// UpToDate reports whether dir holds exactly the embedded catalog.
func UpToDate(dir string) (bool, error) {
	m, err := EmbeddedManifest()
	if err != nil {
		return false, err
	}
	return ReadMarker(dir).Digest == Digest(m), nil
}

func perm(mode string) fs.FileMode {
	if mode == "0755" {
		return 0o755
	}
	return 0o644
}

func writeTree(dir string, m Manifest) error {
	for _, e := range m.Files {
		b, err := Embedded.ReadFile(e.Path)
		if err != nil {
			return err
		}
		dst := filepath.Join(dir, filepath.FromSlash(e.Path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, b, perm(e.Mode)); err != nil {
			return err
		}
		if err := os.Chmod(dst, perm(e.Mode)); err != nil {
			return err
		}
	}
	return writeMarker(dir, m)
}

func writeMarker(dir string, m Manifest) error {
	mk := Marker{Digest: Digest(m)}
	for _, e := range m.Files {
		mk.Files = append(mk.Files, e.Path)
	}
	b, err := json.MarshalIndent(mk, "", "  ")
	if err != nil {
		return err
	}
	return fsx.WriteAtomic(filepath.Join(dir, MarkerName), append(b, '\n'), 0o644)
}

// Sync writes the embedded catalog to dir. It builds the new tree beside dir and swaps it in,
// so a Claude Code session never loads a half-written catalog. Where the swap fails (Windows,
// while a hook holds a file open), it falls back to replacing changed files one by one and
// removing only files the previous sync wrote; the marker goes last either way.
func Sync(dir string) (int, error) {
	m, err := EmbeddedManifest()
	if err != nil {
		return 0, err
	}
	next := dir + ".next"
	old := dir + ".old"
	_ = os.RemoveAll(next)
	_ = os.RemoveAll(old)
	if err := writeTree(next, m); err != nil {
		_ = os.RemoveAll(next)
		return 0, err
	}
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		if err := os.Rename(next, dir); err != nil {
			return 0, err
		}
		return len(m.Files), nil
	}
	if err := os.Rename(dir, old); err == nil {
		if err := os.Rename(next, dir); err != nil {
			_ = os.Rename(old, dir)
			_ = os.RemoveAll(next)
			return 0, err
		}
		_ = os.RemoveAll(old)
		return len(m.Files), nil
	}
	_ = os.RemoveAll(next)
	return syncInPlace(dir, m)
}

func syncInPlace(dir string, m Manifest) (int, error) {
	prev := ReadMarker(dir)
	want := map[string]bool{}
	n := 0
	for _, e := range m.Files {
		want[e.Path] = true
		dst := filepath.Join(dir, filepath.FromSlash(e.Path))
		if h, _ := fsx.FileSha256(dst); h == e.Sha256 {
			continue
		}
		b, err := Embedded.ReadFile(e.Path)
		if err != nil {
			return n, err
		}
		if err := fsx.WriteAtomic(dst, b, perm(e.Mode)); err != nil {
			return n, err
		}
		n++
	}
	for _, p := range prev.Files {
		if !want[p] && path.Clean(p) == p && !strings.HasPrefix(p, "../") {
			_ = os.Remove(filepath.Join(dir, filepath.FromSlash(p)))
		}
	}
	return n, writeMarker(dir, m)
}

// Valid reports whether dir looks like a catalog.
func Valid(dir string) error {
	if _, err := os.Stat(filepath.Join(dir, ".claude-plugin", "marketplace.json")); err != nil {
		return fmt.Errorf("%s has no .claude-plugin/marketplace.json", dir)
	}
	return nil
}
