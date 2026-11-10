package setup

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"makeit/internal/execx"
	"makeit/internal/mcplocal"
	"makeit/internal/plan"
	"makeit/internal/platform"
	"makeit/internal/state"
	"makeit/internal/tools"
)

// nodeMarker is the file that ends a node version folder: it holds the tarball's sha256, and goes
// through the generation, so rollback finds the folder from it.
const nodeMarker = ".makeit-node"

// nodeLinks are the programs of node's bin folder that makeit links into ~/.local/bin.
var nodeLinks = []string{"node", "npm", "npx"}

func (e *env) nodeRoot() string {
	return filepath.Join(e.Roots.Home, ".local", "share", "makeit", "node")
}

func (e *env) localBin() string { return filepath.Join(e.Roots.Home, ".local", "bin") }

// nodeDistHere: nodejs.org builds fit this machine (glibc Linux on x86-64 or arm64; Alpine's musl
// and the Homebrew and winget routes keep their own).
func (e *env) nodeDistHere() bool {
	p := e.Platform
	return p.OS == "linux" && p.PM != platform.APK && p.PM != platform.Brew && (p.Arch == "amd64" || p.Arch == "arm64")
}

// nodeUsable is the node row's presence: a node makeit linked counts (PATH order is doctor's
// business); else node on PATH, inside the browser MCP's floor where the nodejs.org route applies.
// An unreadable version and an offline run take the node there is.
func (e *env) nodeUsable(present func(string) bool) bool {
	if rec := e.st.Prereqs["node"]; rec != nil && rec.InstalledBy == "makeit" && rec.Route == "nodedist" {
		if _, err := os.Lstat(filepath.Join(e.localBin(), "node")); err == nil {
			return true
		}
	}
	if !present("node") {
		return false
	}
	if !e.nodeDistHere() || e.Offline || e.Run == nil || !e.Present("node") {
		return true
	}
	r, err := e.Run(context.Background(), execx.Cmd{Argv: []string{"node", "-v"}, Timeout: 5 * time.Second})
	return err != nil || r.Code != 0 || mcplocal.Satisfies(strings.TrimSpace(r.Stdout), mcplocal.NodeFloor)
}

// newestNode is the newest Node LTS, looked up once per run.
func (e *env) newestNode(ctx context.Context) (string, error) {
	e.nodeOnce.Do(func() {
		if e.Offline {
			e.nodeErr = errOffline
			return
		}
		// Like plugins: the daily run leaves a release rollback undid, a run by hand takes it.
		var skip []string
		if e.Background {
			skip = e.st.ToolSkips["node"]
		}
		e.nodeVer, e.nodeErr = tools.NewestNodeLTS(ctx, e.client(), userAgent(), cmp.Or(e.NodeBase, "https://nodejs.org"), skip)
	})
	return e.nodeVer, e.nodeErr
}

// installNode downloads Node ver, checks it, unpacks it into its version folder and writes the
// folder's marker through the generation under op; it returns the tarball's sha256.
func (e *env) installNode(ctx context.Context, op, ver string) (string, error) {
	data, sum, err := tools.FetchNode(ctx, e.client(), userAgent(), cmp.Or(e.NodeBase, "https://nodejs.org"), ver, e.Platform.Arch)
	if err != nil {
		return "", err
	}
	dest := filepath.Join(e.nodeRoot(), ver)
	tmp := dest + ".tmp"
	if err := os.RemoveAll(tmp); err != nil {
		return "", err
	}
	if err := os.MkdirAll(e.nodeRoot(), 0o755); err != nil {
		return "", err
	}
	if err := tools.UnpackNode(data, tmp); err != nil {
		return "", fmt.Errorf("unpack node %s: %w", ver, err)
	}
	if err := os.RemoveAll(dest); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return "", err
	}
	if err := e.write(op, filepath.Join(dest, nodeMarker), []byte(sum), 0o644); err != nil {
		_ = os.RemoveAll(dest)
		return "", err
	}
	return sum, nil
}

// ownedLink: path is a symlink into makeit's node folder.
func (e *env) ownedLink(path string) bool {
	target, err := os.Readlink(path)
	if err != nil {
		return false
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}
	return strings.HasPrefix(filepath.Clean(target), e.nodeRoot()+string(filepath.Separator))
}

