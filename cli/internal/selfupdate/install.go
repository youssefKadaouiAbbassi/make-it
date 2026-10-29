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
	"makeit/internal/state"
)

var (
	// ErrNothing is a rollback with no kept binary to go back to.
	ErrNothing = errors.New("nothing to roll back to")
	// ErrForeign is a file at the install target that makeit's receipt does not name.
	ErrForeign = errors.New("is not makeit's")
)

// kept is self/makeit-<v>[.exe] in the stack dir.
func (e Env) kept(v string) string {
	return filepath.Join(e.Roots.Stack, "self", "makeit-"+v+exeSuffix(e.Target))
}

// Previous is the version a rollback would bring back: the receipt's previous, kept in self/.
func (e Env) Previous() (string, error) {
	rec, err := e.Gate()
	if err != nil {
		return "", err
	}
	return e.previous(rec)
}

func (e Env) previous(rec *state.Receipt) (string, error) {
	if rec.Previous == nil || *rec.Previous == "" {
		return "", ErrNothing
	}
	if _, err := os.Stat(e.kept(*rec.Previous)); err != nil {
		return "", fmt.Errorf("%w: %s is missing", ErrNothing, e.kept(*rec.Previous))
	}
	return *rec.Previous, nil
}

// Rollback brings back the binary that ran before the last swap, after its `version --json` check.
// The version it leaves becomes the skip (the higher one wins) and is not kept: `self update --to V`
// downloads it again.
func (e Env) Rollback(ctx context.Context) (Result, error) {
	lock, err := fsx.Acquire(filepath.Join(e.Roots.Stack, "lock"), time.Now())
	if err != nil {
		return Result{}, err
	}
	defer lock.Release()
	rec, err := e.Gate()
	if err != nil {
		return Result{}, err
	}
	prev, err := e.previous(rec)
	if err != nil {
		return Result{}, err
	}
	dir := filepath.Dir(e.Exe)
	clean(dir)
	tmp, err := copyTemp(e.kept(prev), dir, e.Target)
	if err != nil {
		return Result{}, err
	}
	if err := e.check(ctx, tmp, prev); err != nil {
		_ = os.Remove(tmp)
		return Result{}, err
	}
	if err := swap(tmp, e.Exe, e.Version, e.Windows); err != nil {
		_ = os.Remove(tmp)
		return Result{}, err
	}
	_ = os.Remove(e.kept(prev))
	from := e.Version
	rec.Version, rec.Previous = prev, nil
	if other := e.newestKept(); other != "" {
		rec.Previous = &other
	}
	if n, err := claudecli.Compare(from, rec.Skipped); rec.Skipped == "" || err == nil && n > 0 {
		rec.Skipped = from
	}
	if err := state.SaveReceipt(e.ReceiptPath(), rec); err != nil {
		return Result{}, fmt.Errorf("makeit %s is back in place, but %s was not written: %w", prev, e.ReceiptPath(), err)
	}
	return Result{From: from, To: prev, BinPath: e.Exe}, nil
}

// newestKept is the highest version left in self/, or "".
func (e Env) newestKept() string {
	entries, _ := os.ReadDir(filepath.Join(e.Roots.Stack, "self"))
	var versions []string
	for _, en := range entries {
		if v, ok := strings.CutPrefix(strings.TrimSuffix(en.Name(), exeSuffix(e.Target)), "makeit-"); ok {
			versions = append(versions, v)
		}
	}
	slices.SortFunc(versions, func(a, b string) int { n, _ := claudecli.Compare(b, a); return n })
	if len(versions) == 0 {
		return ""
	}
	return versions[0]
}

// tmpPath is where a new binary waits in dir before it is renamed into place.
func tmpPath(dir, target string) string {
	return filepath.Join(dir, fmt.Sprintf(".makeit.new-%d%s", os.Getpid(), exeSuffix(target)))
}

// copyTemp copies src to tmpPath(dir, target) with mode 0755, flushed to disk; a failure leaves no
// file.
func copyTemp(src, dir, target string) (string, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	tmp := tmpPath(dir, target)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return "", err
	}
	_, err = f.Write(data)
	if err == nil {
		err = syncFile(f)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, 0o755)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

// InstallResult is what self install did.
type InstallResult struct {
	// Path is the installed binary; Previous is the version it replaced, if any.
	Path, Previous string
	// OldPath is another copy an earlier receipt named; it stays where it is.
	OldPath string
	// OnPath is true when Path's directory is on PATH.
	OnPath bool
}

// InstallTarget is where self install puts makeit: prefix, or ~/.local/bin.
func (e Env) InstallTarget(prefix string) string {
	if prefix == "" {
		prefix = filepath.Join(e.Roots.Home, ".local", "bin")
	}
	return filepath.Join(prefix, "makeit"+exeSuffix(e.Target))
}

// Install copies this binary to the target and writes a fresh receipt, which clears any skip. A
// file already at the target is replaced only when the receipt names it, and is then kept in self/
// for a rollback.
func (e Env) Install(_ context.Context, prefix string) (InstallResult, error) {
	if e.Dev() {
		return InstallResult{}, ErrDev
	}
	lock, err := fsx.Acquire(filepath.Join(e.Roots.Stack, "lock"), time.Now())
	if err != nil {
		return InstallResult{}, err
	}
	defer lock.Release()
	target, err := filepath.Abs(e.InstallTarget(prefix))
	if err != nil {
		return InstallResult{}, err
	}
	old, _, err := state.LoadReceipt(e.ReceiptPath())
	if err != nil {
		return InstallResult{}, err
	}
	res := InstallResult{Path: target}
	namesTarget := false
	if old != nil {
		if p := e.Roots.Abs(old.BinPath); Same(p, target) {
			namesTarget = true
		} else {
			res.OldPath = p
		}
	}
	var prev *string
	_, statErr := os.Stat(target)
	switch {
	case Same(e.Exe, target):
		// Installing the running copy: only the receipt is written, and its rollback stays.
		if namesTarget {
			prev = old.Previous
		}
	case errors.Is(statErr, os.ErrNotExist):
	case statErr != nil:
		return res, statErr
	case !namesTarget:
		return res, fmt.Errorf("%s %w: move it, or choose another --prefix", target, ErrForeign)
	case old.Version == e.Version:
		prev = old.Previous
	default:
		if err := keep(filepath.Join(e.Roots.Stack, "self"), target, old.Version, exeSuffix(e.Target) != ""); err != nil {
			return res, err
		}
		prev = &old.Version
	}
	if !Same(e.Exe, target) {
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return res, err
		}
		tmp, err := copyTemp(e.Exe, filepath.Dir(target), e.Target)
		if err != nil {
			return res, err
		}
		if err := os.Rename(tmp, target); err != nil {
			_ = os.Remove(tmp)
			return res, err
		}
	}
	rec := &state.Receipt{Method: "script", BinPath: e.Roots.Portable(target), Version: e.Version, Target: e.Target,
		InstalledAt: e.Now().UTC(), Previous: prev}
	if err := state.SaveReceipt(e.ReceiptPath(), rec); err != nil {
		return res, err
	}
	if prev != nil {
		res.Previous = *prev
	}
	res.OnPath = slices.ContainsFunc(filepath.SplitList(e.Getenv("PATH")), func(d string) bool { return d != "" && Same(d, filepath.Dir(target)) })
	return res, nil
}
