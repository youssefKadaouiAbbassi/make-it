package doctor

import (
	"makeit/internal/mcplocal"
)

// mcpLocal checks the local chrome-devtools-mcp copy .mcp.json starts with node; without it npx
// starts the server, slower but working, so a gap is a warning.
func (r *report) mcpLocal(m Machine) {
	if m.StackDir == "" {
		return
	}
	b, err := m.Catalog.ReadFile(mcplocal.MCPFile)
	if err != nil {
		return
	}
	pin := mcplocal.Pin(b)
	if pin == "" {
		return
	}
	const id, g = "mcp.chrome-devtools", "mcp"
	dir := mcplocal.Dir(m.StackDir)
	switch have := mcplocal.Installed(m.StackDir); have {
	case pin:
		r.add(id, g, OK, mcplocal.Package+" "+pin+" is installed in "+dir, "")
	case "":
		r.add(id, g, Warn, "no local "+mcplocal.Package+" in "+dir+": npx starts the browser server, slower", "makeit update")
	default:
		r.add(id, g, Warn, "the local "+mcplocal.Package+" is "+have+" but the plugin set pins "+pin, "makeit update")
	}
}
