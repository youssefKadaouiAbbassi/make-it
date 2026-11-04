package setup

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"makeit/internal/claudecli"
	"makeit/internal/execx"
	"makeit/internal/plan"
	"makeit/internal/platform"
	"makeit/internal/state"
)

// installTimeout bounds a package install or the native installer's download.
const installTimeout = 30 * time.Minute

// claudeStep installs Claude Code by the first route of platform.ClaudeRoutes, or upgrades one
// below the catalog's floor. Any working install is adopted whatever its route.
func (e *env) claudeStep() plan.Step {
	routes := platform.ClaudeRoutes(e.Platform, e.Present)
	first, native := routes[0], routes[len(routes)-1]
	detail := "route " + first.Name + ":\n" + first.Text()
	if first.Name != native.Name {
		detail += "\nfallback when sudo is unavailable, native installer:\n" + native.Text()
	}
	return plan.Step{ID: "claude", Title: "Install Claude Code", Detail: detail,
		Already: func(ctx context.Context) (bool, error) {
			_, _, ok := e.findClaude(ctx)
			return ok, nil
		},
		Run: func(ctx context.Context) (string, error) {
			in, _, ok := e.findClaude(ctx)
			if ok {
				return "", nil
			}
			var route string
			var err error
			if in != nil {
				cask := ""
				if in.Source == "brew" {
					if resolved, rerr := filepath.EvalSymlinks(in.Path); rerr == nil {
						in.Source, cask = claudecli.BrewOrigin(resolved)
					}
				}
				route, err = "upgrade", e.runShell(ctx, claudecli.UpgradeCommand(in.Source, e.Platform.PM, cask))
			} else {
				route, err = e.installClaude(ctx, first, native)
			}
			if err != nil {
				return "", err
			}
			_, v, ok := e.findClaude(ctx)
			if !ok {
				return "", errors.New("after the install, Claude Code is still missing or below the minimum version; open a new terminal and run makeit doctor")
			}
			e.st.Prereqs = initMap(e.st.Prereqs)
			e.st.Prereqs["claude-code"] = &state.Prereq{InstalledBy: "makeit", Route: route}
			e.st.ClaudeCode.VersionSeen = v
			return "Claude Code " + v + " (" + route + ")", nil
		},
	}
}

// findClaude asks the claude PATH runs first (or the first one found) for its version and sets
// e.claude when it is at least the catalog's floor.
func (e *env) findClaude(ctx context.Context) (in *claudecli.Install, version string, usable bool) {
	found := e.Locate(ctx)
	if len(found) == 0 {
		return nil, "", false
	}
	in = &found[0]
	cli := &claudecli.CLI{Path: in.Path, Run: e.Run}
	v, err := cli.Version(ctx)
	if err != nil {
		return in, "", false
	}
	if c, err := claudecli.Compare(v, e.req.Requires.ClaudeCode.Hard); err != nil || c < 0 {
		return in, v, false
	}
	e.claude = cli
	// This check runs in the plan pass and again in the apply pass, each with a new CLI, so
	// each pass starts from fresh listings. Both cost 0.6 s: started together, for the checks
	// that follow to read from the cache.
	var warm sync.WaitGroup
	warm.Add(2)
	go func() { defer warm.Done(); _, _ = cli.Marketplaces(ctx) }()
	go func() { defer warm.Done(); _, _ = cli.Plugins(ctx) }()
	warm.Wait()
	return in, v, true
}

// installClaude runs the first route. A signed-repository route falls back to the native
// installer when sudo cannot run; a key that fails its check (exit 3) never falls back.
func (e *env) installClaude(ctx context.Context, first, native platform.Route) (string, error) {
	switch first.Name {
	case string(platform.APT), string(platform.DNF), string(platform.APK):
		if !e.canSudo(ctx) {
			break
		}
		r, err := e.Run(ctx, execx.Cmd{Argv: []string{"sh", "-c", e.shell(first.Argv[2])}, Inherit: e.prompting(), Timeout: installTimeout})
		switch {
		case err != nil:
			return "", err
		case r.Code == 3:
			return "", fmt.Errorf("the Claude Code signing key failed its check; nothing was installed: %s", r.Text())
		case r.Code != 0:
			return "", fmt.Errorf("installing Claude Code from the %s repository exited %d: %s", first.Name, r.Code, r.Text())
		}
		return first.Name, nil
	default:
		native = first
	}
	return native.Name, e.runAll(ctx, []execx.Cmd{{Argv: native.Argv, Inherit: e.prompting(), Timeout: installTimeout}})
}

// canSudo reports whether the repository route's sudo steps can run. At a terminal sudo asks for
// the password now (sudo -v); a refusal or a cancel picks the native installer, as does sudo -n
// failing when no one is there to type a password.
func (e *env) canSudo(ctx context.Context) bool {
	switch {
	case e.Root:
		return true
	case !e.Present("sudo"):
		return false
	}
	c := execx.Cmd{Argv: []string{"sudo", "-n", "true"}}
	if e.prompting() {
		c = execx.Cmd{Argv: []string{"sudo", "-v"}, Inherit: true}
	}
	r, err := e.Run(ctx, c)
	return err == nil && r.Code == 0
}

// shell prepares a script that calls sudo: as root sudo runs the command itself, and with no
// one to type a password it adds -n so it fails instead of waiting.
func (e *env) shell(script string) string {
	switch {
	case e.Root:
		return "sudo() { \"$@\"; }\n" + script
	case !e.prompting():
		return "sudo() { command sudo -n \"$@\"; }\n" + script
	}
	return script
}

// runShell runs a script through sh with e.shell's sudo.
func (e *env) runShell(ctx context.Context, script string) error {
	return e.runAll(ctx, []execx.Cmd{{Argv: []string{"sh", "-c", e.shell(script)}, Inherit: e.prompting(), Timeout: installTimeout}})
}
