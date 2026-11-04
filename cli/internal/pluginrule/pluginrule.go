// Package pluginrule decides how each stack plugin gets its updates: it loads in place from a
// marketplace folder, it is pinned to a commit, its marketplace auto-updates, or it needs
// `claude plugin update` (cli/UPDATE-UNINSTALL.md, "How a stack plugin updates"). Setup, update
// and doctor share it. It reads only documented output and formats: `claude plugin list` and
// `marketplace list`, marketplace.json, plugin.json and settings.json.
package pluginrule

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"makeit/internal/catalog"
	"makeit/internal/claudecli"
	"makeit/internal/fsx"
	"makeit/internal/jsonx"
)

// BundleIDs are the bundle's id and its plugin.json dependencies; a dependency without a
// marketplace comes from the bundle's own.
func BundleIDs(src catalog.Source, req catalog.Requires) ([]string, error) {
	name, _, _ := strings.Cut(req.Bundle, "@")
	raw, err := src.ReadFile("plugins/" + name + "/.claude-plugin/plugin.json")
	if err != nil {
		return nil, fmt.Errorf("the plugin set's %s manifest: %w", name, err)
	}
	var manifest struct {
		Dependencies []string `json:"dependencies"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("the plugin set's %s manifest: %w", name, err)
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

// Entry is one plugin of a marketplace.json.
type Entry struct {
	// SHA is the commit a github, git, git-subdir or url source pins; empty otherwise.
	SHA string
}

// ReadMarketplace reads a marketplace.json: plugin name to entry.
func ReadMarketplace(path string) (map[string]Entry, error) {
	data, found, err := fsx.ReadOptional(path)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%s does not exist", path)
	}
	entries, err := ParseMarketplace(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return entries, nil
}

// ParseMarketplace parses a marketplace.json: plugin name to entry.
func ParseMarketplace(data []byte) (map[string]Entry, error) {
	var doc struct {
		Plugins []struct {
			Name   string `json:"name"`
			Source any    `json:"source"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	entries := map[string]Entry{}
	for _, p := range doc.Plugins {
		var e Entry
		if src, ok := p.Source.(map[string]any); ok {
			e.SHA, _ = src["sha"].(string)
		}
		entries[p.Name] = e
	}
	return entries, nil
}

// MarketplaceFile is where a marketplace added from a local path keeps its marketplace.json.
func MarketplaceFile(m claudecli.Marketplace) (string, bool) {
	switch m.Source {
	case "directory":
		return filepath.Join(m.Path, ".claude-plugin", "marketplace.json"), true
	case "file":
		return m.Path, true
	}
	return "", false
}

const official = "claude-plugins-official"

// AutoUpdates reports whether Claude Code auto-updates marketplace name (plugins_loading.md
// 356-364): settings' extraKnownMarketplaces.<name>.autoUpdate, else on only for
// claude-plugins-official; off when DISABLE_UPDATES, DISABLE_AUTOUPDATER or
// CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC is set without FORCE_AUTOUPDATE_PLUGINS, in the
// environment or in settings' env. settings and getenv may be nil.
func AutoUpdates(settings *jsonx.Object, name string, getenv func(string) string) bool {
	set := func(key string) bool {
		if getenv != nil && getenv(key) != "" {
			return true
		}
		s, _ := lookup(settings, "env", key).(string)
		return s != ""
	}
	if (set("DISABLE_UPDATES") || set("DISABLE_AUTOUPDATER") || set("CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC")) &&
		!set("FORCE_AUTOUPDATE_PLUGINS") {
		return false
	}
	if on, ok := lookup(settings, "extraKnownMarketplaces", name, "autoUpdate").(bool); ok {
		return on
	}
	return name == official
}

// lookup follows keys through nested objects; nil when a step is missing.
func lookup(o *jsonx.Object, keys ...string) any {
	var cur any = o
	for _, k := range keys {
		obj, ok := cur.(*jsonx.Object)
		if !ok || obj == nil {
			return nil
		}
		if cur, ok = obj.Get(k); !ok {
			return nil
		}
	}
	return cur
}

// Rule is how a plugin gets its updates.
type Rule string

// Rules, first match wins.
const (
	InPlace    Rule = "in-place"
	Pinned     Rule = "pinned"
	AutoUpdate Rule = "auto-update"
	Command    Rule = "command"
)

// Verdict is a plugin's rule, the line shown for it, and its marketplace pin when Pinned.
type Verdict struct {
	Rule Rule
	Text string
	Pin  string
}

// Inputs are what the rules read.
type Inputs struct {
	Markets []claudecli.Marketplace
	Plugins []claudecli.Plugin
	// Entries holds, per marketplace name, the plugins of each marketplace added from a local path.
	Entries  map[string]map[string]Entry
	Settings *jsonx.Object
	Getenv   func(string) string
}

// Gather lists marketplaces and plugins, reads each local marketplace's file (a missing or
// invalid one is skipped) and settings.json (absent or invalid is empty).
func Gather(ctx context.Context, c *claudecli.CLI, settingsPath string, getenv func(string) string) (Inputs, error) {
	in := Inputs{Entries: map[string]map[string]Entry{}, Getenv: getenv}
	// The two listings cost 0.6 s each: both at once.
	var wg sync.WaitGroup
	var merr, perr error
	wg.Add(2)
	go func() { defer wg.Done(); in.Markets, merr = c.Marketplaces(ctx) }()
	go func() { defer wg.Done(); in.Plugins, perr = c.Plugins(ctx) }()
	wg.Wait()
	if merr != nil {
		return in, merr
	}
	if perr != nil {
		return in, perr
	}
	for _, m := range in.Markets {
		if path, ok := MarketplaceFile(m); ok {
			if entries, err := ReadMarketplace(path); err == nil {
				in.Entries[m.Name] = entries
			}
		}
	}
	if data, found, err := fsx.ReadOptional(settingsPath); err == nil && found {
		in.Settings, _ = jsonx.DecodeObject(data)
	}
	return in, nil
}

// Classify gives the rule for a plugin id (name@marketplace).
func (in Inputs) Classify(id string) Verdict {
	name, market, _ := strings.Cut(id, "@")
	for _, p := range in.Plugins {
		if p.ID == id && p.ReadFromFolder != "" {
			return Verdict{Rule: InPlace, Text: "loads in place from " + p.ReadFromFolder}
		}
	}
	update := "claude plugin update " + id + " --scope user"
	if e := in.Entries[market][name]; e.SHA != "" {
		return Verdict{Rule: Pinned, Pin: e.SHA, Text: update}
	}
	if AutoUpdates(in.Settings, market, in.Getenv) {
		return Verdict{Rule: AutoUpdate, Text: "updated by Claude Code (" + market + " auto-updates)"}
	}
	return Verdict{Rule: Command, Text: update}
}
