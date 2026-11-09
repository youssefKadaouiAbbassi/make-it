package setup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"makeit/internal/execx"
	"makeit/internal/mcplocal"
	"makeit/internal/plan"
)

// mcpLocalStep keeps the local copy of chrome-devtools-mcp at the version the plugin set pins
// (setup, update and the daily run alike): .mcp.json starts that copy with node, and npx when it
// is missing, so a failed install costs speed, not the server.
func (e *env) mcpLocalStep() plan.Step {
	pin := func(ctx context.Context) string {
		// catalogTree first: it resolves the newest plugin set, whose .mcp.json carries the pin.
		if _, err := e.catalogTree(ctx); err != nil || e.Catalog == nil {
			return ""
		}
		b, err := e.Catalog.ReadFile(mcplocal.MCPFile)
		if err != nil {
			return ""
		}
		return mcplocal.Pin(b)
	}
	dir := mcplocal.Dir(e.Roots.Stack)
	return plan.Step{ID: "mcp.chrome-devtools", Title: "Install " + mcplocal.Package + " in " + dir,
		Already: func(ctx context.Context) (bool, error) {
			if e.Offline || e.Run == nil {
				return true, nil
			}
			v := pin(ctx)
			if v == "" || mcplocal.Installed(e.Roots.Stack) == v {
				return true, nil
			}
			// No npm: node's own install brings it when node is still to come; otherwise npx serves.
			if !e.Present("npm") {
				if e.Present("node") || e.Background {
					return true, nil
				}
				return false, plan.ErrNotYet
			}
			return false, nil
		},
		Run: func(ctx context.Context) (string, error) {
			v := pin(ctx)
			argv := []string{"npm", "install", "--prefix", dir, "--no-audit", "--no-fund", mcplocal.Package + "@" + v}
			if err := e.runAll(ctx, []execx.Cmd{{Argv: argv, Timeout: installTimeout}}); err != nil {
				e.keep(mcplocal.Package, fmt.Sprintf("npm could not install %s %s (%v); npx starts the server meanwhile", mcplocal.Package, v, err))
				return "", plan.ErrSkip
			}
			return v, nil
		}}
}

// unMCPLocalStep removes that copy on uninstall; it is makeit's own, installed by the step above.
func (e *env) unMCPLocalStep() plan.Step {
	dir := mcplocal.Dir(e.Roots.Stack)
	return plan.Step{ID: "mcp.chrome-devtools", Title: "Remove " + dir,
		Already: func(context.Context) (bool, error) {
			_, err := os.Lstat(dir)
			return errors.Is(err, fs.ErrNotExist), nil
		},
		Run: func(context.Context) (string, error) {
			if err := os.RemoveAll(dir); err != nil {
				return "", err
			}
			_ = os.Remove(filepath.Dir(dir)) // <stack>/mcp, only once empty
			return "", nil
		}}
}
