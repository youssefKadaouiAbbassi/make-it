package setup

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"makeit/internal/buildinfo"
	"makeit/internal/execx"
	"makeit/internal/fetch"
	"makeit/internal/fsx"
	"makeit/internal/pathenv"
	"makeit/internal/plan"
	"makeit/internal/platform"
	"makeit/internal/state"
	"makeit/internal/tools"
)

// prereqSteps installs every tool that applies to this machine and is missing, each by the first
// route the machine has: its package manager, a toolchain, the vendor's package file, the
// project's release binary. A tool with no route here is not a step: it is reported as "needs
// you" with its manual route (a required one is a step that fails). A tool makeit installed and
// the user removed since stays removed. The daily run plans no tool: installs need a terminal.
func (e *env) prereqSteps() []plan.Step {
	if e.Background {
		return nil
	}
	var steps []plan.Step
	refreshed := false
	// npm arrives with node, which setup installs before the rows that need it (table order), so
	// the npm route applies even where node is still to be installed.
	nodePlanned := !e.Present("node")
	avail := func(bin string) bool { return e.Present(bin) || (bin == "npm" && nodePlanned) }
	// Package routes need root, except brew and winget. Without it a required row still tries
	// (and fails); any other row takes a route that needs no root, or is left to the user.
	canPackages := e.Root || e.Present("sudo") || e.Platform.PM == platform.Brew || e.Platform.PM == platform.WinGet
	// A tool already on disk in a folder makeit puts on PATH is present even before a new terminal
	// sees it: never installed twice, never taken for one the user removed.
	dirs := pathenv.Dirs(e.Roots.Home, e.getenv, e.exists)
	present := func(bin string) bool {
		if e.Present(bin) {
			return true
		}
		for _, d := range dirs {
			for _, name := range []string{bin, bin + ".exe"} {
				if fi, err := os.Stat(filepath.Join(d, name)); err == nil && fi.Mode().IsRegular() {
					return true
				}
			}
		}
		return false
	}
	usable := func(bin string) bool {
		if bin == "node" {
			return e.nodeUsable(present)
		}
		return present(bin)
	}
	for _, s := range platform.Status(e.Platform, usable) {
		r := s.Prereq
		if s.Present {
			continue
		}
		if rec := e.st.Prereqs[r.ID]; rec != nil && rec.InstalledBy == "makeit" && !slices.Contains(e.Reinstall, r.ID) && !(r.ID == "node" && e.Present("node")) {
			e.keep(r.ID, "removed by you")
			continue
		}
		rt := e.toolRoute(r, &refreshed, avail, canPackages)
		if rt.run == nil {
			e.needsYou = append(e.needsYou, NeedsYou{ID: r.ID, Manual: r.Manual})
			continue
		}
		nodePlanned = nodePlanned || r.ID == "node"
		steps = append(steps, plan.Step{ID: "prereq." + r.ID, Title: "Install " + r.ID, Detail: rt.detail, Command: rt.argv,
			Already: func(context.Context) (bool, error) { return false, nil }, Run: rt.run})
	}
	return steps
}

// toolRoute is how one missing tool gets installed here.
type toolRoute struct {
	name, detail string
	argv         []string
	run          func(ctx context.Context) (string, error)
}

