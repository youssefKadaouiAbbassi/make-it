// Package mcplocal is makeit's own install of chrome-devtools-mcp under <stack>/mcp/chrome-devtools,
// the copy the plugin's .mcp.json starts with node instead of npx (spec item 13, speed).
package mcplocal

import (
	"encoding/json"
	"os"
	"path/filepath"

	"makeit/internal/latest"
)

// Package is the npm package; MCPFile the catalog file that pins its version.
const (
	Package = "chrome-devtools-mcp"
	MCPFile = "plugins/makeit/.mcp.json"
)

// Dir is the npm prefix of the local install.
func Dir(stack string) string { return filepath.Join(stack, "mcp", "chrome-devtools") }

// Pin is the version .mcp.json pins, or "" when it pins none.
func Pin(mcpJSON []byte) string { return latest.MCPPin(mcpJSON, Package) }

// Installed is the version of the local copy, or "" when it is missing or unreadable.
func Installed(stack string) string {
	b, err := os.ReadFile(filepath.Join(Dir(stack), "node_modules", Package, "package.json"))
	if err != nil {
		return ""
	}
	var p struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(b, &p) != nil {
		return ""
	}
	return p.Version
}
