package doctor

import (
	"os"
	"path/filepath"

	"makeit/internal/mcplocal"
)

// mcpPin is the version the plugin set pins: the one synced into <stack>/catalog (what setup
// resolved and the marketplace serves) when there is one, else the catalog in use.
func mcpPin(m Machine) string {
	if m.CatalogDir != "" {
		if b, err := os.ReadFile(filepath.Join(m.CatalogDir, filepath.FromSlash(mcplocal.MCPFile))); err == nil {
			if v := mcplocal.Pin(b); v != "" {
				return v
			}
		}
	}
	b, err := m.Catalog.ReadFile(mcplocal.MCPFile)
	if err != nil {
		return ""
	}
	return mcplocal.Pin(b)
}

// mcpLocal checks the local chrome-devtools-mcp copy .mcp.json starts in-process; without it, or
// with another version, npx starts the server, slower but working, so a gap is a warning. A node
// outside the package's range cannot run it either way.
func (r *report) mcpLocal(m Machine) {
	if m.StackDir == "" {
		return
	}
	pin := mcpPin(m)
	if pin == "" {
		return
	}
	const id, g = "mcp.chrome-devtools", "mcp"
	dir := mcplocal.Dir(m.StackDir)
	where := ""
	if m.Getenv != nil && m.Getenv("MAKEIT_DIR") != "" {
		where = " (MAKEIT_DIR is set for makeit; the plugin reads it only from Claude Code's own environment, else it looks in ~/.makeit)"
	}
	have := mcplocal.Installed(m.StackDir)
	if rng := mcplocal.Engines(m.StackDir); !mcplocal.Satisfies(m.NodeVersion, rng) {
		r.add(id, g, Warn, mcplocal.Package+" needs node "+rng+", this is "+m.NodeVersion+": the browser server cannot start", "install a newer node, then makeit update")
		return
	}
	switch have {
	case pin:
		r.add(id, g, OK, mcplocal.Package+" "+pin+" is installed in "+dir+where, "")
	case "":
		r.add(id, g, Warn, "no local "+mcplocal.Package+" in "+dir+": npx starts the browser server, slower"+where, "makeit update")
	default:
		r.add(id, g, Warn, "the local "+mcplocal.Package+" is "+have+" but the plugin set pins "+pin+": npx starts the server"+where, "makeit update")
	}
}
