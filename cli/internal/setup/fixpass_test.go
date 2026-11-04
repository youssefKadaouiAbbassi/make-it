package setup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"makeit/internal/catalog"
	"makeit/internal/claudecli"
	"makeit/internal/execx"
	"makeit/internal/plan"
	"makeit/internal/platform"
	"makeit/internal/state"
)

// Distro suite, 2026-11-04: the newest releases were looked up before setup had installed git, so a
// fresh machine got the shipped set and the second setup moved it. The lookup waits for git.
func TestResolveWaitsForGit(t *testing.T) {
	e, _ := newEnv(t, "apt-get")
	calls := 0
	moved, err := catalog.TreeOf(map[string][]byte{"x": []byte("newest")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.Resolve = func(context.Context) Resolved { calls++; return Resolved{Tree: moved, Notes: []string{"n"}} }
	if _, err := e.catalogTree(context.Background()); err != nil || calls != 0 {
		t.Fatalf("resolved without git: calls %d err %v", calls, err)
	}
	e.Present = only("apt-get", "git")
	for range 2 {
		got, err := e.catalogTree(context.Background())
		if err != nil || got != moved {
			t.Fatalf("tree %v err %v, want the resolved one", got, err)
		}
	}
	if calls != 1 || !slices.Contains(e.notes, "n") {
		t.Errorf("calls %d notes %v, want one lookup and its notes kept", calls, e.notes)
	}
}

// Review C1: a newest tag with no asset (or a failed checksum) keeps the tool and does not stop the run.
func TestToolUpgradeFetchFailureKeeps(t *testing.T) {
	e, f, path := installedPrek(t, "0.5.9", []byte("prek 0.5.9\n"))
	prekReleases(t, e, f, "b\trefs/tags/v0.6.0\n", nil) // tagged, nothing uploaded yet
	s, _ := upgradeStep(t, e, "prek")
	if done, _ := s.Already(context.Background()); done {
		t.Fatal("already")
	}
	if _, err := s.Run(context.Background()); !errors.Is(err, plan.ErrSkip) {
		t.Fatalf("err %v, want a skip, not a failure", err)
	}
	if r := keptReason(e, "prek"); !strings.Contains(r, "kept at 0.5.9") {
		t.Errorf("kept %q", r)
	}
	if b, _ := os.ReadFile(path); string(b) != "prek 0.5.9\n" {
		t.Errorf("binary %q", b)
	}
}

// Review I2: offline, a missing release tool is left to the user, not planned to fail.
func TestOfflineReleaseRouteNeedsYou(t *testing.T) {
	e, _ := newEnv(t, "apt-get", "git", "node", "jq")
	e.Offline = true
	if slices.Contains(stepIDs(e), "prereq.prek") {
		t.Errorf("prek planned offline: %v", stepIDs(e))
	}
	if !slices.ContainsFunc(e.needsYou, func(n NeedsYou) bool { return n.ID == "prek" }) {
		t.Errorf("needs you %+v, want prek", e.needsYou)
	}
}

// Review I3: rollback passes over a run that left nothing to undo.
func TestRollbackSkipsEmptyGeneration(t *testing.T) {
	stack := t.TempDir()
	g1, err := state.NewGeneration(stack, 0, "update", nil)
	if err != nil {
		t.Fatal(err)
	}
	g1.Manifest.Ops = []state.Op{{ID: "file.x", Kind: "write", Path: "home:x"}}
	if err := g1.Save(); err != nil {
		t.Fatal(err)
	}
	g2, err := state.NewGeneration(stack, g1.Manifest.Generation, "update", nil)
	if err != nil {
		t.Fatal(err)
	}
	g2.Manifest.Changes = []state.Change{{Kind: "claude-code", Item: "Claude Code", From: "1", To: "2"}}
	if err := g2.Save(); err != nil {
		t.Fatal(err)
	}
	g, err := lastUndoable(stack)
	if err != nil || g.Manifest.Generation != g1.Manifest.Generation {
		t.Fatalf("picked %v (%v), want generation %d", g, err, g1.Manifest.Generation)
	}
}

// Review I4: with no one to type a password (--yes, a pipe, the daily run), a sudo route is a note.
func TestClaudeUpgradeNoPromptSkipsSudo(t *testing.T) {
	e, f := upgradeEnv(t, "apt", "2.1.317", "2.1.320", "2.1.320")
	e.Yes, e.Interactive = true, false
	if ran, _ := runUpgrade(t, e); ran {
		t.Error("upgraded through sudo -n")
	}
	if slices.ContainsFunc(f.argvs(), func(c string) bool { return strings.Contains(c, "apt-get") }) {
		t.Error("ran apt-get")
	}
	if r := keptReason(e, "claude-code"); !strings.Contains(r, "2.1.320") {
		t.Errorf("kept %q", r)
	}
}

// Review I4: an upgrade that fails is a note, not a stopped update.
func TestClaudeUpgradeFailureKeeps(t *testing.T) {
	e, f := upgradeEnv(t, "native", "2.1.317", "2.1.320", "2.1.320")
	f.answers["sh -c "+e.shell("claude update")] = func(execx.Cmd) execx.Result { return execx.Result{Code: 1, Stderr: "network down"} }
	_, err := runUpgrade(t, e)
	if !errors.Is(err, plan.ErrSkip) {
		t.Fatalf("err %v, want a skip", err)
	}
	if r := keptReason(e, "claude-code"); !strings.Contains(r, "2.1.320") {
		t.Errorf("kept %q", r)
	}
}

// Review I5: npm under a system prefix needs root like a package manager.
func TestClaudeUpgradeNpmSystemPrefix(t *testing.T) {
	e, _ := upgradeEnv(t, "npm", "2.1.317", "2.1.320", "2.1.320")
	found := []claudecli.Install{{Path: "/usr/local/bin/claude", Source: "npm", OnPath: true}}
	e.Locate = func(context.Context) []claudecli.Install { return found }
	e.Background = true
	if ran, _ := runUpgrade(t, e); ran {
		t.Error("the daily run upgraded a system npm install")
	}
}

// Review I6: a Claude Code that updated itself is recorded by its own step, even on a quiet run.
func TestClaudeSeenStep(t *testing.T) {
	e, _ := upgradeEnv(t, "native", "2.1.318", "2.1.318", "2.1.318")
	e.st.ClaudeCode.VersionSeen = "2.1.317"
	s := e.claudeSeenStep()
	if done, _ := s.Already(context.Background()); done {
		t.Fatal("a moved version counts as already")
	}
	if _, err := s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := state.Change{Kind: "claude-code", Item: "Claude Code", From: "2.1.317", To: "2.1.318"}
	if !slices.Contains(e.changes, want) || e.st.ClaudeCode.VersionSeen != "2.1.318" {
		t.Errorf("changes %+v seen %q", e.changes, e.st.ClaudeCode.VersionSeen)
	}
	if done, _ := s.Already(context.Background()); !done {
		t.Error("recorded twice")
	}
}

// Review I7: an undone tool release is passed over by the daily run only, as for plugins.
func TestToolSkipsOnlyInDailyRun(t *testing.T) {
	e, f, _ := installedPrek(t, "0.5.9", []byte("prek 0.5.9\n"))
	prekReleases(t, e, f, "a\trefs/tags/v0.5.9\nb\trefs/tags/v0.6.0\n", nil)
	e.st.ToolSkips = map[string][]string{"prek": {"v0.6.0"}}
	if v, _ := e.newestRelease(context.Background(), "prek"); v != "0.6.0" {
		t.Errorf("by hand: %q, want 0.6.0", v)
	}
	d, f2, _ := installedPrek(t, "0.5.9", []byte("prek 0.5.9\n"))
	prekReleases(t, d, f2, "a\trefs/tags/v0.5.9\nb\trefs/tags/v0.6.0\n", nil)
	d.st.ToolSkips = map[string][]string{"prek": {"v0.6.0"}}
	d.Background = true
	if v, _ := d.newestRelease(context.Background(), "prek"); v != "0.5.9" {
		t.Errorf("daily: %q, want 0.5.9", v)
	}
}

// Review M8: one item moved twice in a run is one change, first version to last.
func TestReportChangesMergesSameItem(t *testing.T) {
	got := reportChanges([]state.Change{
		{Kind: "claude-code", Item: "Claude Code", From: "2.1.310", To: "2.1.315"},
		{Kind: "claude-code", Item: "Claude Code", From: "2.1.315", To: "2.1.320"},
	}, nil, nil, false)
	if want := []state.Change{{Kind: "claude-code", Item: "Claude Code", From: "2.1.310", To: "2.1.320"}}; !slices.Equal(got, want) {
		t.Errorf("changes %+v", got)
	}
}

// Review M10: the plan shows the command the upgrade runs, sudo included.
func TestClaudeUpgradeShowsCommand(t *testing.T) {
	e, _ := upgradeEnv(t, "native", "2.1.317", "2.1.320", "2.1.320")
	if s := e.claudeUpgradeStep(); !strings.Contains(strings.Join(s.Command, " "), "claude update") {
		t.Errorf("command %v", s.Command)
	}
}

var _ = filepath.Join
var _ = platform.APT

// The first version seen is recorded in the apply pass, not listed as a move.
func TestClaudeSeenFirstRunRecords(t *testing.T) {
	e, _ := upgradeEnv(t, "native", "2.1.318", "2.1.318", "2.1.318")
	if done, _ := e.claudeSeenStep().Already(context.Background()); !done {
		t.Error("a first run has a move to record")
	}
	if e.st.ClaudeCode.VersionSeen != "2.1.318" || len(e.changes) != 0 {
		t.Errorf("seen %q changes %+v", e.st.ClaudeCode.VersionSeen, e.changes)
	}
}
