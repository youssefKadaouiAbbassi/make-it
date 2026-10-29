package doctor

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"makeit/internal/catalog"
	"makeit/internal/claudecli"
	"makeit/internal/pluginrule"
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
	var ids []string
	err := reqErr
	if err == nil {
		ids, err = pluginrule.BundleIDs(m.Catalog, req)
	}
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
