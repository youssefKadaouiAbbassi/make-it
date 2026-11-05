package state

import (
	"strconv"
	"testing"
)

// Daily runs that only noticed Claude Code moving (nothing to undo) must not push the one run
// rollback can undo out of the history (the 2026-11-05 audit).
func TestPruneKeepsTheUndoableRun(t *testing.T) {
	stack := t.TempDir()
	g, err := NewGeneration(stack, 0, "update", nil)
	if err != nil {
		t.Fatal(err)
	}
	pre := "sha256:x"
	if err := g.Record(Op{ID: "settings", Kind: "write", Path: "claude:settings.json", PreSha256: &pre}); err != nil {
		t.Fatal(err)
	}
	last := g.Manifest.Generation
	for i := range Keep + 2 {
		n, err := NewGeneration(stack, last, "update --auto", nil)
		if err != nil {
			t.Fatal(err)
		}
		n.Manifest.Changes = []Change{{Kind: "claude-code", Item: "Claude Code", From: "2.1." + strconv.Itoa(i), To: "2.1." + strconv.Itoa(i+1)}}
		if err := n.Save(); err != nil {
			t.Fatal(err)
		}
		last = n.Manifest.Generation
		if err := Prune(stack); err != nil {
			t.Fatal(err)
		}
	}
	gens, err := LoadGenerations(stack)
	if err != nil {
		t.Fatal(err)
	}
	if len(gens) != 2 || gens[0].Manifest.Generation != last || gens[1].Manifest.Generation != g.Manifest.Generation {
		var ns []int
		for _, x := range gens {
			ns = append(ns, x.Manifest.Generation)
		}
		t.Errorf("generations %v, want the newest (%d) and the undoable one (%d)", ns, last, g.Manifest.Generation)
	}
}

// Undoable runs still keep only the newest Keep.
func TestPruneCapsUndoableRuns(t *testing.T) {
	stack := t.TempDir()
	last := 0
	for range Keep + 3 {
		g, err := NewGeneration(stack, last, "update", nil)
		if err != nil {
			t.Fatal(err)
		}
		pre := "sha256:x"
		if err := g.Record(Op{ID: "s", Kind: "write", Path: "claude:settings.json", PreSha256: &pre}); err != nil {
			t.Fatal(err)
		}
		last = g.Manifest.Generation
	}
	if err := Prune(stack); err != nil {
		t.Fatal(err)
	}
	if gens, _ := LoadGenerations(stack); len(gens) != Keep {
		t.Errorf("%d generations, want %d", len(gens), Keep)
	}
}

func TestManifestUndoable(t *testing.T) {
	for _, c := range []struct {
		m    Manifest
		want bool
	}{
		{Manifest{}, false},
		{Manifest{Ops: []Op{{ID: "x"}}}, true},
		{Manifest{Pins: []PinMove{{}}}, true},
		{Manifest{Changes: []Change{{Kind: "tool"}}}, true},
		{Manifest{Changes: []Change{{Kind: "mcp"}}}, true},
		{Manifest{Changes: []Change{{Kind: "claude-code"}, {Kind: "npm"}, {Kind: "makeit"}}}, false},
	} {
		if got := c.m.Undoable(); got != c.want {
			t.Errorf("%+v: %v, want %v", c.m, got, c.want)
		}
	}
}
