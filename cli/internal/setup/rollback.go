package setup

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"makeit/internal/catalog"
	"makeit/internal/fsx"
	"makeit/internal/plan"
	"makeit/internal/state"
)

// ErrNothingToRollBack: no run has changed this machine since the last rollback.
var ErrNothingToRollBack = errors.New("nothing to roll back: no update has changed this machine yet")

// LastUndoable is the run makeit rollback would undo.
func LastUndoable(stack string) (*state.Generation, error) { return lastUndoable(stack) }

// lastUndoable is the newest generation that a rollback has not undone yet. A rollback's own
// generation is not undone by the next rollback: that one goes one run further back.
func lastUndoable(stack string) (*state.Generation, error) {
	gens, err := state.LoadGenerations(stack)
	if err != nil {
		return nil, err
	}
	var pick *state.Generation
	for _, g := range gens {
		if !g.Manifest.RolledBack && g.Manifest.Command != "rollback" && g.Manifest.Undoable() {
			pick = g
			break
		}
	}
	// A newer run whose record cannot be read may be the one to undo: stop rather than undo an
	// older run as if it were the last.
	bad, err := state.Unreadable(stack)
	if err != nil {
		return nil, err
	}
	if n := len(bad); n > 0 && (pick == nil || bad[n-1] > pick.Manifest.Generation) {
		dir := filepath.Join(state.GenerationsDir(stack), strconv.Itoa(bad[n-1]))
		return nil, fmt.Errorf("the record of run %d cannot be read (%s), so rollback stops rather than undo an older run in its place: fix or remove that folder", bad[n-1], filepath.Join(dir, "manifest.json"))
	}
	if pick == nil {
		return nil, ErrNothingToRollBack
	}
	return pick, nil
}

// Rollback undoes the last run that changed something: its files where nobody edited them since,
// and the plugin releases it moved, which stay skipped until something newer ships.
func Rollback(ctx context.Context, e Env, mode plan.Mode, confirm func([]plan.Outcome) (bool, error), argv []string) (Report, error) {
	g, err := lastUndoable(e.Roots.Stack)
	if err != nil {
		return Report{}, err
	}
	rep, _, err := execute(ctx, e, "rollback", mode, confirm, argv, func(en *env) []phase {
		return []phase{{steps: en.rollbackPlan(g)}}
	})
	if err == nil && rep.Generation > 0 && !rep.Plan.Failed() {
		g.Manifest.RolledBack = true
		err = g.Save()
	}
	return rep, err
}

// rollbackPlan is rollback's steps for g: find Claude Code (an undo never installs it), then g's
// files and plugin releases.
func (e *env) rollbackPlan(g *state.Generation) []plan.Step {
	find := e.findClaudeStep("not usable here, so the plugin releases this run moved stay: run `makeit rollback` again once Claude Code is back")
	return append([]plan.Step{find}, e.rollbackSteps(g)...)
}

// mcpChanges are the MCP servers a run moved.
func mcpChanges(m state.Manifest) []state.Change {
	var out []state.Change
	for _, c := range m.Changes {
		if c.Kind == "mcp" {
			out = append(out, c)
		}
	}
	return out
}

// rollbackSteps restore g's files, newest change first, then its plugin releases.
func (e *env) rollbackSteps(g *state.Generation) []plan.Step {
	var steps []plan.Step
	ops := g.Manifest.Ops
	for i := len(ops) - 1; i >= 0; i-- {
		steps = append(steps, e.restoreStep(g, ops[i]))
	}
	steps = append(steps, e.toolRollbackSteps(g)...)
	steps = append(steps, e.nodeRollbackSteps(g)...)
	if mcp := mcpChanges(g.Manifest); len(g.Manifest.Pins) > 0 || len(mcp) > 0 {
		steps = append(steps, e.pinsStep(g.Manifest.Pins, mcp))
		for _, p := range g.Manifest.Pins {
			steps = append(steps, plan.Step{ID: "plugin.rollback." + p.ID, Title: fmt.Sprintf("Put %s back to %s", p.ID, p.FromTag),
				Run: func(ctx context.Context) (string, error) {
					if e.claude == nil {
						return "", errNoClaude
					}
					return "", e.act(e.claude.Update(ctx, p.ID, ""))
				}})
		}
	}
	return steps
}

func shaOrNil(data []byte, found bool) *string {
	if !found {
		return nil
	}
	s := fsx.Sha256(data)
	return &s
}

func same(a, b *string) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }

