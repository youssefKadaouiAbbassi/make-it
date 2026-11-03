package setup

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"makeit/internal/platform"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"makeit/internal/claudecli"
	"makeit/internal/fsx"
	"makeit/internal/jsonx"
	"makeit/internal/plan"
	"makeit/internal/pluginrule"
	"makeit/internal/settings"
	"makeit/internal/state"
)

// ErrNothing: makeit has no record on this machine, so there is nothing to uninstall.
var ErrNothing = errors.New("makeit has no record on this machine; nothing to uninstall")

// UninstallOptions are uninstall's flags.
type UninstallOptions struct {
	// KeepSettings leaves settings.json as it is.
	KeepSettings bool
	// Purge also deletes the data directories of the plugins makeit installed, and the makeit
	// directory with every generation: nothing of makeit is left and nothing can be put back.
	Purge bool
}

// officialDecl is where settings.json declares claude-plugins-official. Uninstall keeps it,
// because that marketplace stays (DESIGN 4.5).
const officialDecl = "/extraKnownMarketplaces/" + officialMarketplace

// Uninstall removes what makeit put on this machine and nobody changed since
// (cli/UPDATE-UNINSTALL.md): the plugins it installed (the bundle first, then the dependencies
// Claude Code no longer needs), the marketplace it added once nothing from it stays, its own
// settings values, the files it wrote, the directories it created and its catalog. Everything
// else is kept and listed. Every step runs even when one fails.
func Uninstall(ctx context.Context, e Env, mode plan.Mode, confirm func([]plan.Outcome) (bool, error), argv []string, opts UninstallOptions) (Report, error) {
	if _, err := os.Stat(filepath.Join(e.Roots.Stack, "state.json")); errors.Is(err, fs.ErrNotExist) {
		return Report{}, ErrNothing
	}
	var owned []string
	rep, en, err := execute(ctx, e, "uninstall", mode, confirm, argv, func(en *env) []phase {
		for id := range en.st.Plugins {
			if en.owned(id) {
				owned = append(owned, id)
			}
		}
		return []phase{{steps: en.uninstallSteps(opts), keepGoing: true}}
	})
	if err != nil || en == nil || !en.purge || rep.Plan.Failed() {
		return rep, err
	}
	// The purge step ran only because nothing of makeit remains. execute has returned, so the lock
	// is released. Only makeit's own files go: the makeit directory itself only when empty, because
	// MAKEIT_DIR may name a directory that holds other things.
	errs := []error{os.RemoveAll(state.GenerationsDir(e.Roots.Stack))}
	for _, id := range owned {
		errs = append(errs, os.RemoveAll(filepath.Join(e.Roots.Claude, "plugins", "data", DataID(id))))
	}
	if err := os.Remove(e.Roots.Stack); err != nil && !errors.Is(err, fs.ErrNotExist) {
		if entries, rerr := os.ReadDir(e.Roots.Stack); rerr != nil || len(entries) == 0 {
			errs = append(errs, err)
		}
	}
	return rep, errors.Join(errs...)
}

// DataID names a plugin's data directory under ~/.claude/plugins/data/: the id with every
// character other than an ASCII letter, a digit, '_' or '-' replaced by '-' (plugins_components.md 649).
func DataID(id string) string {
	return strings.Map(func(r rune) rune {
		if r == '_' || r == '-' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, id)
}

// backupData copies the named data directories under ~/.claude/plugins/data/ into the run's
// generation (data/<name>/) before something that could delete them; a missing one is skipped.
func (e *env) backupData(names ...string) error {
	if e.gen == nil {
		return errors.New("setup: a dry run copies nothing")
	}
	for _, name := range names {
		src := filepath.Join(e.Roots.Claude, "plugins", "data", name)
		dst := filepath.Join(e.gen.Dir, "data", name)
		if _, err := os.Stat(src); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		if _, err := os.Stat(dst); err == nil {
			continue // copied earlier in this run
		}
		if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
			return fmt.Errorf("copy %s into the generation: %w", src, err)
		}
	}
	return nil
}