// linkNode points the three links at version ver; one that is absent is made, one of makeit's
// repointed, anything else left alone and returned by name.
func (e *env) linkNode(ver string) (left []string, err error) {
	if err := os.MkdirAll(e.localBin(), 0o755); err != nil {
		return nil, err
	}
	for _, n := range nodeLinks {
		link := filepath.Join(e.localBin(), n)
		if _, err := os.Lstat(link); err == nil && !e.ownedLink(link) {
			left = append(left, n)
			continue
		}
		tmp := link + ".makeit-tmp"
		_ = os.Remove(tmp)
		if err := os.Symlink(filepath.Join("..", "share", "makeit", "node", ver, "bin", n), tmp); err != nil {
			return left, err
		}
		if err := os.Rename(tmp, link); err != nil {
			return left, err
		}
	}
	return left, nil
}

func (e *env) keepNodeLinks(left []string) {
	if len(left) > 0 {
		e.keep("node", "~/.local/bin/"+strings.Join(left, ", ~/.local/bin/")+" is yours (not a link into makeit's node), so it still points where you set it")
	}
}

// pathFirst puts dir first on this process's PATH, so the npm routes later in the run find the
// node just linked.
func pathFirst(dir string) {
	p := os.Getenv("PATH")
	if !slices.Contains(filepath.SplitList(p), dir) {
		_ = os.Setenv("PATH", dir+string(filepath.ListSeparator)+p)
	}
}

// nodeRoute installs the newest Node LTS from nodejs.org, before the package route.
func (e *env) nodeRoute(record func(route string, argv []string, sha string), refresh func() []execx.Cmd) toolRoute {
	return toolRoute{name: "nodedist", detail: "the newest Node LTS from nodejs.org, into ~/.local/share/makeit/node", run: func(ctx context.Context) (string, error) {
		v, err := e.newestNode(ctx)
		if err != nil {
			return "", err
		}
		sum, err := e.installNode(ctx, "prereq.node", v)
		if err != nil {
			return "", err
		}
		if err := e.nodeStarts(ctx, v, refresh); err != nil {
			return "", err
		}
		left, err := e.linkNode(v)
		if err != nil {
			return "", err
		}
		record("nodedist", nil, sum)
		e.st.Prereqs["node"].Version = v
		e.keepNodeLinks(left)
		pathFirst(e.localBin())
		return "v" + v, nil
	}}
}

// nodeStarts runs the node just unpacked. nodejs.org's build needs libatomic, which slim images
// lack: when that is the failure, the package is installed and node tried again.
func (e *env) nodeStarts(ctx context.Context, ver string, refresh func() []execx.Cmd) error {
	probe := execx.Cmd{Argv: []string{filepath.Join(e.nodeRoot(), ver, "bin", "node"), "-v"}, Timeout: 30 * time.Second}
	try := func() error {
		r, err := e.Run(ctx, probe)
		if err != nil {
			return err
		}
		if r.Code != 0 {
			return fmt.Errorf("node %s does not start (%s)", ver, r.Text())
		}
		return nil
	}
	err := try()
	if err == nil || !strings.Contains(err.Error(), "libatomic") {
		return err
	}
	var install execx.Cmd
	switch e.Platform.PM {
	case platform.APT:
		install = e.sudo("env", "DEBIAN_FRONTEND=noninteractive", "apt-get", "install", "-y", "libatomic1")
	case platform.DNF:
		install = e.sudo("dnf", "install", "-y", "libatomic")
	case platform.Pacman:
		install = e.sudo("pacman", "-S", "--noconfirm", "gcc-libs")
	default:
		return fmt.Errorf("%w: install libatomic with your package manager", err)
	}
	if ierr := e.runAll(ctx, append(refresh(), install)); ierr != nil {
		return fmt.Errorf("%w; installing libatomic failed: %v", err, ierr)
	}
	return try()
}

// nodeUpgradeSteps move the node makeit installed from nodejs.org to the newest LTS and repoint
// the links; the folder of the version it leaves stays until the next update.
func (e *env) nodeUpgradeSteps() []plan.Step {
	rec := e.st.Prereqs["node"]
	if rec == nil || rec.InstalledBy != "makeit" || rec.Route != "nodedist" || !e.nodeDistHere() {
		return nil
	}
	if _, err := os.Lstat(filepath.Join(e.localBin(), "node")); err != nil {
		return nil // removed by you: setup's own rule says so
	}
	return []plan.Step{{ID: "upgrade.node", Title: "Update node",
		Already: func(ctx context.Context) (bool, error) {
			v, err := e.newestNode(ctx)
			if err != nil {
				if !errors.Is(err, errOffline) {
					e.keep("node", fmt.Sprintf("kept at %s: could not read its releases (%v)", cmp.Or(rec.Version, "its version"), err))
				}
				return true, nil
			}
			return rec.Version == v, nil
		},
		Run: func(ctx context.Context) (string, error) {
			v, err := e.newestNode(ctx)
			if err != nil {
				return "", err
			}
			sum, err := e.installNode(ctx, "upgrade.node", v)
			if err != nil {
				e.keep("node", fmt.Sprintf("kept at %s: node %s could not be installed (%v)", cmp.Or(rec.Version, "its version"), v, err))
				return "", plan.ErrSkip
			}
			left, err := e.linkNode(v)
			if err != nil {
				return "", err
			}
			e.keepNodeLinks(left)
			from := rec.Version
			rec.Version, rec.Sha256 = v, sum
			e.changes = append(e.changes, state.Change{Kind: "tool", Item: "node", From: from, To: v})
			e.pruneNode(v, from)
			return cmp.Or(from, "?") + " → " + v, nil
		}}}
}

