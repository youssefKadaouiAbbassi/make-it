package doctor

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"makeit/internal/buildinfo"
	"makeit/internal/catalog"
	"makeit/internal/claudecli"
	"makeit/internal/execx"
	"makeit/internal/paths"
	"makeit/internal/platform"
	"makeit/internal/selfupdate"
	"makeit/internal/state"
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
		Getenv:  os.Getenv,
		Root:    os.Geteuid() == 0,
		HasSudo: present("sudo"),
		Version: version, Target: target, CatalogDigest: digest,
		Platform:     p,
		Prereqs:      platform.Status(p, present),
		Routes:       platform.ClaudeRoutes(p, present),
		Installs:     claudecli.Locate(claudecli.MachineProbe(ctx, roots.Home)),
		Catalog:      catalog.Embedded,
		SettingsPath: filepath.Join(roots.Claude, "settings.json"),
		StatePath:    filepath.Join(roots.Stack, "state.json"),
		ClaudeDir:    roots.Claude,
		StackDir:     roots.Stack,
	}
	if exe, err := os.Executable(); err == nil {
		if r, err := filepath.EvalSymlinks(exe); err == nil {
			exe = r
		}
		m.Exe = exe
	}
	self := selfupdate.Env{Roots: roots, Exe: m.Exe, Version: version, Repo: buildinfo.Repo, Keys: buildinfo.Keys()}
	m.Self, m.Dev = self.Status(), self.Dev()
	// A directory or git catalog is checked against its synced copy, not the embedded one.
	m.CatalogDir = filepath.Join(roots.Stack, "catalog")
	if st, _, err := state.LoadState(m.StatePath); err == nil {
		m.CatalogRecord = st.Catalog
		if rec := st.Prereqs["node"]; rec != nil && rec.InstalledBy == "makeit" && rec.Route == "nodedist" {
			m.NodeDist, m.NodeLink = true, filepath.Join(roots.Home, ".local", "bin", "node")
		}
		if k := st.Catalog.Kind; k != "" && k != "embedded" && catalog.ReadMarker(m.CatalogDir).Digest != "" {
			m.Catalog = catalog.Dir(m.CatalogDir)
		}
	}
	man, err := catalog.EmbeddedManifest()
	if err != nil {
		return Machine{}, err
	}
	for _, f := range man.Files {
		if strings.HasPrefix(f.Path, "templates/global/") {
			m.Globals = append(m.Globals, f.Path)
		}
	}
	m.NodePath, _ = execx.LookPath("node")
	if res, err := execx.Run(ctx, execx.Cmd{Argv: []string{"node", "-v"}, Timeout: 5 * time.Second}); err == nil && res.Code == 0 {
		m.NodeVersion = strings.TrimSpace(res.Stdout)
	}
	if len(m.Installs) > 0 {
		m.Claude = &claudecli.CLI{Path: m.Installs[0].Path, Run: execx.Run}
		// A link in a Homebrew prefix is a cask's or, with Homebrew's node, npm's.
		if m.Installs[0].Source == "brew" {
			if resolved, err := filepath.EvalSymlinks(m.Installs[0].Path); err == nil {
				m.Installs[0].Source, m.BrewCask = claudecli.BrewOrigin(resolved)
			}
		}
	}
	return m, nil
}
