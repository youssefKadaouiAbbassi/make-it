package doctor

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"makeit/internal/catalog"
	"makeit/internal/mcplocal"
)

// mcpMachine pins chrome-devtools-mcp in the catalog and puts a local copy at have ("" = none).
func mcpMachine(t *testing.T, pin, have string) Machine {
	t.Helper()
	m, _ := healthy(t)
	if pin != "" {
		write(t, filepath.Join(string(m.Catalog.(catalog.Dir)), "plugins", "makeit", ".mcp.json"),
			`{"mcpServers":{"chrome-devtools":{"args":["chrome-devtools-mcp@`+pin+`"]}}}`)
	}
	m.StackDir = t.TempDir()
	if have != "" {
		write(t, filepath.Join(mcplocal.Dir(m.StackDir), "node_modules", mcplocal.Package, "package.json"), `{"version":"`+have+`"}`)
	}
	return m
}

func TestMCPLocalCheck(t *testing.T) {
	for _, tc := range []struct {
		name, pin, have string
		level           Level
		in              string
	}{
		{"current", "1.12.0", "1.12.0", OK, "1.12.0"},
		{"missing", "1.12.0", "", Warn, "npx"},
		{"wrong version", "1.13.0", "1.12.0", Warn, "pins 1.13.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := byID(t, Run(context.Background(), mcpMachine(t, tc.pin, tc.have)), "mcp.chrome-devtools")
			if got.Level != tc.level || !strings.Contains(got.Message, tc.in) {
				t.Errorf("check %+v, want %s naming %q", got, tc.level, tc.in)
			}
			if tc.level == Warn && got.Fix != "makeit update" {
				t.Errorf("fix %q", got.Fix)
			}
		})
	}
}

// No pin in the catalog, or no stack directory known: no line.
func TestMCPLocalCheckAbsent(t *testing.T) {
	for _, m := range []Machine{mcpMachine(t, "", ""), func() Machine { m := mcpMachine(t, "1.12.0", ""); m.StackDir = ""; return m }()} {
		for _, c := range Run(context.Background(), m) {
			if c.ID == "mcp.chrome-devtools" {
				t.Errorf("unexpected %+v", c)
			}
		}
	}
}
