// Package mcplocal is makeit's own install of chrome-devtools-mcp under <stack>/mcp/chrome-devtools,
// the copy the plugin's .mcp.json starts with node instead of npx (spec item 13, speed).
package mcplocal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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

// Engines is the node range the local copy's package.json asks for, or "".
func Engines(stack string) string {
	var p struct {
		Engines struct {
			Node string `json:"node"`
		} `json:"engines"`
	}
	if b, err := os.ReadFile(filepath.Join(Dir(stack), "node_modules", Package, "package.json")); err != nil || json.Unmarshal(b, &p) != nil {
		return ""
	}
	return p.Engines.Node
}

// Satisfies reports whether node version v ("v22.1.0") is in range rng, written with "||", "^x.y.z"
// and ">=x[.y[.z]]" only (what the package uses). A version or term it cannot read is accepted:
// doctor warns on what it can prove.
func Satisfies(v, rng string) bool {
	have, ok := triple(strings.TrimPrefix(v, "v"))
	if !ok || strings.TrimSpace(rng) == "" {
		return true
	}
	for _, term := range strings.Split(rng, "||") {
		term = strings.TrimSpace(term)
		switch {
		case strings.HasPrefix(term, "^"):
			lo, ok := triple(term[1:])
			if !ok {
				return true
			}
			if have[0] == lo[0] && !less(have, lo) {
				return true
			}
		case strings.HasPrefix(term, ">="):
			lo, ok := triple(term[2:])
			if !ok {
				return true
			}
			if !less(have, lo) {
				return true
			}
		default:
			return true
		}
	}
	return false
}

func less(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// triple reads "x", "x.y" or "x.y.z".
func triple(s string) (n [3]int, ok bool) {
	parts := strings.Split(s, ".")
	if len(parts) > 3 {
		return n, false
	}
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil {
			return n, false
		}
		n[i] = v
	}
	return n, true
}

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
