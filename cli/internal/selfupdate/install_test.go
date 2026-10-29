package selfupdate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"makeit/internal/minisign/minisigntest"
	"makeit/internal/state"
)

// updated is installed(t) after an update to 1.2.0, seen from the new binary: Env.Version is 1.2.0
// and its check answers for the kept 1.1.0.
func updated(t *testing.T) Env {
	t.Helper()
	k := minisigntest.NewKey()
	e, _ := installed(t, k, releaseFiles(k, "1.2.0", "", []byte("new 1.2.0")))
	if _, err := e.Update(context.Background(), "1.2.0", false); err != nil {
		t.Fatal(err)
	}
	e.Version, e.Run = "1.2.0", answers("1.1.0", `["ABC"]`)
	return e
}

func TestRollback(t *testing.T) {
	e := updated(t)
	if p, err := e.Previous(); err != nil || p != "1.1.0" {
		t.Fatalf("previous %q %v", p, err)
	}
	res, err := e.Rollback(context.Background())
	if err != nil || res != (Result{From: "1.2.0", To: "1.1.0", BinPath: e.Exe}) {
		t.Fatalf("%+v %v", res, err)
	}
	if got := read(t, e.Exe); got != "old" {
		t.Errorf("binary %q", got)
	}
	if r := receiptOf(t, e); r.Version != "1.1.0" || r.Skipped != "1.2.0" || r.Previous != nil {
		t.Errorf("receipt %+v", r)
	}
	if entries, _ := os.ReadDir(filepath.Join(e.Roots.Stack, "self")); len(entries) != 0 {
		t.Errorf("self/ holds %v", entries)
	}
	if left := newFiles(t, filepath.Dir(e.Exe)); len(left) > 0 {
		t.Errorf("left %v", left)
	}
}

func TestRollbackKeepsHigherSkip(t *testing.T) {
	e := updated(t)
	r := receiptOf(t, e)
	r.Skipped = "1.3.0"
	writeReceipt(t, e, *r)
	if _, err := e.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := receiptOf(t, e).Skipped; got != "1.3.0" {
		t.Errorf("skip %q, want 1.3.0", got)
	}
}

func TestRollbackNothing(t *testing.T) {
	e := machine(t)
	writeReceipt(t, e, ours(e))
	if _, err := e.Rollback(context.Background()); !errors.Is(err, ErrNothing) || err.Error() != "nothing to roll back to" {
		t.Errorf("no previous: %v", err)
	}
	r := ours(e)
	prev := "1.0.0"
	r.Previous = &prev
	writeReceipt(t, e, r)
	if _, err := e.Previous(); !errors.Is(err, ErrNothing) {
		t.Errorf("previous without its file: %v", err)
	}
	if _, err := e.Rollback(context.Background()); !errors.Is(err, ErrNothing) {
		t.Errorf("rollback without the file: %v", err)
	}
}

func TestRollbackCheckFails(t *testing.T) {
	e := updated(t)
	e.Run = answers("1.0.0", `["ABC"]`)
	before := read(t, e.ReceiptPath())
	if _, err := e.Rollback(context.Background()); err == nil {
		t.Fatal("rolled back to a binary that reports another version")
	}
	if read(t, e.Exe) != "new 1.2.0" || read(t, e.ReceiptPath()) != before || read(t, filepath.Join(e.Roots.Stack, "self", "makeit-1.1.0")) != "old" {
		t.Error("a failed rollback changed something")
	}
	if left := newFiles(t, filepath.Dir(e.Exe)); len(left) > 0 {
		t.Errorf("left %v", left)
	}
}

func TestInstallTarget(t *testing.T) {
	e := machine(t)
	if got, want := e.InstallTarget(""), filepath.Join(e.Roots.Home, ".local", "bin", "makeit"); got != want {
		t.Errorf("default %q, want %q", got, want)
	}
	e.Target = "windows-x64"
	if got := e.InstallTarget("/opt/bin"); got != filepath.Join("/opt/bin", "makeit.exe") {
		t.Errorf("windows %q", got)
	}
}

