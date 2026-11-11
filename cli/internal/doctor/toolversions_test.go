//go:build !windows

package doctor

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"makeit/internal/platform"
)

// A release tool that prints nothing, fails or hangs still gets its line: its version is "" and
// doctor does not wait on it. One that answers gives the first x.y.z it prints.
func TestToolVersions(t *testing.T) {
	bin := t.TempDir()
	script := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	bad := []string{"exit 0", "printf 'tool 9.9.9\\n'; exit 3", "sleep 30"}
	var states []platform.State
	for _, p := range platform.Prereqs {
		if p.Release == nil {
			continue
		}
		if len(states) == len(bad) {
			t.Fatalf("more release tools than cases: add one for %s", p.ID)
		}
		script(p.Release.Bin, bad[len(states)])
		states = append(states, platform.State{Prereq: p, Present: true})
	}
	if len(states) != len(bad) {
		t.Fatalf("%d release tools, %d cases: every case needs a tool", len(states), len(bad))
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	start := time.Now()
	got := toolVersions(t.Context(), states)
	if d := time.Since(start); d > 8*time.Second {
		t.Errorf("toolVersions took %s: a hanging tool must be cut at its limit", d)
	}
	for i, s := range states {
		if v, listed := got[s.Prereq.ID]; v != "" || !listed {
			t.Errorf("%s (%s): version %q listed %v, want an empty version", s.Prereq.ID, bad[i], v, listed)
		}
	}
	script(states[0].Prereq.Release.Bin, "printf 'the tool, version 1.2.3 (build 7)\\n'")
	if got := toolVersions(t.Context(), states[:1]); got[states[0].Prereq.ID] != "1.2.3" {
		t.Errorf("version %q, want 1.2.3", got[states[0].Prereq.ID])
	}
}
