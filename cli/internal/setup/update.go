package setup

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"makeit/internal/catalog"
	"makeit/internal/claudecli"
	"makeit/internal/fsx"
	"makeit/internal/plan"
	"makeit/internal/pluginrule"
	"makeit/internal/state"
)

// Update brings the machine to the stack this binary carries (cli/UPDATE-UNINSTALL.md): setup's
// steps with update's rules for what makeit owns, then the plugin phase, which keeps going past a
// failed plugin and never undoes the earlier steps.
func Update(ctx context.Context, e Env, mode plan.Mode, confirm func([]plan.Outcome) (bool, error), argv []string) (Report, error) {
	rep, _, err := execute(ctx, e, "update", mode, confirm, argv, func(en *env) []phase {
		return []phase{{steps: en.setupSteps(en.updateGlobalSteps())}, {steps: en.pluginSteps(), keepGoing: true}}
	})
	return rep, err
}

// updateGlobalSteps are update's global-file steps. A new catalog file is written. An owned file
// nobody changed is replaced by a newer catalog version, or removed when the catalog dropped it.
// A file the user changed, removed or had before makeit is kept and reported.
func (e *env) updateGlobalSteps() []plan.Step {
	// The files come from the catalog in use: a git or directory catalog may hold fewer or more.
	files, err := catalog.List(e.Catalog, "templates/global/")
	if err != nil {
		return []plan.Step{{ID: "global", Title: "Update the global files",
			Already: func(context.Context) (bool, error) { return false, err }}}
	}
	var steps []plan.Step
	inCatalog := map[string]bool{}
	for _, src := range files {
		rel := strings.TrimPrefix(src, "templates/global/")
		path := filepath.Join(e.Roots.Claude, filepath.FromSlash(rel))
		key := e.Roots.Portable(path)
		inCatalog[key] = true
		want, rerr := e.Catalog.ReadFile(src)
		steps = append(steps, plan.Step{ID: "global." + rel, Title: "Update " + path,
			Detail: "+ " + strings.ReplaceAll(strings.TrimRight(string(want), "\n"), "\n", "\n+ "),
			Already: func(context.Context) (bool, error) {
				if rerr != nil {
					return false, rerr
				}
				cur, found, err := fsx.ReadOptional(path)
				if err != nil {
					return false, err
				}
				rec := e.st.Files[key]
				switch {
				case !found && rec == nil:
					return false, nil
				case !found:
					e.keep(key, removedByYou)
					return true, nil
				case rec == nil:
					// There before makeit: adopted, as setup does.
					e.st.Files = initMap(e.st.Files)
					rec = &state.File{Class: "owned", Source: src, PreExisted: true}
					e.st.Files[key] = rec
				}
				if handBack(rec, cur, want, rerr) {
					e.recordChanged = true
				}
				switch {
				// The user's: there before makeit and not the text makeit last recorded for it.
				case rec.PreExisted && (rec.Sha256 == "" || fsx.Sha256(cur) != rec.Sha256):
					if !bytes.Equal(cur, want) {
						e.keep(key, "yours; the plugin set's version differs")
					}
					return true, nil
				case e.linked(path):
					e.keep(key, "a link, so it is yours")
					return true, nil
				case bytes.Equal(cur, want):
					rec.Sha256 = fsx.Sha256(want)
					return true, nil
				case fsx.Sha256(cur) != rec.Sha256:
					e.keep(key, "changed since makeit wrote it")
					return true, nil
				}
				return false, nil
			},
			Run: func(context.Context) (string, error) {
				// Owner-only: the mode is set exactly, not filtered by umask (see landing).
				if err := e.write("global."+rel, path, want, 0o600); err != nil {
					return "", err
				}
				e.st.Files = initMap(e.st.Files)
				pre := e.st.Files[key] != nil && e.st.Files[key].PreExisted // uninstall keeps what was there before makeit
				e.st.Files[key] = &state.File{Class: "owned", Source: src, Sha256: fsx.Sha256(want), PreExisted: pre, Generation: e.st.Generation}
				return "", nil
			}})
	}
	keys := make([]string, 0, len(e.st.Files))
	for key, rec := range e.st.Files {
		// Only a global file (Source set at templates/global/...) can be stale here; path.env and
		// a merge-class record such as the profile block are a different owner, not this cleanup's.
		if rec.Source != "" && !inCatalog[key] && !rec.PreExisted {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	for _, key := range keys {
		rec := e.st.Files[key]
		path := e.Roots.Abs(key)
		rel := strings.TrimPrefix(key, "claude:")
		steps = append(steps, plan.Step{ID: "global.drop." + rel, Title: "Remove " + path + " (no longer in the plugin set)",
			Already: func(context.Context) (bool, error) {
				if e.linked(path) {
					e.keep(key, "a link, so it is yours")
					return true, nil
				}
				cur, found, err := fsx.ReadOptional(path)
				if err != nil {
					return false, err
				}
				if !found {
					delete(e.st.Files, key)
					return true, nil
				}
				if fsx.Sha256(cur) != rec.Sha256 {
					e.keep(key, "no longer in the plugin set; changed since makeit wrote it")
					return true, nil
				}
				return false, nil
			},
			Run: func(context.Context) (string, error) {
				if err := e.remove("global.drop."+rel, path); err != nil {
					return "", err
				}
				delete(e.st.Files, key)
				return "", nil
			}})
	}
	return steps
}

// errNoClaude: a plugin step needs Claude Code, which is not usable.
var errNoClaude = errors.New("the plugin steps cannot run: Claude Code is not usable")

// gather lists marketplaces and plugins once and keeps the answer until a step changes plugins.
func (e *env) gather(ctx context.Context) (pluginrule.Inputs, error) {
	if e.claude == nil {
		return pluginrule.Inputs{}, plan.ErrNotYet
	}
	if e.inputs == nil {
		in, err := pluginrule.Gather(ctx, e.claude, filepath.Join(e.Roots.Claude, "settings.json"), e.Getenv)
		if err != nil {
			return in, err
		}
		// The marketplace makeit added at its catalog copy gets this binary's pins: the copy on disk
		// is the old catalog until the catalog step syncs it, and the plan must show what the run does.
		own, copyDir := e.req.Marketplace, filepath.Join(e.Roots.Stack, "catalog")
		if rec := e.st.Marketplaces[own]; rec != nil && rec.AddedBy == "makeit" &&
			slices.ContainsFunc(in.Markets, func(m claudecli.Marketplace) bool { return m.Name == own && m.Path == copyDir }) {
			raw, err := e.Catalog.ReadFile(".claude-plugin/marketplace.json")
			if err != nil {
				return in, fmt.Errorf("the plugin set's marketplace.json: %w", err)
			}
			if in.Entries[own], err = pluginrule.ParseMarketplace(raw); err != nil {
				return in, fmt.Errorf("the plugin set's marketplace.json: %w", err)
			}
		}
		e.inputs = &in
	}
	return *e.inputs, nil
}

// owned reports whether makeit installed the plugin id.
func (e *env) owned(id string) bool {
	r := e.st.Plugins[id]
	return r != nil && r.InstalledBy == "makeit"
}

// listed is the listed plugin with id, or nil.
func listed(in pluginrule.Inputs, id string) *claudecli.Plugin {
	for i := range in.Plugins {
		if in.Plugins[i].ID == id {
			return &in.Plugins[i]
		}
	}
	return nil
}

// hint says how an adopted stack plugin gets its updates, once per plugin.
func (e *env) hint(h Hint) {
	for _, x := range e.hints {
		if x.ID == h.ID {
			return
		}
	}
	e.hints = append(e.hints, h)
}

// pluginSteps are update's plugin phase: refresh the makeit marketplace, install the dependencies
// the bundle gained, update what makeit installed, update the versions of adopted plugins from
// the makeit marketplace, and remove what the bundle no longer declares when nothing needs it.
func (e *env) pluginSteps() []plan.Step {
	declared, derr := pluginrule.BundleIDs(e.Catalog, e.req)
	steps := []plan.Step{e.refreshStep()}
	if e.owned(e.req.Bundle) {
		steps = append(steps, e.bundleDepsStep(declared, derr))
	}
	var owned, drop []string
	for id := range e.st.Plugins {
		switch {
		case !e.owned(id) || id == e.req.Bundle:
		case derr == nil && !slices.Contains(declared, id):
			drop = append(drop, id)
		default:
			owned = append(owned, id)
		}
	}
	slices.Sort(owned)
	slices.Sort(drop)
	if e.owned(e.req.Bundle) {
		owned = append([]string{e.req.Bundle}, owned...)
	}
	for _, id := range owned {
		steps = append(steps, e.ownedStep(id))
	}
	steps = append(steps, e.adoptedStep())
	for _, id := range drop {
		steps = append(steps, e.dropStep(id))
	}
	return steps
}

// refreshStep refreshes the makeit marketplace, so the steps after it see its current pins and
// dependency lists (a reinstall without a refresh sees the old list). It also hints how
// each adopted stack plugin that makeit does not update gets its updates.
func (e *env) refreshStep() plan.Step {
	own := e.req.Marketplace
	return plan.Step{ID: "marketplace.refresh", Title: "Refresh the " + own + " marketplace",
		Command: []string{"claude", "plugin", "marketplace", "update", own, "--json"},
		Already: func(ctx context.Context) (bool, error) {
			e.inputs = nil // each pass starts from a fresh listing
			in, err := e.gather(ctx)
			if err != nil {
				return false, err
			}
			if ids, err := pluginrule.BundleIDs(e.Catalog, e.req); err == nil {
				for _, id := range ids {
					if e.owned(id) || listed(in, id) == nil {
						continue
					}
					v := in.Classify(id)
					if strings.HasSuffix(id, "@"+own) && v.Rule != pluginrule.InPlace {
						continue // makeit updates its version
					}
					e.hint(Hint{ID: id, Rule: string(v.Rule), Text: v.Text})
				}
			}
			if !slices.ContainsFunc(in.Markets, func(m claudecli.Marketplace) bool { return m.Name == own }) {
				return true, nil
			}
			return !e.catalogChanges && !e.pluginWork(in), nil
		},
		Run: func(ctx context.Context) (string, error) {
			if e.claude == nil {
				return "", errNoClaude
			}
			defer func() { e.inputs = nil }()
			return "", e.act(e.claude.UpdateMarketplace(ctx, own))
		}}
}

// bundleDepsStep installs the dependencies the bundle declares and lacks by installing the bundle
// again, which keeps them auto-installed (plugin update alone does not install them).
func (e *env) bundleDepsStep(declared []string, derr error) plan.Step {
	return plan.Step{ID: "bundle.deps", Title: "Install the dependencies " + e.req.Bundle + " gained",
		Command: []string{"claude", "plugin", "install", e.req.Bundle, "--scope", "user", "--json"},
		Already: func(ctx context.Context) (bool, error) {
			if derr != nil {
				return false, derr
			}
			in, err := e.gather(ctx)
			if err != nil {
				return false, err
			}
			return !e.missingDeps(in, declared), nil
		},
		Run: func(ctx context.Context) (string, error) {
			if e.claude == nil {
				return "", errNoClaude
			}
			before, err := e.listedIDs(ctx)
			if err != nil {
				return "", err
			}
			if err := e.install(ctx, e.req.Bundle); err != nil {
				return "", err
			}
			e.inputs, e.reload = nil, true
			return "", e.recordNew(ctx, before)
		}}
}

// ownedStep updates a plugin makeit installed. When its pin moved and its version did not, plugin
// update reports up_to_date, so makeit uninstalls it and installs the bundle again,
// which brings it back at the new pin as an auto-installed dependency; a plugin with saved
// options is kept, because the uninstall would delete them.
func (e *env) ownedStep(id string) plan.Step {
	return plan.Step{ID: "plugin." + id, Title: "Update " + id,
		Command: []string{"claude", "plugin", "update", id, "--scope", "user", "--json"},
		Already: func(ctx context.Context) (bool, error) {
			in, err := e.gather(ctx)
			if err != nil {
				return false, err
			}
			return e.ownedCurrent(in, id), nil
		},
		Run: func(ctx context.Context) (string, error) {
			if e.claude == nil {
				return "", errNoClaude
			}
			in, err := e.gather(ctx)
			if err != nil {
				return "", err
			}
			v, p := in.Classify(id), listed(in, id)
			res, err := e.claude.Update(ctx, id, "")
			e.inputs = nil
			if err != nil {
				return "", err
			}
			if res.ShownCommand != nil {
				return "", shown(id, res)
			}
			if !res.OK() {
				return "", fmt.Errorf("claude plugin update %s: %s", id, res.Message)
			}
			rec := e.st.Plugins[id]
			if res.UpdateOutcome != "up_to_date" {
				e.reload = true
			}
			if v.Rule == pluginrule.Pinned && res.UpdateOutcome == "up_to_date" && rec.Observed.GitCommitSha != "" && rec.Observed.GitCommitSha != v.Pin {
				if p != nil && p.HasUserConfig {
					e.keep(id, fmt.Sprintf("its pinned commit moved without a version change; reinstalling deletes its saved options and secrets, so run: "+
						"claude plugin uninstall %s --scope user --keep-data && claude plugin marketplace update %s && claude plugin install %s --scope user",
						id, e.req.Marketplace, e.req.Bundle))
					return "", plan.ErrSkip
				}
				if err := e.backupData(DataID(id)); err != nil {
					return "", err
				}
				if err := e.act(e.claude.Uninstall(ctx, id)); err != nil {
					return "", err
				}
				if err := e.install(ctx, e.req.Bundle); err != nil {
					return "", err
				}
				e.reload = true
			}
			if v.Rule == pluginrule.Pinned {
				rec.Observed.GitCommitSha = v.Pin
			}
			if in, err := e.gather(ctx); err == nil {
				if p := listed(in, id); p != nil {
					rec.Observed.Version = p.Version
				}
			}
			if e.gen != nil {
				e.gen.Manifest.Plugins.Updated = append(e.gen.Manifest.Plugins.Updated, id)
			}
			return rec.Observed.Version, nil
		}}
}

// adoptedStep updates the versions of the plugins from the makeit marketplace that makeit did not
// install: `claude plugin update` only, never a reinstall, uninstall,
// disable or configure.
func (e *env) adoptedStep() plan.Step {
	return plan.Step{ID: "plugins.adopted", Title: "Update the versions of your plugins from the " + e.req.Marketplace + " marketplace",
		Already: func(ctx context.Context) (bool, error) {
			in, err := e.gather(ctx)
			if err != nil {
				return false, err
			}
			return len(e.adoptedIDs(in)) == 0, nil
		},
		Run: func(ctx context.Context) (string, error) {
			if e.claude == nil {
				return "", errNoClaude
			}
			in, err := e.gather(ctx)
			if err != nil {
				return "", err
			}
			var done []string
			var errs []error
			for _, id := range e.adoptedIDs(in) {
				res, err := e.claude.Update(ctx, id, "")
				switch {
				case err != nil:
					errs = append(errs, err)
				case res.ShownCommand != nil:
					errs = append(errs, shown(id, res))
				case !res.OK():
					errs = append(errs, fmt.Errorf("claude plugin update %s: %s", id, res.Message))
				default:
					done = append(done, id+": "+cmp.Or(res.UpdateOutcome, res.Outcome))
					if res.UpdateOutcome != "up_to_date" {
						e.reload = true
					}
					// The pin it was updated to, so the next update leaves it alone until the pin moves.
					if v := in.Classify(id); v.Rule == pluginrule.Pinned {
						e.st.Plugins = initMap(e.st.Plugins)
						e.st.Plugins[id] = &state.Plugin{InstalledBy: "user", Scope: "user", Observed: state.PluginObserved{GitCommitSha: v.Pin}}
					}
					// Recorded like a plugin makeit installed, so a pin move of it reaches the
					// generation (the daily note names it, rollback puts it back).
					if e.gen != nil {
						e.gen.Manifest.Plugins.Updated = append(e.gen.Manifest.Plugins.Updated, id)
					}
				}
			}
			e.inputs = nil
			return strings.Join(done, ", "), errors.Join(errs...)
		}}
}

// adoptedIDs are the listed plugins from the makeit marketplace that makeit did not install and
// that do not load in place, except those known to be at their pin: the version shows it (the
// 12-character commit Claude Code uses when the manifest pins no version), or makeit last updated
// the plugin at that pin.
func (e *env) adoptedIDs(in pluginrule.Inputs) []string {
	var ids []string
	for _, p := range in.Plugins {
		if !strings.HasSuffix(p.ID, "@"+e.req.Marketplace) || e.owned(p.ID) {
			continue
		}
		v := in.Classify(p.ID)
		if v.Rule == pluginrule.InPlace {
			continue
		}
		if v.Rule == pluginrule.Pinned {
			if head, _, _ := strings.Cut(p.Version, "-"); len(v.Pin) >= 12 && head == v.Pin[:12] {
				continue
			}
			if rec := e.st.Plugins[p.ID]; rec != nil && rec.Observed.GitCommitSha == v.Pin {
				continue
			}
		}
		ids = append(ids, p.ID)
	}
	return ids
}

// ownedCurrent reports whether a plugin makeit installed needs no update: it is gone, loads in
// place, is auto-updated, or sits at its marketplace's pin.
func (e *env) ownedCurrent(in pluginrule.Inputs, id string) bool {
	if listed(in, id) == nil {
		return true
	}
	switch v := in.Classify(id); v.Rule {
	case pluginrule.InPlace:
		// Its files load from the folder, but Claude Code keeps the old version until plugin update.
		p := listed(in, id)
		return p.FolderVersion == "" || p.FolderVersion == p.Version
	case pluginrule.AutoUpdate:
		return true
	case pluginrule.Pinned:
		return v.Pin == e.st.Plugins[id].Observed.GitCommitSha
	}
	return false
}

// missingDeps reports whether makeit's bundle is installed and lacks a dependency it declares.
func (e *env) missingDeps(in pluginrule.Inputs, declared []string) bool {
	if !e.owned(e.req.Bundle) || listed(in, e.req.Bundle) == nil {
		return false
	}
	for _, id := range declared {
		if listed(in, id) == nil {
			return true
		}
	}
	return false
}

// pluginWork reports whether a plugin step after the refresh has something to do, so the refresh
// runs only then: a dependency to install, an owned plugin to update or drop, an adopted one to update.
func (e *env) pluginWork(in pluginrule.Inputs) bool {
	declared, err := pluginrule.BundleIDs(e.Catalog, e.req)
	if err != nil || e.missingDeps(in, declared) || len(e.adoptedIDs(in)) > 0 {
		return true
	}
	for id := range e.st.Plugins {
		if !e.owned(id) || listed(in, id) == nil {
			continue
		}
		if !e.ownedCurrent(in, id) || (id != e.req.Bundle && !slices.Contains(declared, id)) {
			return true
		}
	}
	return false
}

// dropStep uninstalls a plugin makeit installed that the bundle no longer declares, when Claude
// Code lists it as no longer needed (`claude plugin prune --dry-run`); one another plugin still
// needs is kept.
func (e *env) dropStep(id string) plan.Step {
	return plan.Step{ID: "plugin.drop." + id, Title: "Remove " + id + " (the bundle no longer declares it)",
		Command: []string{"claude", "plugin", "uninstall", id, "--scope", "user", "--keep-data", "--json"},
		Already: func(ctx context.Context) (bool, error) {
			in, err := e.gather(ctx)
			if err != nil {
				return false, err
			}
			if listed(in, id) == nil {
				delete(e.st.Plugins, id)
				return true, nil
			}
			return false, nil
		},
		Run: func(ctx context.Context) (string, error) {
			if e.claude == nil {
				return "", errNoClaude
			}
			unneeded, err := e.claude.Unneeded(ctx)
			if err != nil {
				return "", err
			}
			if !slices.Contains(unneeded, id) {
				e.keep(id, "another installed plugin still needs it")
				return "", plan.ErrSkip
			}
			if err := e.act(e.claude.Uninstall(ctx, id)); err != nil {
				return "", err
			}
			e.inputs, e.reload = nil, true
			delete(e.st.Plugins, id)
			if e.gen != nil {
				e.gen.Manifest.Plugins.Uninstalled = append(e.gen.Manifest.Plugins.Uninstalled, id)
			}
			return "", nil
		}}
}
