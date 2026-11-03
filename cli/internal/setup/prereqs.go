package setup

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"

	"makeit/internal/buildinfo"
	"makeit/internal/execx"
	"makeit/internal/fsx"
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
	for _, s := range platform.Status(e.Platform, e.Present) {
		r := s.Prereq
		if s.Present {
			continue
		}
		if rec := e.st.Prereqs[r.ID]; rec != nil && rec.InstalledBy == "makeit" && !slices.Contains(e.Reinstall, r.ID) {
			e.keep(r.ID, "removed by you")
			continue
		}
		rt := e.toolRoute(r, &refreshed)
		if rt.run == nil {
			e.needsYou = append(e.needsYou, NeedsYou{ID: r.ID, Manual: r.Manual})
			continue
		}
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

func (e *env) toolRoute(r platform.Prereq, refreshed *bool) toolRoute {
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
	if cmd, ok := platform.InstallCommand(r, e.Platform.PM, e.prompting(), e.Root); ok {
		name := string(e.Platform.PM)
		cmds := append(refresh(), cmd)
		return toolRoute{name: name, detail: name, argv: cmd.Argv, run: func(ctx context.Context) (string, error) {
			if err := e.runAll(ctx, cmds); err != nil {
				return "", err
			}
			record(name, cmd.Argv, "")
			return "", nil
		}}
	}
	if i := slices.IndexFunc(r.Via, func(v platform.Via) bool { return e.Present(v.Bin) }); i >= 0 {
		v := r.Via[i]
		cmd := v.Cmd(e.Roots.Home)
		cmd.Timeout = installTimeout
		return toolRoute{name: v.Bin, detail: "with " + v.Bin, argv: cmd.Argv, run: func(ctx context.Context) (string, error) {
			if err := e.runAll(ctx, []execx.Cmd{cmd}); err != nil {
				return "", err
			}
			record(v.Bin, cmd.Argv, "")
			return "", nil
		}}
	}
	if url, ok := r.PkgFile[e.Platform.PM]; ok && e.Platform.Arch == "amd64" {
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
	if rel := r.Release; rel != nil {
		if asset, _ := rel.Asset(rel.Version, e.Platform.OS, e.Platform.Arch); asset != "" {
			return toolRoute{name: "release", detail: "release v" + rel.Version + " from github.com/" + rel.Repo, run: func(ctx context.Context) (string, error) {
				path, err := tools.InstallRelease(ctx, e.client(), userAgent(), cmp.Or(e.ReleaseBase, "https://github.com"), *rel,
					e.Platform.OS, e.Platform.Arch, filepath.Join(e.Roots.Home, ".local", "bin"))
				if err != nil {
					return "", err
				}
				sha, err := fsx.FileSha256(path)
				if err != nil {
					return "", err
				}
				record("release", nil, sha)
				return "", nil
			}}
		}
	}
	if r.Required {
		return toolRoute{detail: r.Manual, run: func(context.Context) (string, error) {
			return "", fmt.Errorf("%s has no package route here; install it by hand: %s", r.ID, r.Manual)
		}}
	}
	return toolRoute{}
}

// client is the HTTP client for downloads: the one given, else one with a timeout.
func (e *env) client() *http.Client {
	if e.Client != nil {
		return e.Client
	}
	return &http.Client{Timeout: 10 * time.Minute}
}

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
