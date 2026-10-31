package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"makeit/internal/execx"
	"makeit/internal/fsx"
)

// docsMachine is a stack directory, a Go project and a fake docs-mcp-server; onScrape runs
// inside each scrape, while the sync is still going.
type docsMachine struct {
	stack, dir string
	mu         sync.Mutex
	calls      []string
	onScrape   func()
}

func newDocsMachine(t *testing.T) *docsMachine {
	t.Helper()
	m := &docsMachine{stack: t.TempDir(), dir: t.TempDir()}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MAKEIT_DIR", m.stack)
	write(t, filepath.Join(m.dir, "go.mod"), "module x\n\ngo 1.27.3\n\nrequire golang.org/x/term v0.47.0\n")
	saved := docsRun
	docsRun = func(_ context.Context, c execx.Cmd) (execx.Result, error) {
		m.mu.Lock()
		m.calls = append(m.calls, strings.Join(c.Argv, " "))
		m.mu.Unlock()
		switch {
		case strings.Contains(strings.Join(c.Argv, " "), " list "):
			return execx.Result{Stdout: "[]"}, nil
		case strings.Contains(strings.Join(c.Argv, " "), " scrape ") && m.onScrape != nil:
			m.onScrape()
		}
		return execx.Result{Stdout: "Successfully scraped 1 pages"}, nil
	}
	t.Cleanup(func() { docsRun = saved })
	return m
}

func (m *docsMachine) take() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.calls
	m.calls = nil
	return c
}

// Review Focus 4: a trigger during a running sync exits at once, an unchanged manifest costs
// nothing, and an edit made while the sync ran is caught by the next trigger.
func TestSyncIfChangedLockAndMidSyncEdit(t *testing.T) {
	m := newDocsMachine(t)
	lock, err := fsx.Acquire(filepath.Join(m.stack, "docs", "lock"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := run2(t, "docs", "sync", "--if-changed", "--quiet", m.dir); code != 0 || out+errOut != "" || len(m.take()) != 0 {
		t.Fatalf("locked: exit %d %q %q", code, out, errOut)
	}
	lock.Release()
	// The stack run lock (setup, update, init) does not stop a docs sync, and the sync leaves it alone.
	run, err := fsx.Acquire(filepath.Join(m.stack, "lock"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer run.Release()
	if code, _, errOut := run2(t, "docs", "sync", m.dir); code != 0 {
		t.Fatalf("first: exit %d %s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(m.stack, "lock")); err != nil {
		t.Fatalf("the docs sync removed the run lock: %v", err)
	}
	if c := m.take(); len(c) != 2 || !strings.Contains(c[1], "scrape golang.org/x/term https://pkg.go.dev/golang.org/x/term@v0.47.0 --version v0.47.0") {
		t.Fatalf("first calls %q", c)
	}
	if code, _, _ := run2(t, "docs", "sync", "--if-changed", "--quiet", m.dir); code != 0 || len(m.take()) != 0 {
		t.Fatal("unchanged manifests synced again")
	}
	write(t, filepath.Join(m.dir, "go.mod"), "module x\n\ngo 1.27.3\n\nrequire golang.org/x/term v0.48.0\n")
	m.onScrape = func() {
		write(t, filepath.Join(m.dir, "go.mod"), "module x\n\ngo 1.27.3\n\nrequire golang.org/x/term v0.49.0\n")
	}
	if code, _, _ := run2(t, "docs", "sync", "--if-changed", "--quiet", m.dir); code != 0 || len(m.take()) != 2 {
		t.Fatal("a changed manifest was not synced")
	}
	m.onScrape = nil
	if code, _, _ := run2(t, "docs", "sync", "--if-changed", "--quiet", m.dir); code != 0 {
		t.Fatal("exit")
	}
	if c := m.take(); len(c) != 2 || !strings.Contains(c[1], "--version v0.49.0") {
		t.Fatalf("the edit made during the sync was not caught: %q", c)
	}
}

// A sync that failed saves no record, so the next --if-changed trigger tries again.
func TestDocsSyncFailureRetries(t *testing.T) {
	m := newDocsMachine(t)
	ok := docsRun
	docsRun = func(ctx context.Context, c execx.Cmd) (execx.Result, error) {
		_, _ = ok(ctx, c)
		return execx.Result{Code: 1, Stderr: "npx: network down"}, nil
	}
	if code, out, _ := run2(t, "docs", "sync", m.dir); code != 0 || !strings.Contains(out, "not indexed golang.org/x/term") {
		t.Fatalf("failed sync: exit %d %q", code, out)
	}
	m.take()
	docsRun = ok
	if code, _, _ := run2(t, "docs", "sync", "--if-changed", "--quiet", m.dir); code != 0 {
		t.Fatal("exit")
	}
	if c := m.take(); len(c) != 2 {
		t.Fatalf("the failed sync was not retried: %q", c)
	}
}

func TestDocsSyncDryRunWritesNothing(t *testing.T) {
	m := newDocsMachine(t)
	code, out, errOut := run2(t, "docs", "sync", "--dry-run", m.dir)
	if code != 0 || !strings.Contains(out+errOut, "golang.org/x/term v0.47.0") || strings.Contains(out+errOut, "go 1.27.3") || len(m.take()) != 0 {
		t.Fatalf("exit %d %q %q", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(m.stack, "docs")); !os.IsNotExist(err) {
		t.Error("a dry run wrote the docs record")
	}
}

func TestDocsUsage(t *testing.T) {
	newDocsMachine(t)
	if code, _, errOut := run2(t, "docs"); code != 2 || !strings.Contains(errOut, "docs sync") {
		t.Errorf("no subcommand: exit %d %q", code, errOut)
	}
	if code, _, errOut := run2(t, "docs", "sync", "a", "b"); code != 2 {
		t.Errorf("two dirs: exit %d %q", code, errOut)
	}
}
