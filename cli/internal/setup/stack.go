package setup

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"

	"makeit/internal/catalog"
	"makeit/internal/claudecli"
	"makeit/internal/plan"
	"makeit/internal/pluginrule"
	"makeit/internal/state"
)

const officialMarketplace = "claude-plugins-official"

// stackSteps put the catalog on disk and register it, add Anthropic's marketplace (the bundle's
// LSP plugins come from it) and install the bundle. A marketplace or bundle that is already
// there, from any source, is adopted: recorded as the user's and never added again.
func (e *env) stackSteps() []plan.Step {
	dir := filepath.Join(e.Roots.Stack, "catalog")
	return []plan.Step{
		{ID: "catalog", Title: "Write the plugin set to " + dir,
			Already: func(ctx context.Context) (bool, error) {
				// Only a marketplace the user added makes the catalog theirs; one makeit added keeps
				// its catalog in sync, and a deleted catalog is written again.
				listed, err := e.marketplaceListed(ctx, e.req.Marketplace)
				if err != nil {
					return false, err
				}
				rec := e.st.Marketplaces[e.req.Marketplace]
				if listed && rec != nil && rec.AddedBy == "user" {
					return true, nil
				}
				// update writes no catalog for a marketplace the user removed (the marketplace step says so).
				if e.command == "update" && !listed && rec != nil {
					return true, nil
				}
				t, err := e.catalogTree(ctx)
				if err != nil {
					return false, err
				}
				up := catalog.UpToDateTree(dir, t)
				e.catalogChanges = !up
				// The same files from another source still run the step, so the switch is recorded.
				return up && sameCatalog(e.st.Catalog, e.catalogRecord(dir)), nil
			},
			Run: func(ctx context.Context) (string, error) {
				t, err := e.catalogTree(ctx)
				if err != nil {
					return "", err
				}
				n, err := catalog.SyncTree(dir, t)
				if err != nil {
					return "", err
				}
				e.reload = true
				e.st.Catalog = e.catalogRecord(dir)
				return fmt.Sprintf("%d files", n), nil
			}},
		{ID: "marketplace.official", Title: "Add the " + officialMarketplace + " marketplace",
			Command: []string{"claude", "plugin", "marketplace", "add", "anthropics/" + officialMarketplace, "--scope", "user", "--json"},
			Already: func(ctx context.Context) (bool, error) { return e.marketplaceListed(ctx, officialMarketplace) },
			Run: func(ctx context.Context) (string, error) {
				return "", e.act(e.claude.AddMarketplace(ctx, "anthropics/"+officialMarketplace))
			}},
		{ID: "marketplace", Title: "Add the " + e.req.Marketplace + " marketplace from " + dir,
			Command: []string{"claude", "plugin", "marketplace", "add", dir, "--scope", "user", "--json"},
			Already: func(ctx context.Context) (bool, error) {
				listed, err := e.marketplaceListed(ctx, e.req.Marketplace)
				if err != nil || listed {
					return listed, err
				}
				// update does not put back a recorded marketplace the user removed, adopted or added by makeit.
				if rec := e.st.Marketplaces[e.req.Marketplace]; e.command == "update" && rec != nil {
					e.keep("marketplace "+e.req.Marketplace, removedByYou)
					return true, nil
				}
				return false, nil
			},
			Run: func(ctx context.Context) (string, error) {
				if err := e.act(e.claude.AddMarketplace(ctx, dir)); err != nil {
					return "", err
				}
				e.st.Marketplaces = initMap(e.st.Marketplaces)
				e.st.Marketplaces[e.req.Marketplace] = &state.Marketplace{AddedBy: "makeit", Kind: "directory"}
				return "", nil
			}},
		{ID: "bundle", Title: "Install " + e.req.Bundle,
			Command: []string{"claude", "plugin", "install", e.req.Bundle, "--scope", "user", "--json"},
			Already: func(ctx context.Context) (bool, error) {
				p, err := e.plugin(ctx, e.req.Bundle)
				if err != nil {
					return false, err
				}
				// update does not put back a recorded bundle the user removed, adopted or installed by
				// makeit, and leaves a disabled one disabled.
				if rec := e.st.Plugins[e.req.Bundle]; e.command == "update" && p == nil && rec != nil {
					e.keep(e.req.Bundle, removedByYou)
					return true, nil
				}
				if e.command == "update" && p != nil && !p.Enabled {
					e.keep(e.req.Bundle, "disabled by you")
					return true, nil
				}
				if p == nil || !p.Enabled {
					return false, nil
				}
				e.st.Plugins = initMap(e.st.Plugins)
				if e.st.Plugins[e.req.Bundle] == nil {
					e.st.Plugins[e.req.Bundle] = &state.Plugin{InstalledBy: "user", Scope: p.Scope, Observed: state.PluginObserved{Version: p.Version}}
				}
				return true, nil
			},
			Run: func(ctx context.Context) (string, error) {
				before, err := e.listedIDs(ctx)
				if err != nil {
					return "", err
				}
				if err := e.install(ctx, e.req.Bundle); err != nil {
					return "", err
				}
				e.reload = true
				if err := e.recordNew(ctx, before); err != nil {
					return "", err
				}
				if rec := e.st.Plugins[e.req.Bundle]; rec != nil {
					return rec.Observed.Version, nil
				}
				return "", nil
			}},
	}
}

func initMap[V any](m map[string]*V) map[string]*V {
	if m == nil {
		m = map[string]*V{}
	}
	return m
}