// pruneNode removes every version folder but the two in use: the new one and the one before it.
func (e *env) pruneNode(keep ...string) {
	if slices.Contains(keep, "") {
		return
	}
	entries, _ := os.ReadDir(e.nodeRoot())
	for _, d := range entries {
		if !slices.Contains(keep, d.Name()) {
			_ = os.RemoveAll(filepath.Join(e.nodeRoot(), d.Name()))
		}
	}
}

// nodeRollbackSteps undo the node folder g wrote: remove it and point the links back at the
// version before (an update), or remove them (the first install). The marker file itself is
// restored by the file steps before these run.
func (e *env) nodeRollbackSteps(g *state.Generation) []plan.Step {
	var steps []plan.Step
	for _, op := range g.Manifest.Ops {
		if op.ID != "prereq.node" && op.ID != "upgrade.node" {
			continue
		}
		dest := filepath.Dir(e.Roots.Abs(op.Path))
		ver, from := filepath.Base(dest), ""
		for _, c := range g.Manifest.Changes {
			if c.Kind == "tool" && c.Item == "node" {
				from = c.From
			}
		}
		steps = append(steps, plan.Step{ID: "node.rollback", Title: "Put node back to " + cmp.Or(from, "no makeit node"),
			Run: func(context.Context) (string, error) {
				if filepath.Dir(dest) != e.nodeRoot() {
					return "", plan.ErrSkip
				}
				if err := os.RemoveAll(dest); err != nil {
					return "", err
				}
				rec := e.st.Prereqs["node"]
				if prev, err := os.ReadFile(filepath.Join(e.nodeRoot(), from, nodeMarker)); from != "" && err == nil {
					left, err := e.linkNode(from)
					if err != nil {
						return "", err
					}
					e.keepNodeLinks(left)
					if rec != nil {
						rec.Version, rec.Sha256 = from, string(prev)
					}
				} else {
					e.removeNodeLinks()
					_ = os.Remove(e.nodeRoot()) // empty after a first install
					delete(e.st.Prereqs, "node")
				}
				if e.st.ToolSkips == nil {
					e.st.ToolSkips = map[string][]string{}
				}
				if !slices.Contains(e.st.ToolSkips["node"], "v"+ver) {
					e.st.ToolSkips["node"] = append(e.st.ToolSkips["node"], "v"+ver)
				}
				return "", nil
			}})
	}
	return steps
}

// removeNodeLinks removes the links that are makeit's and returns the names of the others that exist.
func (e *env) removeNodeLinks() (left []string) {
	for _, n := range nodeLinks {
		link := filepath.Join(e.localBin(), n)
		switch {
		case e.ownedLink(link):
			_ = os.Remove(link)
		default:
			if _, err := os.Lstat(link); err == nil {
				left = append(left, n)
			}
		}
	}
	return left
}

// nodeUninstallStep removes makeit's node folder and the links into it; a link the user changed
// stays, and is reported.
func (e *env) nodeUninstallStep() plan.Step {
	return plan.Step{ID: "tool.node", Title: "Remove " + e.nodeRoot(),
		Already: func(context.Context) (bool, error) {
			if _, err := os.Stat(e.nodeRoot()); errors.Is(err, os.ErrNotExist) {
				delete(e.st.Prereqs, "node")
				return true, nil
			}
			return false, nil
		},
		Run: func(context.Context) (string, error) {
			left := e.removeNodeLinks()
			if err := os.RemoveAll(e.nodeRoot()); err != nil {
				return "", err
			}
			_ = os.Remove(filepath.Dir(e.nodeRoot())) // the makeit folder, when nothing else is in it
			delete(e.st.Prereqs, "node")
			if len(left) > 0 {
				e.keep("node", "~/.local/bin/"+strings.Join(left, ", ~/.local/bin/")+" is yours (not a link into makeit's node), so it stays")
			}
			return "", nil
		}}
}