// uninstallSteps are uninstall's steps, in order.
func (e *env) uninstallSteps(opts UninstallOptions) []plan.Step {
	e.keepSettings = opts.KeepSettings
	steps := []plan.Step{e.findClaudeStep()}
	var deps []string
	for id := range e.st.Plugins {
		if e.owned(id) && id != e.req.Bundle {
			deps = append(deps, id)
		}
	}
	slices.Sort(deps)
	if e.owned(e.req.Bundle) {
		steps = append(steps, e.unPluginStep(e.req.Bundle, false))
	}
	for _, id := range deps {
		steps = append(steps, e.unPluginStep(id, true))
	}
	steps = append(steps, e.yoursStep(), e.unMarketplaceStep())
	if opts.KeepSettings {
		steps = append(steps, plan.Step{ID: "settings", Title: "Leave settings.json as it is (--keep-settings)",
			Already: func(context.Context) (bool, error) {
				if len(e.st.Settings.Paths) > 0 {
					e.keep("settings", "now yours; makeit no longer tracks them")
				}
				return true, nil
			}})
	} else {
		steps = append(steps, e.unSettingsStep())
	}
	keys := make([]string, 0, len(e.st.Files))
	for k := range e.st.Files {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		steps = append(steps, e.unFileStep(k))
	}
	steps = append(steps, e.toolSteps()...)
	steps = append(steps, e.unDirsStep(), e.unCatalogStep(), e.unStateStep())
	if opts.Purge {
		steps = append(steps, plan.Step{ID: "purge",
			Title: "Delete the data of the plugins makeit installed, and every backup in " + e.Roots.Stack + " (cannot be undone)",
			Run: func(context.Context) (string, error) {
				if !e.stateRemoved {
					e.keep("purge", "skipped: something of makeit remains, and its data and backups stay with it")
					return "", plan.ErrSkip
				}
				e.purge = true
				return "after the run", nil
			}})
	}
	return steps
}

// findClaudeStep finds a usable Claude Code; without one, nothing is uninstalled.
func (e *env) findClaudeStep() plan.Step {
	return plan.Step{ID: "claude", Title: "Find Claude Code",
		Already: func(ctx context.Context) (bool, error) {
			if _, _, ok := e.findClaude(ctx); !ok {
				e.keep("claude", "not usable here, so makeit's plugins and its marketplace stay: run `makeit uninstall` again once Claude Code is back")
			}
			return true, nil
		}}
}

// unPluginStep uninstalls a plugin makeit installed, keeping its data. A dependency goes only
// when Claude Code lists it as no longer needed: uninstalling one that another plugin needs is
// not refused (measured), so makeit checks first.
func (e *env) unPluginStep(id string, dep bool) plan.Step {
	return plan.Step{ID: "plugin." + id, Title: "Uninstall " + id + " (its data stays)",
		Command: []string{"claude", "plugin", "uninstall", id, "--scope", "user", "--keep-data", "--json"},
		Already: func(ctx context.Context) (bool, error) {
			if e.claude == nil {
				return true, nil // stays recorded: the claude line says how to finish
			}
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
			if dep {
				unneeded, err := e.claude.Unneeded(ctx)
				if err != nil {
					return "", err
				}
				if !slices.Contains(unneeded, id) {
					e.keep(id, "another installed plugin still needs it")
					return "", plan.ErrSkip
				}
			}
			if err := e.act(e.claude.Uninstall(ctx, id)); err != nil {
				return "", err
			}
			e.inputs = nil
			delete(e.st.Plugins, id)
			if e.gen != nil {
				e.gen.Manifest.Plugins.Uninstalled = append(e.gen.Manifest.Plugins.Uninstalled, id)
			}
			return "", nil
		}}
}

// yoursStep lists what the user installed or added: makeit leaves all of it alone.
func (e *env) yoursStep() plan.Step {
	return plan.Step{ID: "yours", Title: "Keep the plugins and marketplaces you installed yourself",
		Already: func(context.Context) (bool, error) {
			var ids, names []string
			for id, r := range e.st.Plugins {
				if r.InstalledBy == "user" {
					ids = append(ids, id)
				}
			}
			for name, r := range e.st.Marketplaces {
				if r.AddedBy == "user" {
					names = append(names, name)
				}
			}
			slices.Sort(ids)
			slices.Sort(names)
			for _, id := range ids {
				e.keep(id, "yours")
			}
			for _, name := range names {
				e.keep("marketplace "+name, "yours")
			}
			return true, nil
		}}
}

