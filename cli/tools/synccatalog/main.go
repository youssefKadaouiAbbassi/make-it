// Command synccatalog copies the catalog files git would track into the embed directory and
// writes MANIFEST.json (path, size, sha256, mode). Only allow-listed paths are taken, so build
// leftovers, eval results and unreadable scratch directories never reach the binary.
//
//	go run ./tools/synccatalog -repo .. -out internal/catalog/data
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"makeit/internal/catalog/allowlist"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Entry is one manifest line.
type Entry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Sha256 string `json:"sha256"`
	Mode   string `json:"mode"` // "0755" or "0644"
}

// Manifest is MANIFEST.json.
type Manifest struct {
	SchemaVersion int     `json:"schemaVersion"`
	Files         []Entry `json:"files"`
}

// List returns the allow-listed files git tracks or would track (gitignore applies).
func List(repo string) ([]string, error) {
	out, err := exec.Command("git", "-C", repo, "ls-files", "-z", "--cached", "--others", "--exclude-standard").Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files in %s: %w", repo, err)
	}
	var files []string
	seen := map[string]bool{}
	for _, p := range strings.Split(string(out), "\x00") {
		if p == "" || seen[p] || !allowlist.Allowed(p) {
			continue
		}
		seen[p] = true
		// git lists deleted-but-staged paths too; only real files count.
		if st, err := os.Lstat(filepath.Join(repo, p)); err == nil && st.Mode().IsRegular() {
			files = append(files, p)
		}
	}
	sort.Strings(files)
	return files, nil
}

// Build lists and hashes the files without copying them.
func Build(repo string) (Manifest, error) {
	files, err := List(repo)
	if err != nil {
		return Manifest{}, err
	}
	m := Manifest{SchemaVersion: 1}
	for _, p := range files {
		b, err := os.ReadFile(filepath.Join(repo, p))
		if err != nil {
			return m, err
		}
		st, err := os.Stat(filepath.Join(repo, p))
		if err != nil {
			return m, err
		}
		mode := "0644"
		if st.Mode().Perm()&0o111 != 0 {
			mode = "0755"
		}
		sum := sha256.Sum256(b)
		m.Files = append(m.Files, Entry{Path: p, Size: int64(len(b)), Sha256: "sha256:" + hex.EncodeToString(sum[:]), Mode: mode})
	}
	if len(m.Files) == 0 {
		return m, errors.New("no catalog files found: is -repo the catalog repository?")
	}
	return m, nil
}

func run(repo, out string) error {
	m, err := Build(repo)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(out); err != nil {
		return err
	}
	for _, e := range m.Files {
		dst := filepath.Join(out, "files", filepath.FromSlash(e.Path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		b, err := os.ReadFile(filepath.Join(repo, e.Path))
		if err != nil {
			return err
		}
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			return err
		}
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "MANIFEST.json"), append(b, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Printf("synccatalog: %d files into %s\n", len(m.Files), out)
	return nil
}

func main() {
	repo := flag.String("repo", "..", "catalog repository root")
	out := flag.String("out", "internal/catalog/data", "embed directory to (re)create")
	flag.Parse()
	if err := run(*repo, *out); err != nil {
		fmt.Fprintln(os.Stderr, "synccatalog:", err)
		os.Exit(1)
	}
}
