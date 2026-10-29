// Package selfupdate installs, updates and rolls back the makeit binary (cli/SELF-UPDATE.md): the
// receipt gate, a release checked against the keys compiled into the running binary, the swap, and
// the last two binaries kept for a rollback.
package selfupdate

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"makeit/internal/execx"
	"makeit/internal/paths"
	"makeit/internal/state"
)

// Env is what the self commands read from the machine; tests replace each field.
type Env struct {
	Roots paths.Roots
	// Exe is this executable, symlinks resolved.
	Exe                   string
	Version, Target, Repo string
	Keys                  []string
	// Base is where releases are: https://github.com unless MAKEIT_RELEASES_URL says otherwise.
	Base   string
	Client *http.Client
	Run    execx.Runner
	// Windows swaps by renaming the running binary aside first.
	Windows bool
	Getenv  func(string) string
	Now     func() time.Time
}

var (
	// ErrDev is a development build: it has no release keys to check a new binary with.
	ErrDev = errors.New("this makeit is a development build: it does not update itself")
	// ErrNoReceipt is a makeit that its install script did not install.
	ErrNoReceipt = errors.New("makeit was not installed by its install script")
	// ErrOtherPath is a receipt for another copy of makeit.
	ErrOtherPath = errors.New("makeit's receipt is for another copy")
)

// Dev reports a development build: no release version, repository or keys.
func (e Env) Dev() bool { return e.Version == "dev" || e.Repo == "" || len(e.Keys) == 0 }

// ReceiptPath is stack:receipt.json.
func (e Env) ReceiptPath() string { return filepath.Join(e.Roots.Stack, "receipt.json") }

// Gate returns the receipt when this binary may change itself: a release build, installed by its
// install script at this path.
func (e Env) Gate() (*state.Receipt, error) {
	if e.Dev() {
		return nil, ErrDev
	}
	rec, _, err := state.LoadReceipt(e.ReceiptPath())
	if err != nil {
		return nil, err
	}
	if rec == nil || rec.Method != "script" {
		return nil, fmt.Errorf("%w: run `%s self install` first", ErrNoReceipt, e.Exe)
	}
	if bin := e.Roots.Abs(rec.BinPath); !Same(bin, e.Exe) {
		return nil, fmt.Errorf("%w: the receipt names %s, this makeit is %s", ErrOtherPath, bin, e.Exe)
	}
	return rec, nil
}

// Same reports whether a and b are the same path once symlinks are resolved.
func Same(a, b string) bool { return canonical(a) == canonical(b) }

func canonical(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	p = filepath.Clean(p)
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return p
}

// Base is the releases base: MAKEIT_RELEASES_URL when set, which must be an https URL, else def.
func Base(getenv func(string) string, def string) (string, error) {
	v := getenv("MAKEIT_RELEASES_URL")
	if v == "" {
		return def, nil
	}
	if u, err := url.Parse(v); err != nil || u.Scheme != "https" || u.Host == "" {
		return "", fmt.Errorf("MAKEIT_RELEASES_URL must be an https URL, not %q", v)
	}
	return strings.TrimRight(v, "/"), nil
}

// Status is what version and doctor show about the receipt.
type Status struct {
	// Receipt is true when receipt.json exists.
	Receipt bool
	// BinPath is the binary the receipt names; Ours is true when that is this executable.
	BinPath string
	Ours    bool
	// Skipped is the version a rollback left.
	Skipped string
	Err     error
}

// Status reads the receipt, without the gate's dev rule.
func (e Env) Status() Status {
	rec, info, err := state.LoadReceipt(e.ReceiptPath())
	if err != nil {
		return Status{Receipt: info.Found, Err: err}
	}
	if rec == nil {
		return Status{}
	}
	bin := e.Roots.Abs(rec.BinPath)
	return Status{Receipt: true, BinPath: bin, Ours: Same(bin, e.Exe), Skipped: rec.Skipped}
}