// fromMarketplace are the listed plugins from marketplace name; with users set, only those makeit
// did not install.
func (e *env) fromMarketplace(in pluginrule.Inputs, name string, users bool) []string {
	var ids []string
	for _, p := range in.Plugins {
		if strings.HasSuffix(p.ID, "@"+name) && !(users && e.owned(p.ID)) {
			ids = append(ids, p.ID)
		}
	}
	return ids
}

// unMarketplaceStep removes the marketplace makeit added, once no plugin from it is installed:
// removing it uninstalls them and deletes their data (measured). Its plugins' data directories
// are copied into the generation first (DESIGN 4.5).
func (e *env) unMarketplaceStep() plan.Step {
	own := e.req.Marketplace
	stays := func(left []string) {
		e.keep("marketplace "+own, strings.Join(left, ", ")+" still installed from it, and removing it would uninstall them")
	}
	return plan.Step{ID: "marketplace", Title: "Remove the " + own + " marketplace (its plugins' data is copied into the generation first)",
		Command: []string{"claude", "plugin", "marketplace", "remove", own, "--scope", "user", "--json"},
		Already: func(ctx context.Context) (bool, error) {
			if rec := e.st.Marketplaces[own]; rec == nil || rec.AddedBy != "makeit" || e.claude == nil {
				return true, nil
			}
			in, err := e.gather(ctx)
			if err != nil {
				return false, err
			}
			if !slices.ContainsFunc(in.Markets, func(m claudecli.Marketplace) bool { return m.Name == own }) {
				delete(e.st.Marketplaces, own)
				return true, nil
			}
			if users := e.fromMarketplace(in, own, true); len(users) > 0 {
				stays(users)
				return true, nil
			}
			return false, nil
		},
		Run: func(ctx context.Context) (string, error) {
			if e.claude == nil {
				return "", errNoClaude
			}
			e.inputs = nil
			in, err := e.gather(ctx)
			if err != nil {
				return "", err
			}
			if left := e.fromMarketplace(in, own, false); len(left) > 0 {
				stays(left)
				return "", plan.ErrSkip
			}
			var names []string
			entries, err := os.ReadDir(filepath.Join(e.Roots.Claude, "plugins", "data"))
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return "", err
			}
			for _, d := range entries {
				if d.IsDir() && strings.HasSuffix(d.Name(), "-"+DataID(own)) {
					names = append(names, d.Name())
				}
			}
			if err := e.backupData(names...); err != nil {
				return "", err
			}
			if err := e.act(e.claude.RemoveMarketplace(ctx, own)); err != nil {
				return "", err
			}
			e.inputs = nil
			delete(e.st.Marketplaces, own)
			return "", nil
		}}
}

// unmergeSettings takes makeit's values out of the live settings.json with the merge records, the
// claude-plugins-official declaration aside. found is false when there is no settings.json.
func (e *env) unmergeSettings() (res settings.Result, old []byte, found bool, err error) {
	path := filepath.Join(e.Roots.Claude, "settings.json")
	old, found, err = fsx.ReadOptional(path)
	if err != nil || !found {
		return settings.Result{}, old, found, err
	}
	live, err := jsonx.DecodeObject(old)
	if err != nil {
		return settings.Result{}, old, true, fmt.Errorf("%s is not valid JSON, fix it first: %w", path, err)
	}
	recs := settings.State{}
	for k, r := range e.st.Settings.Paths {
		if k != officialDecl && !strings.HasPrefix(k, officialDecl+"/") {
			recs[k] = r
		}
	}
	return settings.Unmerge(live, recs), old, true, nil
}

// settingsEmptied: makeit created settings.json and nothing else would be left in it.
func (e *env) settingsEmptied(res settings.Result) bool {
	return e.st.Settings.CreatedByUs && res.Settings.Len() == 0
}

// settingsLeft reports whether settings.json still holds a value of makeit's to take out.
func (e *env) settingsLeft() bool {
	if len(e.st.Settings.Paths) == 0 {
		return false
	}
	res, _, found, err := e.unmergeSettings()
	return err != nil || (found && (res.Changed() || e.settingsEmptied(res)))
}

