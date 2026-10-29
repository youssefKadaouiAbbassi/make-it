package doctor

import (
	"context"
	"fmt"

	"makeit/internal/fsx"
	"makeit/internal/jsonx"
	"makeit/internal/settings"
)

// settings checks Claude Code's settings.json: that it parses, that Claude Code accepts it, and
// where it differs from the catalog's baseline. marketplace is the catalog's own marketplace;
// unusable is why Claude Code cannot be asked.
func (r *report) settings(ctx context.Context, m Machine, marketplace, unusable string) {
	const g = "settings"
	data, found, err := fsx.ReadOptional(m.SettingsPath)
	var live *jsonx.Object
	skipValid, skipStack := "", ""
	switch {
	case err != nil:
		r.add("settings.parse", g, Error, fmt.Sprintf("%s cannot be read: %v", m.SettingsPath, err), "")
		skipValid, skipStack = "settings.json cannot be read", "settings.json cannot be read"
	case !found:
		r.add("settings.parse", g, Info, m.SettingsPath+" does not exist yet", "")
		live, skipValid = jsonx.NewObject(), "no settings.json to validate"
	default:
		if live, err = jsonx.DecodeObject(data); err != nil {
			r.add("settings.parse", g, Error, fmt.Sprintf("%s is not valid JSON: %v", m.SettingsPath, err), "")
			skipValid, skipStack = "settings.json does not parse", "settings.json does not parse"
		} else {
			r.add("settings.parse", g, OK, m.SettingsPath+" parses", "")
		}
	}
	if skipValid == "" {
		skipValid = unusable
	}
	r.valid(ctx, m, data, skipValid)
	r.stack(m, live, marketplace, skipStack)
}

func (r *report) valid(ctx context.Context, m Machine, data []byte, skip string) {
	const id, g = "settings.valid", "settings"
	if skip != "" {
		r.add(id, g, Skipped, skip, "")
		return
	}
	problems, err := m.Claude.ValidateSettings(ctx, data)
	if err != nil {
		r.add(id, g, Error, err.Error(), "")
		return
	}
	for _, p := range problems {
		r.add(id+":"+p.Key, g, Error, p.Key+" is invalid", p.Reason)
	}
	if len(problems) == 0 {
		r.add(id, g, OK, "Claude Code accepts settings.json", "")
	}
}

// stack reports where settings.json differs from the baseline, as a first setup would see it:
// a stack value that is missing is a warning, a value of the user's that setup keeps is info.
func (r *report) stack(m Machine, live *jsonx.Object, marketplace, skip string) {
	const id, g = "settings.stack", "settings"
	if skip != "" {
		r.add(id, g, Skipped, skip, "")
		return
	}
	raw, err := m.Catalog.ReadFile("settings.baseline.json")
	if err != nil {
		r.add(id, g, Error, "the catalog's settings.baseline.json: "+err.Error(), "")
		return
	}
	baseline, err := jsonx.DecodeObject(raw)
	if err != nil {
		r.add(id, g, Error, "the catalog's settings.baseline.json: "+err.Error(), "")
		return
	}
	baseline = settings.Desired(baseline, marketplace)
	reported := 0
	for _, c := range settings.Merge(live, baseline, nil, settings.DefaultOptions()).Changes {
		switch c.Action {
		case settings.Set, settings.ReAsserted:
			r.add(id+":"+c.Path, g, Warn, fmt.Sprintf("%s: the stack sets %s", c.Path, settings.Show(c.Path, c.After)), "makeit setup")
		case settings.AddItems:
			r.add(id+":"+c.Path, g, Warn, fmt.Sprintf("%s: the stack adds %s", c.Path, settings.Show(c.Path, c.After)), "makeit setup")
		case settings.KeptTheirs, settings.Held:
			r.add(id+":"+c.Path, g, Info, c.Path+": your value is kept", "")
		default:
			continue
		}
		reported++
	}
	if reported == 0 {
		r.add(id, g, OK, "settings.json has every stack value", "")
	}
}
