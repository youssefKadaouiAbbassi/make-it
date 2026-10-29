package doctor

import (
	"cmp"
	"context"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"
	"time"

	"makeit/internal/catalog"
	"makeit/internal/claudecli"
	"makeit/internal/state"
)

// plugins checks the makeit marketplace and the bundle with every plugin it depends on.
func (r *report) plugins(ctx context.Context, m Machine, req catalog.Requires, reqErr error, unusable string, st *state.State) {
	const g = "plugins"
	if unusable != "" {
		r.add("marketplace", g, Skipped, unusable, "")
	} else {
		r.marketplace(ctx, m, req, st)
	}
	ids, err := bundleIDs(m.Catalog, req, reqErr)
	if err != nil {
		r.add("plugin.list", g, Error, err.Error(), "")
		return
	}
	if unusable != "" {
		for _, id := range ids {
			r.add("plugin."+id, g, Skipped, unusable, "")
		}
		return
	}
	installed, err := m.Claude.Plugins(ctx)
	if err != nil {
		r.add("plugin.list", g, Error, err.Error(), "")
		return
	}
	for _, id := range ids {
		i := slices.IndexFunc(installed, func(p claudecli.Plugin) bool { return p.ID == id })
		switch {
		case i < 0:
			r.add("plugin."+id, g, Error, id+" is not installed", "makeit setup")
		case len(installed[i].Errors) > 0:
			r.add("plugin."+id, g, Error, id+": "+installed[i].Errors[0], "")
		case !installed[i].Enabled:
			r.add("plugin."+id, g, Info, id+" is installed but disabled", "")
		default:
			r.add("plugin."+id, g, OK, id+" "+installed[i].Version, "")
		}
	}
}

func (r *report) marketplace(ctx context.Context, m Machine, req catalog.Requires, st *state.State) {
	const id, g = "marketplace", "plugins"
	markets, err := m.Claude.Marketplaces(ctx)
	if err != nil {
		r.add(id, g, Error, err.Error(), "")
		return
	}
	for _, mk := range markets {
		if mk.Name == req.Marketplace {
			if rec := st.Marketplaces[mk.Name]; rec != nil && rec.AddedBy == "user" {
				r.add(id, g, Info, fmt.Sprintf("marketplace %s (%s %s) is yours (adopted)", mk.Name, mk.Source, cmp.Or(mk.Path, mk.Repo, mk.URL)), "")
				return
			}
			r.add(id, g, OK, fmt.Sprintf("marketplace %s (%s %s)", mk.Name, mk.Source, cmp.Or(mk.Path, mk.Repo, mk.URL)), "")
			return
		}
	}
	r.add(id, g, Error, "the "+req.Marketplace+" marketplace is not added", "makeit setup")
}

// bundleIDs are the bundle's id and its plugin.json dependencies; a dependency without a
// marketplace comes from the bundle's own.
func bundleIDs(src catalog.Source, req catalog.Requires, reqErr error) ([]string, error) {
	if reqErr != nil {
		return nil, reqErr
	}
	name, _, _ := strings.Cut(req.Bundle, "@")
	raw, err := src.ReadFile("plugins/" + name + "/.claude-plugin/plugin.json")
	if err != nil {
		return nil, fmt.Errorf("the catalog's %s manifest: %w", name, err)
	}
	var manifest struct {
		Dependencies []string `json:"dependencies"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("the catalog's %s manifest: %w", name, err)
	}
	ids := []string{req.Bundle}
	for _, d := range manifest.Dependencies {
		if !strings.Contains(d, "@") {
			d += "@" + req.Marketplace
		}
		ids = append(ids, d)
	}
	return ids, nil
}

// record checks makeit's own record of what it did on this machine.
func (r *report) record(s *state.State, info state.Info, err error) {
	const id, g = "state", "state"
	switch {
	case err != nil:
		r.add(id, g, Error, err.Error(), "")
	case !info.Found:
		r.add(id, g, Info, "setup has not run on this machine", "makeit setup")
	default:
		r.add(id, g, OK, fmt.Sprintf("setup recorded by makeit %s at %s", s.CLIVersion, s.WrittenAt.Format(time.RFC3339)), "")
	}
}