func TestInstall(t *testing.T) {
	type check func(t *testing.T, e Env, target string, res InstallResult)
	cases := []struct {
		name    string
		prepare func(t *testing.T, e *Env, target string)
		err     error
		check   check
	}{
		{"fresh", nil, nil, func(t *testing.T, e Env, target string, res InstallResult) {
			fi, err := os.Stat(target)
			if err != nil || fi.Mode().Perm() != 0o755 || read(t, target) != "new" {
				t.Errorf("target %v %v", fi, err)
			}
			r := receiptOf(t, e)
			if r.Method != "script" || r.BinPath != e.Roots.Portable(target) || r.Version != "1.1.0" || r.Target != "linux-x64" ||
				!r.InstalledAt.Equal(e.Now()) || r.Previous != nil {
				t.Errorf("receipt %+v", r)
			}
			if res.OnPath || res.Path != target || res.Previous != "" || res.OldPath != "" {
				t.Errorf("result %+v", res)
			}
		}},
		{"prefix on PATH", func(t *testing.T, e *Env, target string) {
			e.Getenv = func(k string) string {
				if k == "PATH" {
					return "/usr/bin" + string(os.PathListSeparator) + filepath.Dir(target)
				}
				return ""
			}
		}, nil, func(t *testing.T, _ Env, _ string, res InstallResult) {
			if !res.OnPath {
				t.Error("OnPath false")
			}
		}},
		{"source is the target", func(t *testing.T, e *Env, target string) {
			write(t, target, "running")
			e.Exe = target
		}, nil, func(t *testing.T, e Env, target string, _ InstallResult) {
			if read(t, target) != "running" || receiptOf(t, e).BinPath != e.Roots.Portable(target) {
				t.Error("not just the receipt")
			}
		}},
		{"over our own", func(t *testing.T, e *Env, target string) {
			write(t, target, "1.0.0 bytes")
			writeReceipt(t, *e, state.Receipt{Method: "script", BinPath: e.Roots.Portable(target), Version: "1.0.0", Skipped: "1.0.5"})
		}, nil, func(t *testing.T, e Env, target string, res InstallResult) {
			r := receiptOf(t, e)
			if read(t, target) != "new" || read(t, filepath.Join(e.Roots.Stack, "self", "makeit-1.0.0")) != "1.0.0 bytes" ||
				res.Previous != "1.0.0" || r.Previous == nil || *r.Previous != "1.0.0" || r.Skipped != "" {
				t.Errorf("result %+v, receipt %+v", res, r)
			}
		}},
		{"foreign file", func(t *testing.T, _ *Env, target string) { write(t, target, "someone else's") }, ErrForeign,
			func(t *testing.T, e Env, target string, _ InstallResult) {
				if read(t, target) != "someone else's" {
					t.Error("foreign file changed")
				}
				if _, err := os.Stat(e.ReceiptPath()); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("receipt written: %v", err)
				}
			}},
		{"earlier receipt for another path", func(t *testing.T, e *Env, _ string) {
			write(t, filepath.Join(e.Roots.Home, "elsewhere", "makeit"), "older")
			writeReceipt(t, *e, state.Receipt{Method: "script", BinPath: "home:elsewhere/makeit", Version: "1.0.0"})
		}, nil, func(t *testing.T, e Env, _ string, res InstallResult) {
			old := filepath.Join(e.Roots.Home, "elsewhere", "makeit")
			if res.OldPath != old || read(t, old) != "older" || res.Previous != "" {
				t.Errorf("result %+v", res)
			}
		}},
		{"dev build", func(_ *testing.T, e *Env, _ string) { e.Version = "dev" }, ErrDev, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := machine(t)
			e.Exe = filepath.Join(t.TempDir(), "downloaded")
			write(t, e.Exe, "new")
			e.Getenv = func(k string) string {
				if k == "PATH" {
					return "/usr/bin"
				}
				return ""
			}
			prefix := filepath.Join(t.TempDir(), "bin")
			target := e.InstallTarget(prefix)
			if c.prepare != nil {
				c.prepare(t, &e, target)
			}
			res, err := e.Install(context.Background(), prefix)
			if !errors.Is(err, c.err) {
				t.Fatalf("err %v, want %v", err, c.err)
			}
			if c.check != nil {
				c.check(t, e, target, res)
			}
		})
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}
