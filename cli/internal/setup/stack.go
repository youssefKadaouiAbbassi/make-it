package setup

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"

	"makeit/internal/catalog"
	"makeit/internal/claudecli"
	"makeit/internal/plan"
	"makeit/internal/state"
)

const officialMarketplace = "claude-plugins-official"

// stackSteps put the catalog on disk and register it, add Anthropic's marketplace (the bundle's
// LSP plugins come from it) and install the bundle. A marketplace or bundle that is already
// there, from any source, is adopted: recorded as the user's and never added again.
func (e *env) stackSteps() []plan.Step {
	dir := filepath.Join(e.Roots.Stack, "catalog")
	return []plan.Step{
		{ID: "catalog", Title: "Write the catalog to " + dir,
			Already: func(ctx context.Context) (bool, error) {
				adopted, err := e.marketplaceListed(ctx, e.req.Marketplace)
				if err != nil || adopted {
					return adopted, err
				}
				return catalog.UpToDate(dir)
			},
			Run: func(context.Context) (string, error) {
				n, err := catalog.Sync(dir)
				if err != nil {
					return "", err
				}
				e.st.Catalog = state.Catalog{Kind: "embedded", Location: e.Roots.Portable(dir), MarketplaceName: e.req.Marketplace}
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
			Already: func(ctx context.Context) (bool, error) { return e.marketplaceListed(ctx, e.req.Marketplace) },
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
				if err != nil || p == nil || !p.Enabled {
					return false, err
				}
				e.st.Plugins = initMap(e.st.Plugins)
				if e.st.Plugins[e.req.Bundle] == nil {
					e.st.Plugins[e.req.Bundle] = &state.Plugin{InstalledBy: "user", Scope: p.Scope, Observed: state.PluginObserved{Version: p.Version}}
				}
				return true, nil
			},
			Run: func(ctx context.Context) (string, error) {
				// No --accept-command: a marketplace-declared command is shown and refused (DESIGN 4.4).
				res, err := e.claude.Install(ctx, e.req.Bundle, "")
				if err != nil {
					return "", err
				}
				if res.ShownCommand != nil {
					return "", fmt.Errorf("%s wants to run a command makeit did not show you (sha256 %s); install it by hand to review it:\n%s",
						e.req.Bundle, res.ShownCommand.Sha256, res.Output)
				}
				if !res.OK() {
					return "", fmt.Errorf("claude plugin install %s: %s", e.req.Bundle, res.Message)
				}
				p, err := e.plugin(ctx, e.req.Bundle)
				if err != nil {
					return "", err
				}
				rec := &state.Plugin{InstalledBy: "makeit", Scope: "user", Generation: e.st.Generation}
				if p != nil {
					rec.Observed.Version = p.Version
				}
				e.st.Plugins = initMap(e.st.Plugins)
				e.st.Plugins[e.req.Bundle] = rec
				if e.gen != nil {
					e.gen.Manifest.Plugins.Installed = append(e.gen.Manifest.Plugins.Installed, e.req.Bundle)
				}
				return rec.Observed.Version, nil
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