// unSettingsStep takes makeit's values out of settings.json with the merge records: a value still
// equal to what makeit wrote returns to what was there before, and a value the user changed stays.
// The declaration of claude-plugins-official stays. With Claude Code, the result is validated before
// and after the write and put back on failure; without it (the delegate's ruling), it is written
// unvalidated, its pre-image kept. A settings.json makeit created that would be left empty is removed.
func (e *env) unSettingsStep() plan.Step {
	path := filepath.Join(e.Roots.Claude, "settings.json")
	keepTheirs := func(changes []settings.Change) {
		for _, c := range changes {
			if c.Action == settings.KeptTheirs {
				e.keep(c.Path, "changed since makeit set it")
			}
		}
	}
	return plan.Step{ID: "settings", Title: "Take makeit's values out of " + path,
		Already: func(context.Context) (bool, error) {
			if len(e.st.Settings.Paths) == 0 {
				return true, nil
			}
			res, _, found, err := e.unmergeSettings()
			if err != nil {
				return false, err
			}
			keepTheirs(res.Changes)
			return !found || (!res.Changed() && !e.settingsEmptied(res)), nil
		},
		Run: func(ctx context.Context) (string, error) {
			// Unmerged again now, not at planning time: a running Claude Code may have written the file.
			res, old, found, err := e.unmergeSettings()
			if err != nil {
				return "", err
			}
			keepTheirs(res.Changes)
			if !found {
				e.st.Settings = state.Settings{}
				return "", nil
			}
			if e.settingsEmptied(res) {
				if err := e.remove("settings", path); err != nil {
					return "", err
				}
				e.st.Settings = state.Settings{}
				return "removed: makeit created it and nothing else is in it", nil
			}
			data, err := jsonx.Marshal(res.Settings)
			if err != nil {
				return "", err
			}
			if e.claude != nil {
				if err := e.validate(ctx, data, "settings.json without makeit's values"); err != nil {
					return "", err
				}
			}
			if err := e.write("settings", path, data, 0o600); err != nil {
				return "", err
			}
			if e.claude != nil {
				written, _, err := fsx.ReadOptional(path)
				if err == nil {
					err = e.validate(ctx, written, "settings.json after the write")
				}
				if err != nil {
					if rerr := e.restore(path, old); rerr != nil {
						return "", errors.Join(err, rerr)
					}
					return "", fmt.Errorf("%w; the previous settings.json was put back", err)
				}
			}
			e.st.Settings = state.Settings{}
			return "", nil
		}}
}

// unFileStep removes a file makeit wrote while it still holds what makeit wrote. A file the user
// changed, had before makeit, or linked is kept.
func (e *env) unFileStep(key string) plan.Step {
	rec := e.st.Files[key]
	if rec.Class == "merge" {
		return e.unMergeStep(key)
	}
	path := e.Roots.Abs(key)
	return plan.Step{ID: "file." + strings.TrimPrefix(key, "claude:"), Title: "Remove " + path,
		Already: func(context.Context) (bool, error) {
			// What is kept as the user's is theirs from now on: its record goes, so it does not hold state.json.
			yours := func(reason string) (bool, error) {
				e.keep(key, reason)
				delete(e.st.Files, key)
				return true, nil
			}
			if rec.PreExisted {
				return yours("yours")
			}
			if e.linked(path) {
				return yours("a link, so it is yours")
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
				return yours("changed since makeit wrote it")
			}
			return false, nil
		},
		Run: func(context.Context) (string, error) {
			if err := e.remove("file."+key, path); err != nil {
				return "", err
			}
			delete(e.st.Files, key)
			return "", nil
		}}
}

// createdDirs are the directories makeit recorded creating, deepest first.
func (e *env) createdDirs() []string {
	var keys []string
	for k, d := range e.st.Dirs {
		if d.CreatedByUs {
			keys = append(keys, k)
		}
	}
	slices.SortFunc(keys, func(a, b string) int {
		if n := strings.Count(b, "/") - strings.Count(a, "/"); n != 0 {
			return n
		}
		return strings.Compare(a, b)
	})
	return keys
}

// unDirsStep removes the directories makeit created once they are empty; one that holds
// anything else is the user's and stays.
func (e *env) unDirsStep() plan.Step {
	return plan.Step{ID: "dirs", Title: "Remove the directories makeit created, when empty",
		Already: func(context.Context) (bool, error) {
			for _, k := range e.createdDirs() {
				if _, err := os.Stat(e.Roots.Abs(k)); err == nil {
					return false, nil
				}
			}
			return true, nil
		},
		Run: func(context.Context) (string, error) {
			var removed []string
			for _, k := range e.createdDirs() {
				dir := e.Roots.Abs(k)
				if e.linked(dir) {
					e.keep(k, "a link, so it is yours")
					delete(e.st.Dirs, k)
					continue
				}
				entries, err := os.ReadDir(dir)
				switch {
				case errors.Is(err, fs.ErrNotExist):
				case err != nil:
					return strings.Join(removed, ", "), err
				case len(entries) > 0:
					e.keep(k, "not empty")
				default:
					if err := os.Remove(dir); err != nil {
						return strings.Join(removed, ", "), err
					}
					removed = append(removed, k)
				}
				delete(e.st.Dirs, k)
			}
			return strings.Join(removed, ", "), nil
		}}
}

