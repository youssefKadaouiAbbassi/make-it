// Package fsx has the file operations every makeit write goes through: atomic replace,
// hashing, and the single-run lock.
package fsx

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// WriteAtomic replaces path with data: a temporary file in the same directory, fsync, rename.
// A reader sees the old file or the new one, never a partial write. On Windows the rename is
// retried, because an editor, antivirus or a running hook may hold the target open for a moment.
func WriteAtomic(path string, data []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil && runtime.GOOS != "windows" {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	attempts := 1
	if runtime.GOOS == "windows" {
		attempts = 3
	}
	for i := 0; ; i++ {
		err = os.Rename(name, path)
		if err == nil || i+1 >= attempts {
			return err
		}
		time.Sleep(time.Duration(200*(i+1)) * time.Millisecond)
	}
}

// ReadOptional returns the file's bytes, or nil and false when it does not exist.
func ReadOptional(path string) ([]byte, bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	return b, err == nil, err
}

// Sha256 is the hex digest of data, prefixed "sha256:".
func Sha256(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// FileSha256 hashes a file; empty string when it does not exist.
func FileSha256(path string) (string, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// Lock is held for the length of one modifying run.
type Lock struct{ path string }

// StaleAfter is how old a lock may be before it is taken over: no run takes this long.
const StaleAfter = time.Hour

// Acquire creates the lock file, or reports who holds it.
func Acquire(path string, now time.Time) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	for range 2 {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, werr := fmt.Fprintf(f, "%d %d\n", os.Getpid(), now.Unix())
			cerr := f.Close()
			if werr != nil || cerr != nil {
				_ = os.Remove(path)
				return nil, errors.Join(werr, cerr)
			}
			return &Lock{path: path}, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		b, _ := os.ReadFile(path)
		fields := strings.Fields(string(b))
		if len(fields) == 2 {
			if ts, perr := strconv.ParseInt(fields[1], 10, 64); perr == nil && now.Sub(time.Unix(ts, 0)) > StaleAfter {
				_ = os.Remove(path)
				continue
			}
		}
		pid := "unknown"
		if len(fields) > 0 {
			pid = fields[0]
		}
		return nil, fmt.Errorf("another makeit run holds %s (pid %s); wait for it, or delete the file if that process is gone", path, pid)
	}
	return nil, fmt.Errorf("could not take the lock %s", path)
}

// Release removes the lock file.
func (l *Lock) Release() {
	if l != nil {
		_ = os.Remove(l.path)
	}
}
