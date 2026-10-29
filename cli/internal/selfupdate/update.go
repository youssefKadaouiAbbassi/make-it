package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"makeit/internal/claudecli"
	"makeit/internal/fsx"
	"makeit/internal/release"
	"makeit/internal/state"
)

// ErrDowngrade is `--to` an older version without `--allow-downgrade`.
var ErrDowngrade = errors.New("an older version needs --allow-downgrade")

// Pick is the version a self update would move to.
type Pick struct {
	// To is the version to install; "" when there is nothing to do.
	To string
	// Latest is the newest release; Skipped is true when it is no newer than the version a
	// rollback left.
	Latest  string
	Skipped bool
}

// Pick chooses the version: to when given (`--to`), else the latest release when it is newer than
// this binary and than the receipt's skip. It compares with the running version, never the
// receipt's, which a crash after a swap can leave behind.
func (e Env) Pick(ctx context.Context, rec *state.Receipt, to string, allowDowngrade bool) (Pick, error) {
	if to != "" {
		n, err := claudecli.Compare(to, e.Version)
		switch {
		case err != nil:
			return Pick{}, err
		case n == 0:
			return Pick{}, nil
		case n < 0 && !allowDowngrade:
			return Pick{}, fmt.Errorf("%w: makeit %s is older than this %s", ErrDowngrade, to, e.Version)
		}
		return Pick{To: to}, nil
	}
	latest, err := release.Latest(ctx, e.Client, e.Base, e.Repo)
	if err != nil {
		return Pick{}, err
	}
	p := Pick{Latest: latest}
	if n, err := claudecli.Compare(latest, e.Version); err != nil || n <= 0 {
		return p, err
	}
	if rec != nil && rec.Skipped != "" {
		if n, err := claudecli.Compare(latest, rec.Skipped); err == nil && n <= 0 {
			p.Skipped = true
			return p, nil
		}
	}
	p.To = latest
	return p, nil
}

// Result is what a self command changed.
type Result struct {
	From, To string
	// BinPath is the binary that changed.
	BinPath string
}

// Update moves this binary to release v, which the caller picked: steps 2 and 4 to 10 of
// cli/SELF-UPDATE.md. explicit is `--to`, which clears a skip. Nothing changes unless the new binary
// is verified and checked; the receipt is written after the swap.
func (e Env) Update(ctx context.Context, v string, explicit bool) (Result, error) {
	// time.Now, not e.Now: the lock's staleness is measured against the real clock.
	lock, err := fsx.Acquire(filepath.Join(e.Roots.Stack, "lock"), time.Now())
	if err != nil {
		return Result{}, err
	}
	defer lock.Release()
	rec, err := e.Gate()
	if err != nil {
		return Result{}, err
	}
	dir := filepath.Dir(e.Exe)
	clean(dir)
	sha, err := e.signedSha(ctx, v)
	if err != nil {
		return Result{}, err
	}
	tmp, err := e.download(ctx, v, sha, dir)
	if err != nil {
		return Result{}, err
	}
	if err := e.replace(ctx, tmp, v); err != nil {
		return Result{}, err
	}
	from := e.Version
	rec.Version, rec.Target, rec.Previous = v, e.Target, &from
	if n, err := claudecli.Compare(v, rec.Skipped); explicit || rec.Skipped != "" && err == nil && n > 0 {
		rec.Skipped = ""
	}
	if err := state.SaveReceipt(e.ReceiptPath(), rec); err != nil {
		return Result{}, fmt.Errorf("makeit %s is in place, but %s was not written: %w", v, e.ReceiptPath(), err)
	}
	return Result{From: from, To: v, BinPath: e.Exe}, nil
}

// replace checks tmp as release v, keeps the running binary in self/ and swaps tmp in. On failure
// tmp is removed and the binary is as it was.
func (e Env) replace(ctx context.Context, tmp, v string) error {
	err := e.check(ctx, tmp, v)
	if err == nil {
		err = keep(filepath.Join(e.Roots.Stack, "self"), e.Exe, e.Version, exeSuffix(e.Target) != "")
	}
	if err == nil {
		err = swap(tmp, e.Exe, e.Version, e.Windows)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

// keep copies binPath into selfDir as makeit-<version>[.exe] and keeps that file plus the highest
// other version; the rest are deleted, best effort.
func keep(selfDir, binPath, version string, windows bool) error {
	suffix := ""
	if windows {
		suffix = ".exe"
	}
	data, err := os.ReadFile(binPath)
	if err != nil {
		return err
	}
	name := "makeit-" + version + suffix
	if err := fsx.WriteAtomic(filepath.Join(selfDir, name), data, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(selfDir)
	if err != nil {
		return err
	}
	var others []string
	for _, en := range entries {
		v, ok := strings.CutPrefix(strings.TrimSuffix(en.Name(), suffix), "makeit-")
		if ok && en.Name() != name {
			others = append(others, v)
		}
	}
	slices.SortFunc(others, func(a, b string) int { n, _ := claudecli.Compare(b, a); return n })
	for i, v := range others {
		if i > 0 {
			_ = os.Remove(filepath.Join(selfDir, "makeit-"+v+suffix))
		}
	}
	return nil
}

// swap puts newPath in binPath's place. On Unix a rename over the running binary is atomic and
// safe. With aside (Windows), the running binary is first renamed to binPath.old-<oldVersion>, and
// renamed back if the second rename fails.
func swap(newPath, binPath, oldVersion string, aside bool) error {
	if !aside {
		return os.Rename(newPath, binPath)
	}
	old := binPath + ".old-" + oldVersion
	if err := os.Rename(binPath, old); err != nil {
		return err
	}
	if err := os.Rename(newPath, binPath); err != nil {
		if rerr := os.Rename(old, binPath); rerr != nil {
			return errors.Join(err, rerr)
		}
		return err
	}
	return nil
}

// clean removes what an interrupted self command left in binDir, best effort: downloads that were
// never swapped in, and binaries renamed aside on Windows. Callers hold the lock.
func clean(binDir string) {
	for _, pattern := range []string{".makeit.new-*", "makeit*.old-*"} {
		matches, _ := filepath.Glob(filepath.Join(binDir, pattern))
		for _, m := range matches {
			_ = os.Remove(m)
		}
	}
}