func (e *env) toolRoute(r platform.Prereq, refreshed *bool, avail func(string) bool, canPackages bool) toolRoute {
	record := func(route string, argv []string, sha string) {
		e.st.Prereqs = initMap(e.st.Prereqs)
		e.st.Prereqs[r.ID] = &state.Prereq{InstalledBy: "makeit", Route: route, Argv: argv, Sha256: sha}
	}
	// The package index once per run, before the first install: slim images ship without one.
	refresh := func() []execx.Cmd {
		if *refreshed {
			return nil
		}
		*refreshed = true
		switch e.Platform.PM {
		case platform.APT:
			return []execx.Cmd{e.sudo("apt-get", "update")}
		case platform.APK:
			return []execx.Cmd{e.sudo("apk", "update")}
		}
		return nil
	}
	if r.NodeDist && e.nodeDistHere() && !e.Offline {
		return e.nodeRoute(record, refresh)
	}
	packages := canPackages || r.Required
	if cmd, ok := platform.InstallCommand(r, e.Platform.PM, e.prompting(), e.Root); ok && packages {
		name := string(e.Platform.PM)
		return toolRoute{name: name, detail: name, argv: cmd.Argv, run: func(ctx context.Context) (string, error) {
			// The index refresh goes to the first install that runs, whichever row that is.
			if err := e.runAll(ctx, append(refresh(), cmd)); err != nil {
				return "", err
			}
			record(name, cmd.Argv, "")
			return "", nil
		}}
	}
	if i := slices.IndexFunc(r.Via, func(v platform.Via) bool { return avail(v.Bin) }); i >= 0 {
		v := r.Via[i]
		cmd := v.Cmd(e.Roots.Home)
		cmd.Timeout = installTimeout
		// On Windows npm puts its shims in the prefix itself, not in <prefix>/bin.
		if e.Platform.OS == "windows" && v.Bin == "npm" {
			if j := slices.Index(cmd.Argv, "--prefix"); j >= 0 && j+1 < len(cmd.Argv) {
				cmd.Argv[j+1] = filepath.Join(e.Roots.Home, ".local", "bin")
			}
		}
		return toolRoute{name: v.Bin, detail: "with " + v.Bin, argv: cmd.Argv, run: func(ctx context.Context) (string, error) {
			if err := e.runAll(ctx, []execx.Cmd{cmd}); err != nil {
				return "", err
			}
			record(v.Bin, cmd.Argv, "")
			return "", nil
		}}
	}
	if url, ok := r.PkgFile[e.Platform.PM]; ok && e.Platform.Arch == "amd64" && packages {
		kind := "deb"
		if e.Platform.PM == platform.DNF {
			kind = "rpm"
		}
		return toolRoute{name: kind, detail: "Google's ." + kind + " from dl.google.com", run: func(ctx context.Context) (string, error) {
			path, err := tools.FetchPackageFile(ctx, e.client(), userAgent(), url)
			if err != nil {
				return "", err
			}
			defer func() { _ = os.RemoveAll(filepath.Dir(path)) }()
			var install execx.Cmd
			if kind == "deb" {
				install = e.sudo("env", "DEBIAN_FRONTEND=noninteractive", "apt-get", "install", "-y", path)
			} else {
				install = e.sudo("dnf", "install", "-y", path)
			}
			if err := e.runAll(ctx, append(refresh(), install)); err != nil {
				return "", err
			}
			record(kind, nil, "")
			return "", nil
		}}
	}
	if rel := r.Release; rel != nil && rel.Ships(e.Platform.OS, e.Platform.Arch) && !e.Offline {
		return toolRoute{name: "release", detail: "the newest release from github.com/" + rel.Repo, run: func(ctx context.Context) (string, error) {
			v, err := e.newestRelease(ctx, r.ID)
			if err != nil {
				return "", err
			}
			name, data, err := tools.FetchRelease(ctx, e.client(), userAgent(), cmp.Or(e.ReleaseBase, "https://github.com"), *rel, v, e.Platform.OS, e.Platform.Arch)
			if err != nil {
				return "", err
			}
			// Through the generation, like every file setup writes: rollback removes it again.
			if err := e.write("prereq."+r.ID, filepath.Join(e.Roots.Home, ".local", "bin", name), data, 0o755); err != nil {
				return "", err
			}
			record("release", nil, fsx.Sha256(data))
			e.st.Prereqs[r.ID].Version = v
			return "v" + v, nil
		}}
	}
	if r.Required {
		return toolRoute{detail: r.Manual, run: func(context.Context) (string, error) {
			return "", fmt.Errorf("%s has no package route here; install it by hand: %s", r.ID, r.Manual)
		}}
	}
	return toolRoute{}
}

// client is the HTTP client for one download: the one given (tests), else a new one whose roots
// are read from the CA bundle files now, so a download after this run installed ca-certificates
// sees them.
func (e *env) client() *http.Client {
	if e.Client != nil {
		return e.Client
	}
	return fetch.Client(10*time.Minute, fetch.BundlePaths(e.getenv))
}

// CheckReinstall refuses a --reinstall id the tool table does not know.
func CheckReinstall(ids []string) error {
	for _, id := range ids {
		if !slices.ContainsFunc(platform.Prereqs, func(r platform.Prereq) bool { return r.ID == id }) {
			var known []string
			for _, r := range platform.Prereqs {
				known = append(known, r.ID)
			}
			return fmt.Errorf("no tool named %q; the tools: %s", id, strings.Join(known, ", "))
		}
	}
	return nil
}

func (e *env) checkReinstall() error { return CheckReinstall(e.Reinstall) }

func userAgent() string { return "makeit/" + buildinfo.Version }

// runAll runs cmds in order and stops at the first that fails, naming it and its output.
func (e *env) runAll(ctx context.Context, cmds []execx.Cmd) error {
	for _, c := range cmds {
		r, err := e.Run(ctx, c)
		if err != nil {
			return fmt.Errorf("%s: %w", execx.Quote(c.Argv), err)
		}
		if r.Code != 0 {
			return fmt.Errorf("%s exited %d: %s", execx.Quote(c.Argv), r.Code, r.Text())
		}
	}
	return nil
}
