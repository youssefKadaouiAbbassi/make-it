package selfupdate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"makeit/internal/paths"
	"makeit/internal/state"
)

// machine is a temp home with a stack dir and makeit 1.1.0 at ~/.local/bin/makeit.
func machine(t *testing.T) Env {
	t.Helper()
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(bin, "makeit")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	roots := paths.Roots{Home: home, Claude: filepath.Join(home, ".claude"), Stack: filepath.Join(home, ".makeit")}
	if err := os.MkdirAll(roots.Stack, 0o700); err != nil {
		t.Fatal(err)
	}
	return Env{Roots: roots, Exe: exe, Version: "1.1.0", Target: "linux-x64", Repo: "o/makeit", Keys: []string{"key"},
		Getenv: func(string) string { return "" }, Now: func() time.Time { return time.Unix(1_800_000_000, 0).UTC() }}
}

// ours is a receipt for e's binary.
func ours(e Env) state.Receipt {
	return state.Receipt{Method: "script", BinPath: e.Roots.Portable(e.Exe), Version: e.Version, Target: e.Target}
}

func writeReceipt(t *testing.T, e Env, r state.Receipt) {
	t.Helper()
	if err := state.SaveReceipt(e.ReceiptPath(), &r); err != nil {
		t.Fatal(err)
	}
}

func TestGate(t *testing.T) {
	other := func(e Env) *state.Receipt { r := ours(e); r.BinPath = "home:bin/makeit"; return &r }
	cases := []struct {
		name    string
		edit    func(*Env)
		receipt func(Env) *state.Receipt
		want    error
		says    func(Env) []string
	}{
		{"dev version", func(e *Env) { e.Version = "dev" }, nil, ErrDev,
			func(Env) []string { return []string{"this makeit is a development build: it does not update itself"} }},
		{"no repo", func(e *Env) { e.Repo = "" }, nil, ErrDev, nil},
		{"no keys", func(e *Env) { e.Keys = nil }, nil, ErrDev, nil},
		{"no receipt", nil, nil, ErrNoReceipt,
			func(e Env) []string { return []string{"run `" + e.Exe + " self install` first"} }},
		{"installed another way", nil, func(e Env) *state.Receipt { r := ours(e); r.Method = "brew"; return &r }, ErrNoReceipt, nil},
		{"another path", nil, other, ErrOtherPath,
			func(e Env) []string { return []string{e.Roots.Abs("home:bin/makeit"), e.Exe} }},
		{"ours", nil, func(e Env) *state.Receipt { r := ours(e); return &r }, nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := machine(t)
			if c.edit != nil {
				c.edit(&e)
			}
			if c.receipt != nil {
				writeReceipt(t, e, *c.receipt(e))
			}
			rec, err := e.Gate()
			if !errors.Is(err, c.want) {
				t.Fatalf("err %v, want %v", err, c.want)
			}
			if c.want == nil && rec == nil {
				t.Fatal("no receipt returned")
			}
			if c.says != nil {
				for _, s := range c.says(e) {
					if !strings.Contains(err.Error(), s) {
						t.Errorf("%q does not say %q", err, s)
					}
				}
			}
		})
	}
}

// ~/.local/bin is a symlink, so the receipt's path and the resolved executable differ until both
// are resolved.
func TestGateSymlinkedBinDir(t *testing.T) {
	e := machine(t)
	link := filepath.Dir(e.Exe)
	real := filepath.Join(e.Roots.Home, "real-bin")
	if err := os.Rename(link, real); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	r := ours(e) // home:.local/bin/makeit, through the link
	e.Exe = filepath.Join(real, "makeit")
	writeReceipt(t, e, r)
	if _, err := e.Gate(); err != nil {
		t.Fatalf("symlinked bin dir: %v", err)
	}
}

func TestStatus(t *testing.T) {
	e := machine(t)
	if s := e.Status(); s.Receipt || s.Err != nil {
		t.Errorf("no receipt: %+v", s)
	}
	r := ours(e)
	r.Skipped = "1.3.0"
	writeReceipt(t, e, r)
	e.Version = "dev" // Status has no dev rule
	if s := e.Status(); !s.Receipt || !s.Ours || s.Skipped != "1.3.0" || s.BinPath != e.Exe || s.Err != nil {
		t.Errorf("ours: %+v", s)
	}
	r.BinPath = "home:bin/makeit"
	writeReceipt(t, e, r)
	if s := e.Status(); !s.Receipt || s.Ours || s.BinPath != e.Roots.Abs("home:bin/makeit") {
		t.Errorf("another path: %+v", s)
	}
	if err := os.WriteFile(e.ReceiptPath(), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s := e.Status(); s.Err == nil {
		t.Errorf("unreadable: %+v", s)
	}
}

func TestBase(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == "MAKEIT_RELEASES_URL" {
				return v
			}
			return ""
		}
	}
	for v, want := range map[string]string{"": "https://github.com", "https://127.0.0.1:8443": "https://127.0.0.1:8443"} {
		if b, err := Base(env(v), "https://github.com"); err != nil || b != want {
			t.Errorf("%q: %q %v, want %q", v, b, err, want)
		}
	}
	for _, bad := range []string{"http://x", "::bad"} {
		if _, err := Base(env(bad), "https://github.com"); err == nil || !strings.Contains(err.Error(), "MAKEIT_RELEASES_URL") {
			t.Errorf("%q: %v, want an error naming MAKEIT_RELEASES_URL", bad, err)
		}
	}
}