// restoreStep puts one file back as it was before op, unless it changed since the run.
func (e *env) restoreStep(g *state.Generation, op state.Op) plan.Step {
	path := e.Roots.Abs(op.Path)
	id := op.ID
	if !strings.Contains(id, ".") && id != "settings" {
		id = "global." + id
	}
	return plan.Step{ID: id, Title: "Restore " + path,
		Already: func(context.Context) (bool, error) {
			cur, found, err := fsx.ReadOptional(path)
			if err != nil {
				return false, err
			}
			now := shaOrNil(cur, found)
			switch {
			case same(now, op.PreSha256):
				return true, nil
			case same(now, op.PostSha256):
				return false, nil
			}
			e.keep(path, "changed since the update: kept as it is")
			return true, nil
		},
		Run: func(context.Context) (string, error) {
			if op.PreSha256 == nil {
				return "", os.Remove(path)
			}
			old, err := os.ReadFile(filepath.Join(g.Dir, filepath.FromSlash(op.PreImage)))
			if err != nil {
				return "", err
			}
			// The write went through the link to its target (landing); so does the restore, and
			// the file keeps the mode it has (a profile is 0644, a global file 0600).
			dst, perm, err := landing(path, 0o600)
			if err != nil {
				return "", err
			}
			return "", fsx.WriteAtomic(dst, old, perm)
		}}
}

// pinsStep writes the earlier releases back into the local plugin set, records the undone ones as
// skipped, and refreshes the marketplace so Claude Code sees them.
func (e *env) pinsStep(pins []state.PinMove, mcp []state.Change) plan.Step {
	dir := filepath.Join(e.Roots.Stack, "catalog")
	cat := filepath.Join(dir, ".claude-plugin", "marketplace.json")
	return plan.Step{ID: "catalog.pins", Title: "Restore the plugin releases",
		Run: func(ctx context.Context) (string, error) {
			tree, err := catalog.DirTree(dir)
			if err != nil {
				return "", err
			}
			data, err := tree.ReadFile(".claude-plugin/marketplace.json")
			if err != nil {
				return "", err
			}
			var m map[string]any
			if err := json.Unmarshal(data, &m); err != nil {
				return "", fmt.Errorf("read %s: %w", cat, err)
			}
			plugins, _ := m["plugins"].([]any)
			for _, p := range pins {
				name, _, _ := strings.Cut(p.ID, "@")
				for _, raw := range plugins {
					entry, _ := raw.(map[string]any)
					src, _ := entry["source"].(map[string]any)
					if entry["name"] == name && src != nil {
						src["ref"], src["sha"] = p.FromTag, p.FromSHA
					}
				}
				// A release is skipped by its tag; a branch by the commit, or the branch would stay
				// skipped for good.
				skip := p.ToTag
				if p.FromTag == p.ToTag {
					skip = p.ToSHA
				}
				e.st.PluginSkips = initSkips(e.st.PluginSkips)
				if !slices.Contains(e.st.PluginSkips[p.ID], skip) {
					e.st.PluginSkips[p.ID] = append(e.st.PluginSkips[p.ID], skip)
				}
			}
			out, err := json.Marshal(m)
			if err != nil {
				return "", err
			}
			// Written the way the catalog step writes, marker included, so doctor sees makeit's own set.
			files := map[string][]byte{}
			for k, v := range tree.Files {
				files[k] = v
			}
			files[".claude-plugin/marketplace.json"] = out
			// The MCP servers go back to the versions they ran before, like the plugins.
			if b, ok := files["plugins/makeit/.mcp.json"]; ok {
				for _, c := range mcp {
					b = []byte(strings.ReplaceAll(string(b), c.Item+"@"+c.To, c.Item+"@"+c.From))
				}
				files["plugins/makeit/.mcp.json"] = b
			}
			exec := map[string]bool{}
			for _, f := range tree.Manifest.Files {
				exec[f.Path] = f.Mode == "0755"
			}
			next, err := catalog.TreeOf(files, exec)
			if err != nil {
				return "", err
			}
			if _, err := catalog.SyncTree(dir, next); err != nil {
				return "", err
			}
			if e.claude == nil {
				return "", errNoClaude
			}
			e.reload = true
			return "", e.act(e.claude.UpdateMarketplace(ctx, e.req.Marketplace))
		}}
}

func initSkips(m map[string][]string) map[string][]string {
	if m == nil {
		return map[string][]string{}
	}
	return m
}