// unCatalogStep removes makeit's copy of the catalog once no marketplace points at it.
func (e *env) unCatalogStep() plan.Step {
	dir := filepath.Join(e.Roots.Stack, "catalog")
	own := e.req.Marketplace
	users := func(in pluginrule.Inputs) []string {
		var names []string
		for _, m := range in.Markets {
			if m.Path == dir {
				names = append(names, m.Name)
			}
		}
		return names
	}
	return plan.Step{ID: "catalog", Title: "Remove makeit's catalog " + dir,
		Already: func(ctx context.Context) (bool, error) {
			if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
				return true, nil
			}
			if e.claude == nil {
				return false, nil // nothing can use it without Claude Code (the delegate's ruling)
			}
			in, err := e.gather(ctx)
			if err != nil {
				return false, err
			}
			for _, name := range users(in) {
				// The run removes makeit's own marketplace first unless a plugin of the user's needs it.
				if rec := e.st.Marketplaces[own]; name == own && rec != nil && rec.AddedBy == "makeit" && len(e.fromMarketplace(in, own, true)) == 0 {
					continue
				}
				e.keep("plugin set "+dir, "the "+name+" marketplace still uses it")
				return true, nil
			}
			return false, nil
		},
		Run: func(ctx context.Context) (string, error) {
			var in pluginrule.Inputs
			if e.claude != nil {
				e.inputs = nil
				var err error
				if in, err = e.gather(ctx); err != nil {
					return "", err
				}
			}
			if names := users(in); len(names) > 0 {
				e.keep("plugin set "+dir, "the "+names[0]+" marketplace still uses it")
				return "", plan.ErrSkip
			}
			if err := os.RemoveAll(dir); err != nil {
				e.catalogLeft = true
				return "", err
			}
			return "", nil
		}}
}

// remaining are the items makeit still owns on the machine.
func (e *env) remaining() []string {
	var left []string
	for id, r := range e.st.Plugins {
		if r.InstalledBy == "makeit" {
			left = append(left, id)
		}
	}
	for name, r := range e.st.Marketplaces {
		if r.AddedBy == "makeit" {
			left = append(left, "marketplace "+name)
		}
	}
	for k, r := range e.st.Files {
		if !r.PreExisted {
			left = append(left, k)
		}
	}
	for k, d := range e.st.Dirs {
		if d.CreatedByUs {
			left = append(left, k)
		}
	}
	if !e.keepSettings && e.settingsLeft() {
		left = append(left, "settings")
	}
	if e.catalogLeft {
		left = append(left, "plugin set")
	}
	slices.Sort(left)
	return left
}

// unStateStep removes state.json, keeping a copy in the generation, once nothing of makeit
// remains; otherwise it keeps only the records of what remains, so a rerun finishes the job.
func (e *env) unStateStep() plan.Step {
	statePath := filepath.Join(e.Roots.Stack, "state.json")
	return plan.Step{ID: "state", Title: "Remove makeit's record " + statePath + " (kept while anything of makeit remains)",
		Run: func(context.Context) (string, error) {
			if e.keepSettings || !e.settingsLeft() {
				e.st.Settings = state.Settings{} // the values are the user's now
			}
			if left := e.remaining(); len(left) > 0 {
				for id, r := range e.st.Plugins {
					if r.InstalledBy != "makeit" {
						delete(e.st.Plugins, id)
					}
				}
				for name, r := range e.st.Marketplaces {
					if r.AddedBy != "makeit" {
						delete(e.st.Marketplaces, name)
					}
				}
				for k, r := range e.st.Files {
					if r.PreExisted {
						delete(e.st.Files, k)
					}
				}
				return "kept: " + strings.Join(left, ", ") + " remain", nil
			}
			data, found, err := fsx.ReadOptional(statePath)
			if err != nil {
				return "", err
			}
			if found && e.gen != nil {
				if err := fsx.WriteAtomic(filepath.Join(e.gen.Dir, "state.json"), data, 0o600); err != nil {
					return "", err
				}
			}
			if err := os.Remove(statePath); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return "", err
			}
			e.stateRemoved = true
			return "removed; a copy is in the generation", nil
		}}
}

