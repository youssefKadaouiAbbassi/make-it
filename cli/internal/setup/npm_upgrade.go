package setup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"makeit/internal/execx"
	"makeit/internal/plan"
	"makeit/internal/platform"
	"makeit/internal/state"
)

// npmUpgradeSteps move each tool makeit installed with npm (the two language servers, marked) to
// npm's newest release, read with npm view like the MCP servers' versions: setup, update and the
// daily run alike. A tool whose package folders are all gone was removed by you and stays so. The
// moves are kind "npm": rollback leaves them (never pin), so a run that only moved npm tools is
// not one rollback picks.
func (e *env) npmUpgradeSteps() []plan.Step {
	var steps []plan.Step
	down := false // one registry failure per run: the other tools are not asked again
	for _, r := range platform.Prereqs {
		rec := e.st.Prereqs[r.ID]
		if rec == nil || rec.InstalledBy != "makeit" || rec.Route != "npm" {
			continue
		}
		i := slices.Index(rec.Argv, "--prefix")
		if i < 0 || i+2 > len(rec.Argv) {
			continue
		}
		prefix, pkgs := rec.Argv[i+1], rec.Argv[i+2:]
		// npm's global layout: <prefix>/lib/node_modules, except on Windows (<prefix>/node_modules).
		modules := filepath.Join(prefix, "lib", "node_modules")
		if e.Platform.OS == "windows" {
			modules = filepath.Join(prefix, "node_modules")
		}
		have := map[string]string{}
		var present []string
		for _, p := range pkgs {
			if v := npmVersion(filepath.Join(modules, p, "package.json")); v != "" {
				have[p] = v
				present = append(present, p)
			}
		}
		if len(present) == 0 {
			continue
		}
		id := r.ID
		newest := map[string]string{}
		var behind []string
		steps = append(steps, plan.Step{ID: "upgrade." + id, Title: "Update " + id,
			Already: func(ctx context.Context) (bool, error) {
				if e.Offline || e.Run == nil || down {
					return true, nil
				}
				behind = nil
				for _, p := range present {
					res, err := e.Run(ctx, execx.Cmd{Argv: []string{"npm", "view", p, "version", "--fetch-retries=0"}, Timeout: 30 * time.Second})
					// npm can print a warning line first: the version is the last line.
					out := strings.Fields(res.Stdout)
					v := ""
					if len(out) > 0 {
						v = out[len(out)-1]
					}
					if err != nil || res.Code != 0 || v == "" {
						why := strings.TrimSpace(res.Text())
						if err != nil {
							why = err.Error()
						}
						if why == "" {
							why = "npm printed no version"
						}
						down = true
						e.keep(id, fmt.Sprintf("kept at %s: could not read the npm registry (%s)", have[p], why))
						return true, nil
					}
					newest[p] = v
					if v != have[p] {
						behind = append(behind, p)
					}
				}
				return len(behind) == 0, nil
			},
			Run: func(ctx context.Context) (string, error) {
				argv := []string{"npm", "install", "-g", "--prefix", prefix}
				for _, p := range behind {
					argv = append(argv, p+"@"+newest[p])
				}
				if err := e.runAll(ctx, []execx.Cmd{{Argv: argv, Timeout: installTimeout}}); err != nil {
					e.keep(id, fmt.Sprintf("kept at %s: npm could not install %s (%v)", have[behind[0]], newest[behind[0]], err))
					return "", plan.ErrSkip
				}
				var moved []string
				for _, p := range behind {
					e.changes = append(e.changes, state.Change{Kind: "npm", Item: p, From: have[p], To: newest[p]})
					moved = append(moved, p+" "+have[p]+" → "+newest[p])
				}
				return strings.Join(moved, ", "), nil
			}})
	}
	return steps
}

// npmVersion is the version in an installed package's package.json, or "" when it is not there.
func npmVersion(path string) string {
	b, err := os.ReadFile(path)
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
