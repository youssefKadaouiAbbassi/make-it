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

// setup resolves the newest plugin set into <stack>/catalog; doctor reads that pin, not the embedded one.
func TestMCPLocalCheckReadsStackCatalog(t *testing.T) {
	m := mcpMachine(t, "1.12.0", "1.13.0")
	m.CatalogDir = t.TempDir()
	write(t, filepath.Join(m.CatalogDir, "plugins", "makeit", ".mcp.json"), `{"args":["chrome-devtools-mcp@1.13.0"]}`)
	if got := byID(t, Run(context.Background(), m), "mcp.chrome-devtools"); got.Level != OK {
		t.Errorf("check %+v, want ok against the stack catalog's 1.13.0", got)
	}
}

func TestMCPLocalCheckNodeRange(t *testing.T) {
	m := mcpMachine(t, "1.12.0", "")
	write(t, filepath.Join(mcplocal.Dir(m.StackDir), "node_modules", mcplocal.Package, "package.json"),
		`{"version":"1.12.0","engines":{"node":"^20.19.0 || ^22.12.0 || >=23"}}`)
	for v, level := range map[string]Level{"v18.19.1": Warn, "v22.11.0": Warn, "v22.12.0": OK, "": OK} {
		m.NodeVersion = v
		got := byID(t, Run(context.Background(), m), "mcp.chrome-devtools")
		if got.Level != level {
			t.Errorf("node %q: %+v, want %s", v, got, level)
		}
		if level == Warn && (!strings.Contains(got.Message, "^20.19.0 || ^22.12.0 || >=23") || !strings.Contains(got.Message, v)) {
			t.Errorf("node %q: message %q lacks the range or the version", v, got.Message)
		}
	}
}

// MAKEIT_DIR set for makeit does not reach the plugin: the line says where the wrapper looks.
func TestMCPLocalCheckMakeitDirNote(t *testing.T) {
	m := mcpMachine(t, "1.12.0", "1.12.0")
	m.Getenv = func(k string) string {
		if k == "MAKEIT_DIR" {
			return m.StackDir
		}
		return ""
	}
	if got := byID(t, Run(context.Background(), m), "mcp.chrome-devtools"); !strings.Contains(got.Message, "~/.makeit") {
		t.Errorf("message %q does not say the plugin looks in ~/.makeit", got.Message)
	}
}

// A wrong-version copy is never run, so its engines are irrelevant: the version is reported first.
func TestMCPLocalCheckVersionBeforeNodeRange(t *testing.T) {
	m := mcpMachine(t, "1.13.0", "")
	write(t, filepath.Join(mcplocal.Dir(m.StackDir), "node_modules", mcplocal.Package, "package.json"),
		`{"version":"1.12.0","engines":{"node":"^20.19.0"}}`)
	m.NodeVersion = "v18.19.1"
	if got := byID(t, Run(context.Background(), m), "mcp.chrome-devtools"); !strings.Contains(got.Message, "pins 1.13.0") {
		t.Errorf("message %q, want the version mismatch, not the node range", got.Message)
	}
}

func TestMCPLocalNodeRangeCarriesMakeitDirNote(t *testing.T) {
	m := mcpMachine(t, "1.12.0", "")
	write(t, filepath.Join(mcplocal.Dir(m.StackDir), "node_modules", mcplocal.Package, "package.json"),
		`{"version":"1.12.0","engines":{"node":"^20.19.0"}}`)
	m.NodeVersion = "v18.19.1"
	m.Getenv = func(k string) string { return map[string]string{"MAKEIT_DIR": m.StackDir}[k] }
	if got := byID(t, Run(context.Background(), m), "mcp.chrome-devtools"); !strings.Contains(got.Message, "~/.makeit") {
		t.Errorf("message %q lacks the MAKEIT_DIR note", got.Message)
	}
}
