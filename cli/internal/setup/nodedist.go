package setup

import (
	"cmp"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"makeit/internal/execx"
	"makeit/internal/fsx"
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

// nodeMarkerPath is the marker of version ver's folder.
func (e *env) nodeMarkerPath(ver string) string { return filepath.Join(e.nodeRoot(), ver, nodeMarker) }

// dropBrokenNodeRecord forgets a nodedist record whose folder is gone (an uninstall, then a
// rollback of something else) or whose node link dangles: that node is not installed, so setup
// installs it again instead of calling it removed by you.
func (e *env) dropBrokenNodeRecord() {
	rec := e.st.Prereqs["node"]
	if rec == nil || rec.InstalledBy != "makeit" || rec.Route != "nodedist" {
		return
	}
	link := filepath.Join(e.localBin(), "node")
	_, lerr := os.Lstat(link)
	_, serr := os.Stat(link)
	if _, err := os.Stat(e.nodeMarkerPath(rec.Version)); err != nil || (lerr == nil && serr != nil) {
		delete(e.st.Prereqs, "node")
	}
}

// nodeVersion is `node -v` of the node on PATH, "" when it cannot be read.
func (e *env) nodeVersion() string {
	if e.Run == nil {
		return ""
	}
	r, err := e.Run(context.Background(), execx.Cmd{Argv: []string{"node", "-v"}, Timeout: 5 * time.Second})
	if err != nil || r.Code != 0 {
		return ""
	}
	return strings.TrimSpace(r.Stdout)
}

// nodeUsable is the node row's presence: a node makeit linked counts (PATH order is doctor's
// business); else node on PATH, inside the browser MCP's floor where the nodejs.org route applies.
// An unreadable version and an offline run take the node there is.
func (e *env) nodeUsable(present func(string) bool) bool {
	if slices.Contains(e.Reinstall, "node") {
		return false
	}
	if rec := e.st.Prereqs["node"]; rec != nil && rec.InstalledBy == "makeit" && rec.Route == "nodedist" {
		if _, err := os.Stat(filepath.Join(e.localBin(), "node")); err == nil {
			return true
		}
	}
	if !present("node") {
		return false
	}
	if !e.nodeDistHere() || e.Offline || !e.Present("node") {
		return true
	}
	return mcplocal.Satisfies(e.nodeVersion(), mcplocal.NodeFloor)
}

// nodePathAtStart puts ~/.local/bin first on this process's PATH when makeit installed node, so a
// run after the install (the daily one) uses makeit's node and npm.
func (e *env) nodePathAtStart() {
	if rec := e.st.Prereqs["node"]; rec != nil && rec.InstalledBy == "makeit" && rec.Route == "nodedist" {
		pathFirst(e.localBin())
	}
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

// nodeUndo is what a run changed about node, kept in the generation (node.json) so rollback, and
// a failure part way, can put it back: the folder (unless it was there before), the links, and
// the earlier record.
type nodeUndo struct {
	Op         string            `json:"op"`
	Version    string            `json:"version"`
	DirExisted bool              `json:"dirExisted"`
	Prev       *state.Prereq     `json:"prev"`
	Links      map[string]string `json:"links"` // name -> the target a link of makeit's had; absent for none
}

const nodeUndoFile = "node.json"

// beginNode captures the state a run is about to change.
func (e *env) beginNode(op, ver string) *nodeUndo {
	u := &nodeUndo{Op: op, Version: ver, Links: map[string]string{}}
	_, err := os.Stat(filepath.Join(e.nodeRoot(), ver))
	u.DirExisted = err == nil
	if rec := e.st.Prereqs["node"]; rec != nil {
		c := *rec
		u.Prev = &c
	}
	for _, n := range nodeLinks {
		if link := filepath.Join(e.localBin(), n); e.ownedLink(link) {
			u.Links[n], _ = os.Readlink(link)
		}
	}
	return u
}

// undoNode puts back what u captured: the links makeit owns (or that are gone) go to their earlier
// target or are removed, the folder is removed unless it was there before, the record returns.
func (e *env) undoNode(u *nodeUndo) error {
	for _, n := range nodeLinks {
		link := filepath.Join(e.localBin(), n)
		if _, err := os.Lstat(link); err == nil && !e.ownedLink(link) {
			continue // the user's
		}
		_ = os.Remove(link)
		if prev := u.Links[n]; prev != "" {
			if err := os.Symlink(prev, link); err != nil {
				return err
			}
		}
	}
	if !u.DirExisted {
		if err := os.RemoveAll(filepath.Join(e.nodeRoot(), u.Version)); err != nil {
			return err
		}
		_ = os.Remove(e.nodeRoot()) // empty after a first install
	}
	if u.Prev != nil {
		c := *u.Prev
		e.st.Prereqs = initMap(e.st.Prereqs)
		e.st.Prereqs["node"] = &c
	} else {
		delete(e.st.Prereqs, "node")
	}
	return nil
}

// installNode downloads Node ver, checks it, unpacks it into its version folder, runs check on it
// and writes the folder's marker through the generation under u.Op; it returns the tarball's
// sha256. A folder that fails check is removed before anything is recorded.
func (e *env) installNode(ctx context.Context, u *nodeUndo, check func() error) (string, error) {
	ver := u.Version
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
	fail := func(err error) (string, error) {
		_ = os.RemoveAll(dest)
		_ = os.Remove(e.nodeRoot())
		return "", err
	}
	if err := check(); err != nil {
		return fail(err)
	}
	if e.gen == nil {
		return fail(errors.New("setup: a dry run writes nothing"))
	}
	b, err := json.Marshal(u)
	if err != nil {
		return fail(err)
	}
	if err := fsx.WriteAtomic(filepath.Join(e.gen.Dir, nodeUndoFile), b, 0o600); err != nil {
		return fail(err)
	}
	if err := e.write(u.Op, filepath.Join(dest, nodeMarker), []byte(sum), 0o644); err != nil {
		_ = os.Remove(filepath.Join(e.gen.Dir, nodeUndoFile))
		return fail(err)
	}
	return sum, nil
}

// failNode undoes a run's node changes after a later step failed.
func (e *env) failNode(u *nodeUndo, err error) error {
	if uerr := e.undoNode(u); uerr != nil {
		return fmt.Errorf("%w (and putting node back failed: %v)", err, uerr)
	}
	if e.gen != nil {
		_ = os.Remove(filepath.Join(e.gen.Dir, nodeUndoFile))
	}
	return err
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
// repointed, anything else left alone and returned by name. When one fails, the ones already
// changed are put back, so the links never point at two versions.
func (e *env) linkNode(ver string) (left []string, err error) {
	if err := os.MkdirAll(e.localBin(), 0o755); err != nil {
		return nil, err
	}
	prev := map[string]string{} // name -> earlier target, "" for none
	undo := func() {
		for n, t := range prev {
			link := filepath.Join(e.localBin(), n)
			_ = os.Remove(link)
			if t != "" {
				_ = os.Symlink(t, link)
			}
		}
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
			undo()
			return nil, err
		}
		before, _ := os.Readlink(link)
		if err := os.Rename(tmp, link); err != nil {
			_ = os.Remove(tmp)
			undo()
			return nil, err
		}
		prev[n] = before
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

// libatomicPkgs are the packages that carry libatomic, which nodejs.org's build needs and slim
// images lack.
var libatomicPkgs = platform.Prereq{Pkgs: map[platform.PM][]string{
	platform.APT: {"libatomic1"}, platform.DNF: {"libatomic"}, platform.Pacman: {"gcc-libs"}}}

// nodeProbe runs the node of version ver.
func (e *env) nodeProbe(ctx context.Context, ver string) error {
	r, err := e.Run(ctx, execx.Cmd{Argv: []string{filepath.Join(e.nodeRoot(), ver, "bin", "node"), "-v"}, Timeout: 30 * time.Second})
	if err != nil {
		return err
	}
	if r.Code != 0 {
		return fmt.Errorf("node %s does not start (%s)", ver, r.Text())
	}
	return nil
}

// nodeStarts runs the node just unpacked. When it fails for libatomic and the package manager may
// be used, the package is installed and node tried again; otherwise the error says what to install.
func (e *env) nodeStarts(ctx context.Context, ver string, refresh func() []execx.Cmd, canPackages bool) error {
	err := e.nodeProbe(ctx, ver)
	if err == nil || !strings.Contains(err.Error(), "libatomic") {
		return err
	}
	install, ok := platform.InstallCommand(libatomicPkgs, e.Platform.PM, e.prompting(), e.Root)
	if !ok || !canPackages {
		return fmt.Errorf("%w; install libatomic1 (or libatomic, gcc-libs) with your package manager, then run makeit setup again", err)
	}
	if ierr := e.runAll(ctx, append(refresh(), install)); ierr != nil {
		return fmt.Errorf("%w; installing libatomic failed: %v", err, ierr)
	}
	return e.nodeProbe(ctx, ver)
}

// nodeRoute installs the newest Node LTS from nodejs.org, before the package route. When that
// fails, an old node stays (kept, with the reason) and with no node the package route runs, so
// setup is no worse off than before.
func (e *env) nodeRoute(record func(route string, argv []string, sha string), refresh func() []execx.Cmd, canPackages bool, fallback toolRoute) toolRoute {
	install := func(ctx context.Context) (string, error) {
		v, err := e.newestNode(ctx)
		if err != nil {
			return "", err
		}
		u := e.beginNode("prereq.node", v)
		sum, err := e.installNode(ctx, u, func() error { return e.nodeStarts(ctx, v, refresh, canPackages) })
		if err != nil {
			return "", err
		}
		left, err := e.linkNode(v)
		if err != nil {
			return "", e.failNode(u, err)
		}
		record("nodedist", nil, sum)
		e.st.Prereqs["node"].Version = v
		e.keepNodeLinks(left)
		pathFirst(e.localBin())
		return "v" + v, nil
	}
	return toolRoute{name: "nodedist", detail: "the newest Node LTS from nodejs.org, into ~/.local/share/makeit/node", run: func(ctx context.Context) (string, error) {
		out, err := install(ctx)
		if err == nil {
			return out, nil
		}
		if have := e.nodeVersion(); e.Present("node") {
			e.keep("node", fmt.Sprintf("node %s kept: the newest Node LTS could not be installed (%v)", cmp.Or(have, "on PATH"), err))
			return "", plan.ErrSkip
		}
		if fallback.run == nil {
			return "", err
		}
		return fallback.run(ctx)
	}}
}

// nodeUpgradeSteps move the node makeit installed from nodejs.org to the newest LTS and repoint
// the links; the folder of the version it leaves stays until the next update.
func (e *env) nodeUpgradeSteps() []plan.Step {
	e.dropBrokenNodeRecord()
	rec := e.st.Prereqs["node"]
	if rec == nil || rec.InstalledBy != "makeit" || rec.Route != "nodedist" || !e.nodeDistHere() {
		return nil
	}
	if _, err := os.Stat(filepath.Join(e.localBin(), "node")); err != nil {
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
			u := e.beginNode("upgrade.node", v)
			sum, err := e.installNode(ctx, u, func() error { return e.nodeProbe(ctx, v) })
			if err != nil {
				e.keep("node", fmt.Sprintf("kept at %s: node %s could not be installed (%v)", cmp.Or(rec.Version, "its version"), v, err))
				return "", plan.ErrSkip
			}
			left, err := e.linkNode(v)
			if err != nil {
				return "", e.failNode(u, err)
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

// nodeRollbackSteps undo the node changes g made, from the node.json it kept: the folder (unless
// it was there before), the links, the earlier record. The marker file itself is restored by the
// file steps before this one.
func (e *env) nodeRollbackSteps(g *state.Generation) []plan.Step {
	b, err := os.ReadFile(filepath.Join(g.Dir, nodeUndoFile))
	if err != nil {
		return nil
	}
	var u nodeUndo
	if json.Unmarshal(b, &u) != nil || !nodeVersionName.MatchString(u.Version) {
		return nil
	}
	return []plan.Step{{ID: "node.rollback", Title: "Put node back",
		Run: func(context.Context) (string, error) {
			if err := e.undoNode(&u); err != nil {
				return "", err
			}
			if u.Prev != nil && u.Prev.Version == u.Version {
				return "", nil
			}
			if e.st.ToolSkips == nil {
				e.st.ToolSkips = map[string][]string{}
			}
			if !slices.Contains(e.st.ToolSkips["node"], "v"+u.Version) {
				e.st.ToolSkips["node"] = append(e.st.ToolSkips["node"], "v"+u.Version)
			}
			return "", nil
		}}}
}

// nodeVersionName is the shape of a version folder's name, checked before one is removed.
var nodeVersionName = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

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