// toolSteps removes the release binaries makeit downloaded, one step per tool so each is
// recorded like any other removal. A tool makeit installed any other way (package manager,
// npm, a toolchain) is left alone and reported with the command to remove it by hand.
func (e *env) toolSteps() []plan.Step {
	ids := make([]string, 0, len(e.st.Prereqs))
	for id := range e.st.Prereqs {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	var steps []plan.Step
	for _, id := range ids {
		rec := e.st.Prereqs[id]
		if rec.InstalledBy != "makeit" {
			continue
		}
		if rec.Route != "release" {
			e.keep(id, "installed by makeit through "+rec.Route+"; to remove: "+removeCommand(rec.Route, id))
			continue
		}
		bin := id
		if r := platform.Find(id).Release; r != nil {
			bin = r.Bin
		}
		path := filepath.Join(e.Roots.Home, ".local", "bin", bin)
		steps = append(steps, plan.Step{ID: "tool." + id, Title: "Remove " + path,
			Already: func(context.Context) (bool, error) {
				cur, found, err := fsx.ReadOptional(path)
				if err != nil {
					return false, err
				}
				if !found {
					delete(e.st.Prereqs, id)
					return true, nil
				}
				if fsx.Sha256(cur) != rec.Sha256 {
					e.keep(id, "changed since makeit wrote it")
					return true, nil
				}
				return false, nil
			},
			Run: func(context.Context) (string, error) {
				if err := e.remove("tool."+id, path); err != nil {
					return "", err
				}
				delete(e.st.Prereqs, id)
				return "", nil
			}})
	}
	return steps
}

// removeCommand is the line to remove a tool makeit installed through a route other than a
// release download: the package names the route installed, from the tool table.
func removeCommand(route, id string) string {
	var r platform.Prereq
	if i := slices.IndexFunc(platform.Prereqs, func(p platform.Prereq) bool { return p.ID == id }); i >= 0 {
		r = platform.Prereqs[i]
	}
	pkgs := func(pm platform.PM) string {
		if len(r.Pkgs[pm]) > 0 {
			return strings.Join(r.Pkgs[pm], " ")
		}
		return id
	}
	// Google's package file: the package it installs is the file's name before the version.
	pkgFile := func(pm platform.PM) string {
		name, _, _ := strings.Cut(path.Base(r.PkgFile[pm]), "_")
		return cmp.Or(name, id)
	}
	via := func(bin string) []string {
		for _, v := range r.Via {
			if v.Bin == bin {
				return v.Argv
			}
		}
		return nil
	}
	switch route {
	case "apt":
		return "sudo apt-get remove " + pkgs(platform.APT)
	case "deb":
		return "sudo apt-get remove " + pkgFile(platform.APT)
	case "dnf":
		return "sudo dnf remove " + pkgs(platform.DNF)
	case "rpm":
		return "sudo dnf remove " + pkgFile(platform.DNF)
	case "apk":
		return "sudo apk del " + pkgs(platform.APK)
	case "pacman":
		return "sudo pacman -R " + pkgs(platform.Pacman)
	case "brew":
		return "brew uninstall " + pkgs(platform.Brew)
	case "winget":
		return "winget uninstall --id " + pkgs(platform.WinGet)
	case "npm":
		// The packages after `--prefix <dir>` in the install command.
		argv := via("npm")
		if i := slices.Index(argv, "--prefix"); i >= 0 && i+2 <= len(argv) {
			return "npm uninstall -g --prefix ~/.local " + strings.Join(argv[i+2:], " ")
		}
		return "npm uninstall -g --prefix ~/.local " + id
	case "go":
		bin := id
		if len(r.Bins) > 0 {
			bin = r.Bins[0]
		}
		return "rm ~/.local/bin/" + bin
	case "rustup":
		if argv := via("rustup"); len(argv) > 0 {
			return "rustup component remove " + argv[len(argv)-1]
		}
		return "rustup component remove " + id
	}
	return "remove it the way you installed it"
}
