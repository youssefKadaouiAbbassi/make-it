package doctor

import (
	"context"
	"os"
	"path/filepath"
	"runtime"

	"makeit/internal/catalog"
	"makeit/internal/claudecli"
	"makeit/internal/execx"
	"makeit/internal/paths"
	"makeit/internal/platform"
)

// Gather reads this machine for Run, against the catalog compiled into the binary.
func Gather(ctx context.Context, version, target, digest string) (Machine, error) {
	roots, err := paths.Current()
	if err != nil {
		return Machine{}, err
	}
	present := func(name string) bool { _, ok := execx.LookPath(name); return ok }
	p := platform.Detect(runtime.GOOS, runtime.GOARCH, present, os.Getenv)
	m := Machine{
		Version: version, Target: target, CatalogDigest: digest,
		Platform:     p,
		Prereqs:      platform.Status(p, present),
		Routes:       platform.ClaudeRoutes(p, present),
		Installs:     claudecli.Locate(claudecli.MachineProbe(ctx, roots.Home)),
		Catalog:      catalog.Embedded,
		SettingsPath: filepath.Join(roots.Claude, "settings.json"),
		StatePath:    filepath.Join(roots.Stack, "state.json"),
	}
	if len(m.Installs) > 0 {
		m.Claude = &claudecli.CLI{Path: m.Installs[0].Path, Run: execx.Run}
		// A link in a Homebrew prefix is a cask's or, with Homebrew's node, npm's.
		if m.Installs[0].Source == "brew" {
			if resolved, err := filepath.EvalSymlinks(m.Installs[0].Path); err == nil {
				m.Installs[0].Source, m.BrewCask = brewOrigin(resolved)
			}
		}
	}
	return m, nil
}
