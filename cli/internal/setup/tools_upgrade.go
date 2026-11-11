package setup

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"

	"makeit/internal/fsx"
	"makeit/internal/latest"
	"makeit/internal/plan"
	"makeit/internal/platform"
	"makeit/internal/state"
	"makeit/internal/tools"
)

// release is one tool's newest release, or why it could not be read.
type release struct {
	version string
	err     error
}

var errOffline = errors.New("offline: no release looked up")

// newestRelease is the newest release of tool id, the lookups for every release tool done once
// per run and at the same time (one git ls-remote each), passing over releases rollback undid.
func (e *env) newestRelease(ctx context.Context, id string) (string, error) {
	e.releasesOnce.Do(func() {
		e.releases = map[string]release{}
		var mu sync.Mutex
		var wg sync.WaitGroup
		for _, r := range platform.Prereqs {
			if r.Release == nil {
				continue
			}
			if e.Offline || e.Run == nil {
				e.releases[r.ID] = release{err: errOffline}
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				// Like plugins: the daily run leaves a release rollback undid, a run by hand takes it.
				var skip []string
				if e.Background {
					skip = e.st.ToolSkips[r.ID]
				}
				v, err := latest.Newest(ctx, e.Run, r.Release.Repo, skip)
				mu.Lock()
				e.releases[r.ID] = release{v, err}
				mu.Unlock()
			}()
		}
		wg.Wait()
	})
	rel, ok := e.releases[id]
	if !ok {
		return "", fmt.Errorf("%s has no release route", id)
	}
	return rel.version, rel.err
}

// toolUpgradeSteps move each release binary makeit installed to the newest release: setup,
// update and the daily run alike (no password, no new tool). A binary changed since makeit
// wrote it is the user's and stays.
func (e *env) toolUpgradeSteps() []plan.Step {
	var steps []plan.Step
	for _, r := range platform.Prereqs {
		rec := e.st.Prereqs[r.ID]
		if r.Release == nil || rec == nil || rec.InstalledBy != "makeit" || rec.Route != "release" || !r.Release.Ships(e.Platform.OS, e.Platform.Arch) {
			continue
		}
		name := r.Release.Bin
		if e.Platform.OS == "windows" {
			name += ".exe"
		}
		path := filepath.Join(e.Roots.Home, ".local", "bin", name)
		if !e.exists(path) {
			continue // removed by you: setup's own rule says so
		}
		id, rel := r.ID, *r.Release
		steps = append(steps, plan.Step{ID: "upgrade." + id, Title: "Update " + id,
			Already: func(ctx context.Context) (bool, error) {
				v, err := e.newestRelease(ctx, id)
				if err != nil {
					if !errors.Is(err, errOffline) {
						e.keep(id, fmt.Sprintf("kept at %s: could not read its releases (%v)", cmp.Or(rec.Version, "its version"), err))
					}
					return true, nil
				}
				cur, found, err := fsx.ReadOptional(path)
				if err != nil {
					return false, err
				}
				if !found {
					return true, nil
				}
				if fsx.Sha256(cur) != rec.Sha256 {
					e.keep(id, "changed since makeit wrote it")
					return true, nil
				}
				return rec.Version == v, nil
			},
			Run: func(ctx context.Context) (string, error) {
				v, err := e.newestRelease(ctx, id)
				if err != nil {
					return "", err
				}
				_, data, err := tools.FetchRelease(ctx, e.client(), userAgent(), cmp.Or(e.ReleaseBase, "https://github.com"), rel, v, e.Platform.OS, e.Platform.Arch)
				if err != nil {
					// A tag can be out minutes before its files, or a file can be renamed: the tool
					// stays where it is and the rest of the run goes on.
					e.keep(id, fmt.Sprintf("kept at %s: release v%s could not be installed (%v)", cmp.Or(rec.Version, "its version"), v, err))
					return "", plan.ErrSkip
				}
				from, sha := rec.Version, fsx.Sha256(data)
				if sha == rec.Sha256 {
					rec.Version = v // a 0.3.x record without a version: the same bytes, nothing moved
					return "", plan.ErrSkip
				}
				if err := e.write("upgrade."+id, path, data, 0o755); err != nil {
					return "", err
				}
				rec.Version, rec.Sha256 = v, sha
				e.changes = append(e.changes, state.Change{Kind: "tool", Item: id, From: from, To: v})
				return cmp.Or(from, "?") + " → " + v, nil
			}})
	}
	return append(steps, e.nodeUpgradeSteps()...)
}

// toolLatestStep records the newest release of every release tool, makeit's or not, for doctor.
// It never makes the plan non-empty: a changed record is saved the way a handed-back global file
// is (recordChanged), and a lookup that failed or ran offline records nothing.
func (e *env) toolLatestStep() plan.Step {
	return plan.Step{ID: "tools.latest", Title: "Look up the newest tool releases",
		Already: func(ctx context.Context) (bool, error) {
			for _, r := range platform.Prereqs {
				if r.Release == nil {
					continue
				}
				v, err := e.newestRelease(ctx, r.ID)
				if err != nil || v == "" || e.st.ToolLatest[r.ID] == v {
					continue
				}
				if e.st.ToolLatest == nil {
					e.st.ToolLatest = map[string]string{}
				}
				e.st.ToolLatest[r.ID] = v
				e.recordChanged = true
			}
			return true, nil
		}}
}

// toolRollbackSteps put the records of the tools g upgraded back to the earlier release once
// its file is restored, and skip the undone release until a newer one ships.
func (e *env) toolRollbackSteps(g *state.Generation) []plan.Step {
	var steps []plan.Step
	for _, c := range g.Manifest.Changes {
		if c.Kind != "tool" || c.Item == "node" { // node has its own rollback step
			continue
		}
		i := slices.IndexFunc(g.Manifest.Ops, func(op state.Op) bool { return op.ID == "upgrade."+c.Item })
		if i < 0 {
			continue
		}
		op := g.Manifest.Ops[i]
		steps = append(steps, plan.Step{ID: "tool.rollback." + c.Item, Title: fmt.Sprintf("Put %s back to %s", c.Item, cmp.Or(c.From, "its earlier release")),
			Run: func(context.Context) (string, error) {
				cur, found, err := fsx.ReadOptional(e.Roots.Abs(op.Path))
				if err != nil {
					return "", err
				}
				// Only when the restore step put the earlier binary back (nobody changed it since).
				if !found || op.PreSha256 == nil || fsx.Sha256(cur) != *op.PreSha256 {
					return "", plan.ErrSkip
				}
				if rec := e.st.Prereqs[c.Item]; rec != nil {
					rec.Version, rec.Sha256 = c.From, *op.PreSha256
				}
				if e.st.ToolSkips == nil {
					e.st.ToolSkips = map[string][]string{}
				}
				if !slices.Contains(e.st.ToolSkips[c.Item], "v"+c.To) {
					e.st.ToolSkips[c.Item] = append(e.st.ToolSkips[c.Item], "v"+c.To)
				}
				return "", nil
			}})
	}
	return steps
}