// marketplaceListed reports whether claude lists a marketplace named name. The one named for the
// catalog is recorded as the user's when makeit did not add it.
func (e *env) marketplaceListed(ctx context.Context, name string) (bool, error) {
	if e.claude == nil {
		return false, plan.ErrNotYet
	}
	ms, err := e.claude.Marketplaces(ctx)
	if err != nil {
		return false, err
	}
	if !slices.ContainsFunc(ms, func(m claudecli.Marketplace) bool { return m.Name == name }) {
		return false, nil
	}
	if name == e.req.Marketplace {
		e.st.Marketplaces = initMap(e.st.Marketplaces)
		if e.st.Marketplaces[name] == nil {
			e.st.Marketplaces[name] = &state.Marketplace{AddedBy: "user"}
		}
		if e.st.Marketplaces[name].AddedBy == "user" {
			e.keep("marketplace "+name, "yours: makeit leaves it and its plugins as they are; to use makeit's plugin set instead, run claude plugin marketplace remove "+name+", then makeit setup")
		}
	}
	return true, nil
}

// plugin is the listed plugin with id, or nil.
func (e *env) plugin(ctx context.Context, id string) (*claudecli.Plugin, error) {
	if e.claude == nil {
		return nil, plan.ErrNotYet
	}
	ps, err := e.claude.Plugins(ctx)
	if err != nil {
		return nil, err
	}
	if i := slices.IndexFunc(ps, func(p claudecli.Plugin) bool { return p.ID == id }); i >= 0 {
		return &ps[i], nil
	}
	return nil, nil
}

// install installs id at user scope. No --accept-command: a marketplace-declared command is shown
// and refused, never accepted for the user (DESIGN 4.4).
func (e *env) install(ctx context.Context, id string) error {
	res, err := e.claude.Install(ctx, id, "")
	if err != nil {
		return err
	}
	if res.ShownCommand != nil {
		return shown(id, res)
	}
	if !res.OK() {
		return fmt.Errorf("claude plugin install %s: %s", id, res.Message)
	}
	return nil
}

// shown is the refusal of a command a marketplace declared and makeit did not show the user.
func shown(id string, res claudecli.Result) error {
	return fmt.Errorf("%s wants to run a command makeit did not show you (sha256 %s); install it by hand to review it:\n%s",
		id, res.ShownCommand.Sha256, res.Output)
}

// listedIDs are the ids `claude plugin list` shows.
func (e *env) listedIDs(ctx context.Context) ([]string, error) {
	ps, err := e.claude.Plugins(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(ps))
	for _, p := range ps {
		ids = append(ids, p.ID)
	}
	return ids, nil
}

// recordNew records every listed user-scope plugin missing from before as installed by makeit,
// with its version and, when its marketplace pins it, that commit. A plugin that was there before
// stays the user's.
func (e *env) recordNew(ctx context.Context, before []string) error {
	in, err := pluginrule.Gather(ctx, e.claude, filepath.Join(e.Roots.Claude, "settings.json"), e.Getenv)
	if err != nil {
		return err
	}
	for _, p := range in.Plugins {
		if slices.Contains(before, p.ID) || (p.Scope != "" && p.Scope != "user") {
			continue
		}
		rec := &state.Plugin{InstalledBy: "makeit", Scope: "user", Generation: e.st.Generation,
			Observed: state.PluginObserved{Version: p.Version}}
		if v := in.Classify(p.ID); v.Rule == pluginrule.Pinned {
			rec.Observed.GitCommitSha = v.Pin
		}
		e.st.Plugins = initMap(e.st.Plugins)
		e.st.Plugins[p.ID] = rec
		if e.gen != nil {
			e.gen.Manifest.Plugins.Installed = append(e.gen.Manifest.Plugins.Installed, p.ID)
		}
	}
	return nil
}

// act turns a claude action's result into an error when it did not succeed.
func (e *env) act(res claudecli.Result, err error) error {
	if err != nil {
		return err
	}
	if !res.OK() {
		return fmt.Errorf("%s: %s", res.Command, res.Message)
	}
	return nil
}

// catalogTree is the tree the catalog step writes: the prepared one, or the embedded catalog.
func (e *env) catalogTree(ctx context.Context) (*catalog.Tree, error) {
	if e.Resolve != nil && !e.resolved && e.Present("git") {
		r := e.Resolve(ctx)
		e.resolved = true
		if r.Tree != nil {
			e.Catalog, e.CatalogTree = r.Tree, r.Tree
		}
		e.Pins, e.MCPChanges = r.Pins, r.MCP
		e.notes = append(e.notes, r.Notes...)
	}
	if e.CatalogTree != nil {
		return e.CatalogTree, nil
	}
	return catalog.EmbeddedTree()
}

// sameCatalog reports whether two records name the same catalog source and commit.
func sameCatalog(a, b state.Catalog) bool {
	deref := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	return a.Kind == b.Kind && a.Location == b.Location && deref(a.Requested) == deref(b.Requested) && a.Resolved == b.Resolved &&
		a.RefKind == b.RefKind && a.Tag == b.Tag && a.AllowBranch == b.AllowBranch && a.MarketplaceName == b.MarketplaceName
}

// catalogRecord is what the state records for the catalog in dir. The embedded catalog's location
// is the synced directory, as before; other sources record where they come from.
func (e *env) catalogRecord(dir string) state.Catalog {
	rec := e.CatalogRecord
	if rec.Kind == "" || rec.Kind == "embedded" {
		rec.Kind, rec.Location = "embedded", e.Roots.Portable(dir)
	}
	rec.MarketplaceName = e.req.Marketplace
	return rec
}
