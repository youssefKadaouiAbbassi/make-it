package initrepo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"makeit/internal/catalog"
	"makeit/internal/docs"
	"makeit/internal/execx"
	"makeit/internal/fsx"
	"makeit/internal/plan"
	"makeit/internal/setup"
	"makeit/internal/state"
)

// Env is one init run: the project directory, makeit's stack directory, the catalog it seeds
// from and the programs it runs.
type Env struct {
	Dir, Stack string
	Catalog    catalog.Source
	Req        catalog.Requires
	// Run runs git and gh; Docs.Run runs docs-mcp-server.
	Run     execx.Runner
	Docs    docs.Options
	Present func(string) bool
	Now     func() time.Time
	// CatalogKind and CatalogResolved describe the catalog for the record.
	CatalogKind, CatalogResolved string
}

// Options are init's flags. Offline skips the docs sync and refuses Rulesets: both need the
// network.
type Options struct {
	Update, Rulesets, Offline bool
	// Applied is the init change version /makeit:init brought the project to (the report's
	// Target), recorded with Keep (one line) added to the kept differences; only the record is
	// written.
	Applied string
	Keep    string
}

// Report is what an init run did, or would do.
type Report struct {
	Plan    plan.Report
	Kept    []setup.Kept
	Manual  []string
	Notices []string
	Docs    []string
	// Changes are the init changes newer than the project's initVersion, Target the newest one
	// (what --applied records once they are applied), Keep the differences the user kept.
	Changes []Change
	Target  string
	Keep    []string
}

// noGitNotice says what init leaves out in a folder with files and no git.
const noGitNotice = "no git repository here: set up without git; the commit-hook config (.pre-commit-config.yaml) is left out. After git init, makeit init --update adds it"

// RecordName is the committed record init keeps in the repository.
const RecordName = ".makeit.json"

// run is one Run's working state. next is the record as the steps leave it.
type run struct {
	// noGit: a folder with files and no repository, set up without the git-only parts.
	noGit bool
	Env
	opts    Options
	p       Project
	prev    *state.Repo
	next    map[string]*state.RepoFile
	kept    []setup.Kept
	pending map[string][]byte
	docs    []string
	// manual is the hash of the run's manual steps ("" when there are none).
	manual string
	// bornAt is the newest init change, set on a first init, whose record starts there.
	bornAt string
	// newer are files init wrote, unchanged since, whose template has moved on (without --update).
	newer []string
}

// Run plans init and, as mode allows, applies it. confirm is asked once, with the plan, in
// Prompt mode. A plan with nothing to do applies nothing.
func Run(ctx context.Context, e Env, mode plan.Mode, confirm func([]plan.Outcome) (bool, error), o Options) (Report, error) {
	if o.Keep != "" && o.Applied == "" {
		return Report{}, errors.New("--keep needs --applied")
	}
	if o.Applied != "" {
		if o.Update || o.Rulesets {
			return Report{}, errors.New("--applied runs alone: drop --update and --rulesets")
		}
		return applied(ctx, e, mode, o.Applied, o.Keep)
	}
	if o.Offline && o.Rulesets {
		return Report{}, errors.New("--rulesets needs the network; drop --offline")
	}
	if !e.Present("git") {
		return Report{}, errors.New("makeit init needs git: install it (makeit setup does), then run init again")
	}
	gitInit, noGit, err := needsGitInit(e.Dir)
	if err != nil {
		return Report{}, err
	}
	if noGit && o.Rulesets {
		return Report{}, errors.New("--rulesets needs a git repository with a GitHub remote: run git init, add the remote, then makeit init --rulesets")
	}
	if mode == plan.Apply || mode == plan.Prompt {
		lock, err := fsx.Acquire(filepath.Join(e.Stack, "lock"), time.Now())
		if err != nil {
			return Report{}, err
		}
		defer lock.Release()
	}
	p, err := detectOrEmpty(e.Dir)
	if err != nil {
		return Report{}, err
	}
	prev, info, err := state.LoadRepo(filepath.Join(e.Dir, RecordName))
	if err != nil {
		return Report{}, err
	}
	r := &run{Env: e, opts: o, p: p, prev: prev, noGit: noGit}
	changes, newest, err := pendingChanges(e.Catalog, prev.InitVersion)
	if err != nil {
		return Report{}, err
	}
	if !info.Found && !hasOwnDocs(e.Dir) {
		// A first init writes the current templates: the project starts with every change. Docs
		// already there are kept as they are, and may be older.
		changes, r.bornAt = []Change{}, newest
	}
	steps, seedRep, err := r.steps(gitInit)
	if err != nil {
		return Report{}, err
	}
	r.reset()
	if n := len(changes); n > 0 {
		seedRep.Notices = append(seedRep.Notices, changesNotice(n))
	}
	rep := Report{Plan: plan.Execute(ctx, steps, plan.Options{DryRun: true}), Manual: seedRep.Manual, Notices: seedRep.Notices,
		Changes: changes, Target: newest, Keep: prev.Keep}
	rep.Kept = r.kept
	rep.Notices = append(rep.Notices, r.newerNotice()...)
	if nothingToDo(rep.Plan) || rep.Plan.Failed() {
		return rep, nil
	}
	switch mode {
	case plan.DryRunOnly:
		return rep, nil
	case plan.NeedConsent:
		return rep, setup.ErrConsent
	case plan.Prompt:
		yes, err := confirm(rep.Plan.Outcomes)
		if err != nil {
			return rep, err
		}
		if !yes {
			return rep, setup.ErrDeclined
		}
	}
	r.reset()
	rep.Plan = plan.Execute(ctx, steps, plan.Options{})
	rep.Kept, rep.Docs = r.kept, r.docs
	rep.Notices = append(seedRep.Notices, r.newerNotice()...)
	return rep, nil
}

func (r *run) reset() {
	r.next, r.kept, r.pending, r.docs, r.newer = map[string]*state.RepoFile{}, nil, map[string][]byte{}, nil, nil
}

// newerNotice says which unchanged files init wrote have a newer version, so a plain run does not
// keep them old in silence.
func (r *run) newerNotice() []string {
	switch len(r.newer) {
	case 0:
		return nil
	case 1:
		return []string{"1 file makeit wrote has a newer version in the plugin set (" + r.newer[0] + "): makeit init --update rewrites it"}
	}
	return []string{fmt.Sprintf("%d files makeit wrote have a newer version in the plugin set (%s): makeit init --update rewrites them", len(r.newer), strings.Join(r.newer, ", "))}
}

func (r *run) keep(item, reason string) {
	r.kept = append(r.kept, setup.Kept{Item: item, Reason: reason})
}

func (r *run) path(rel string) string { return filepath.Join(r.Dir, filepath.FromSlash(rel)) }

// linked is true, with a kept line, when rel is a symlink or sits under a directory that resolves
// outside the project: reading or writing it would follow the link out of the repository.
func (r *run) linked(rel string) bool {
	p := r.path(rel)
	if st, err := os.Lstat(p); err == nil && st.Mode()&os.ModeSymlink != 0 {
		r.keep(rel, "a symlink: not followed")
		return true
	}
	root, parent := resolved(r.Dir), resolved(filepath.Dir(p))
	if parent != root && !strings.HasPrefix(parent, root+string(filepath.Separator)) {
		r.keep(rel, "a symlink: not followed")
		return true
	}
	return false
}

// resolved is path's absolute form with its nearest existing ancestor's symlinks resolved; what
// does not exist yet cannot be a link. A dangling link gives "", which is inside nothing.
func resolved(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	rest := ""
	for d := abs; ; d = filepath.Dir(d) {
		if _, err := os.Lstat(d); err == nil {
			real, err := filepath.EvalSymlinks(d)
			if err != nil {
				return ""
			}
			return filepath.Join(real, rest)
		}
		if filepath.Dir(d) == d {
			return abs
		}
		rest = filepath.Join(filepath.Base(d), rest)
	}
}

// steps are, in order: git init when needed, the docs files, the merges, the other seeds, the
// docs sync and the record.
func (r *run) steps(gitInit bool) ([]plan.Step, SeedReport, error) {
	var steps []plan.Step
	if gitInit {
		steps = append(steps, plan.Step{ID: "git", Title: "Start a git repository in " + r.Dir, Command: []string{"git", "init", "-q"},
			Run: func(ctx context.Context) (string, error) {
				if err := os.MkdirAll(r.Dir, 0o755); err != nil {
					return "", err
				}
				res, err := r.Run(ctx, execx.Cmd{Argv: []string{"git", "init", "-q"}, Dir: r.Dir})
				if err == nil && res.Code != 0 {
					err = fmt.Errorf("git init: %s", res.Text())
				}
				return "", err
			}})
	}
	abs, err := filepath.Abs(r.Dir)
	if err != nil {
		return nil, SeedReport{}, err
	}
	v := Values{ProjectName: filepath.Base(abs), Runner: r.p.Runner, PM: r.p.PM, Date: r.Now().Format("2006-01-02")}
	docsFiles, err := DocFiles(r.p, r.Catalog, v)
	if err != nil {
		return nil, SeedReport{}, err
	}
	for _, s := range docsFiles {
		steps = append(steps, r.seedStep(s))
	}
	ours := func(path string) bool { f := r.prev.Files[path]; return f != nil && !f.PreExisted }
	seeds, merges, rep, err := Seeds(r.p, r.Catalog, r.Req, ours)
	var drops []plan.Step
	if r.noGit && len(r.p.Repos) > 0 {
		// A folder of repositories: the files that act inside a repository are left out, and the
		// ones an older run wrote here go when nobody changed them.
		seeds = slices.DeleteFunc(seeds, func(s Seed) bool { return workspaceLeavesOut(s.Path) })
		merges = slices.DeleteFunc(merges, func(m MergeSeed) bool { return workspaceLeavesOut(m.Path) })
		rep.Notices = append(slices.DeleteFunc(rep.Notices, mentionsGitOnly), workspaceNotice(r.p))
		rep.Manual = slices.DeleteFunc(rep.Manual, mentionsGitOnly)
		var notes []string
		drops, notes = r.workspaceDrops()
		rep.Notices = append(rep.Notices, notes...)
	} else if r.noGit {
		// Commit hooks only work inside a repository: their config and its lines are left out.
		seeds = slices.DeleteFunc(seeds, func(s Seed) bool { return s.Path == ".pre-commit-config.yaml" })
		gitOnly := func(l string) bool {
			return strings.Contains(l, ".pre-commit-config") || strings.Contains(l, "prek install")
		}
		rep.Notices = append(slices.DeleteFunc(rep.Notices, gitOnly), noGitNotice)
		rep.Manual = slices.DeleteFunc(rep.Manual, gitOnly)
	}
	if err != nil {
		return nil, SeedReport{}, err
	}
	if len(rep.Manual) > 0 {
		r.manual = fsx.Sha256([]byte(strings.Join(rep.Manual, "\n")))
	}
	for _, m := range merges {
		steps = append(steps, r.mergeStep(m))
	}
	for _, s := range seeds {
		steps = append(steps, r.seedStep(s))
	}
	steps = append(steps, drops...)
	if r.opts.Offline {
		rep.Notices = append(rep.Notices, "--offline: docs not synced")
	} else {
		steps = append(steps, r.docsStep())
	}
	steps = append(steps, r.recordStep())
	if r.opts.Rulesets {
		switch {
		case r.p.GitHub.Owner == "":
			return nil, SeedReport{}, errNoRemote
		case !r.Present("gh"):
			rep.Notices = append(rep.Notices, "--rulesets needs gh (the GitHub CLI), logged in with admin rights on the repository: install it, run gh auth login, then makeit init --rulesets again")
		default:
			rs, err := r.rulesetSteps(context.Background())
			if err != nil {
				return nil, SeedReport{}, err
			}
			steps = append(steps, rs...)
		}
	}
	return steps, rep, nil
}

// seedStep writes a whole file. A file that was there before init is the user's and is kept; a
// file init wrote is updated with --update only while the user has not changed it.
func (r *run) seedStep(s Seed) plan.Step {
	prev := r.prev.Files[s.Path]
	title := "Write " + s.Path
	if prev != nil && r.opts.Update {
		title = "Update " + s.Path
	}
	written := &state.RepoFile{Class: s.Class, Template: s.Template, WrittenSha256: fsx.Sha256(s.Body)}
	return plan.Step{ID: s.Class + ":" + s.Path, Title: title,
		Already: func(context.Context) (bool, error) {
			if r.linked(s.Path) {
				if prev != nil {
					r.next[s.Path] = prev
				}
				return true, nil
			}
			cur, exists, err := fsx.ReadOptional(r.path(s.Path))
			if err != nil {
				return false, err
			}
			switch {
			case prev == nil && !exists:
				r.next[s.Path] = written
				return false, nil
			case prev == nil:
				r.next[s.Path] = &state.RepoFile{Class: s.Class, Template: s.Template, PreExisted: true}
				r.keep(s.Path, "yours: kept as it is")
				return true, nil
			case prev.PreExisted:
				r.next[s.Path] = prev
				return true, nil
			case !r.opts.Update:
				r.next[s.Path] = prev
				if exists && fsx.Sha256(cur) == prev.WrittenSha256 && !bytes.Equal(cur, s.Body) {
					r.newer = append(r.newer, s.Path)
				}
				return true, nil
			case !exists:
				r.next[s.Path] = prev
				r.keep(s.Path, "removed by you: not written again")
				return true, nil
			case fsx.Sha256(cur) != prev.WrittenSha256:
				r.next[s.Path] = prev
				r.keep(s.Path, "changed by you: kept, not updated")
				return true, nil
			case bytes.Equal(cur, s.Body):
				r.next[s.Path] = prev
				return true, nil
			}
			r.next[s.Path] = written
			return false, nil
		},
		Run: func(context.Context) (string, error) {
			return "", writeFile(r.path(s.Path), s.Body)
		}}
}

// mergeStep adds what the stack needs to a file without removing or changing what is there.
func (r *run) mergeStep(m MergeSeed) plan.Step {
	prev := r.prev.Files[m.Path]
	return plan.Step{ID: "merge:" + m.Path, Title: "Add makeit's entries to " + m.Path,
		Already: func(context.Context) (bool, error) {
			if r.linked(m.Path) {
				if prev != nil {
					r.next[m.Path] = prev
				}
				return true, nil
			}
			if prev != nil && !r.opts.Update {
				r.next[m.Path] = prev
				return true, nil
			}
			cur, exists, err := fsx.ReadOptional(r.path(m.Path))
			if err != nil {
				return false, err
			}
			if prev != nil && !prev.PreExisted && !exists {
				r.next[m.Path] = prev
				r.keep(m.Path, "removed by you: not written again")
				return true, nil
			}
			// Entries an earlier run added that are gone now were removed by the user.
			var skip map[string]bool
			if prev != nil {
				skip = map[string]bool{}
				for _, a := range prev.Added {
					skip[a] = true
				}
			}
			res, err := m.Apply(cur, skip)
			if err != nil {
				r.keep(m.Path, "not edited: "+err.Error())
				if prev != nil {
					r.next[m.Path] = prev
				}
				return true, nil
			}
			for _, c := range res.Conflicts {
				r.keep(m.Path+" "+c, "yours: kept, differs from makeit's")
			}
			rec := &state.RepoFile{Class: "merge", Template: m.Template, WrittenSha256: fsx.Sha256(res.Body), PreExisted: exists}
			if prev != nil {
				rec.PreExisted, rec.Added = prev.PreExisted, prev.Added
			}
			rec.Added = append(append([]string{}, rec.Added...), res.Added...)
			if len(rec.Added) == 0 {
				rec.Added = nil
			}
			r.next[m.Path] = rec
			if !res.Changed {
				return true, nil
			}
			r.pending[m.Path] = res.Body
			return false, nil
		},
		Run: func(context.Context) (string, error) {
			b, ok := r.pending[m.Path]
			if !ok {
				// Without a merged body the write would empty the user's file.
				return "", fmt.Errorf("%s: nothing merged, not written", m.Path)
			}
			return "", writeFile(r.path(m.Path), b)
		}}
}

// docsStep indexes the dependencies' docs. It runs Detect again, after the merges, so the record
// holds what this sync read; it is already done while no manifest changed since the last sync.
func (r *run) docsStep() plan.Step {
	return plan.Step{ID: "docs", Title: fmt.Sprintf("Index the docs of %d libraries (docs-mcp-server)", len(Libs(r.p))),
		Already: func(context.Context) (bool, error) {
			if len(Libs(r.p)) == 0 {
				return true, nil
			}
			now, err := ManifestHashes(r.Dir)
			if err != nil {
				return false, err
			}
			rec := docs.LoadRecord(r.Stack, r.Dir)
			return len(rec.Manifests) > 0 && !rec.Failed && reflect.DeepEqual(rec.Manifests, now), nil
		},
		Run: func(ctx context.Context) (string, error) {
			// A sync the docs hook started may be running: two would write one index at once.
			lock, err := fsx.Acquire(docs.LockPath(r.Stack), r.Now())
			if err != nil {
				return "docs not synced: " + err.Error(), plan.ErrSkip
			}
			defer lock.Release()
			p, err := Detect(r.Dir)
			if err != nil {
				return "", err
			}
			rep := docs.Sync(ctx, Libs(p), r.Docs)
			r.docs = rep.Lines()
			if err := docs.SaveRecord(r.Stack, r.Dir, docs.Record{Dir: r.Dir, Manifests: p.Manifests, SyncedAt: r.Now().UTC(), Failed: rep.Failed()}); err != nil {
				return "", err
			}
			return strings.Join(r.docs, "\n"), nil
		}}
}

// recordStep writes .makeit.json when what the steps leave differs from the record on disk.
// SaveRepo stamps the CLI version.
func (r *run) recordStep() plan.Step {
	build := func() *state.Repo {
		next := *r.prev
		next.SchemaVersion = state.SchemaVersion
		next.Answers.Types = r.p.Types
		next.Files = r.next
		next.Catalog = state.RepoCatalog{Kind: r.CatalogKind, Resolved: r.CatalogResolved}
		next.ManualSha256 = r.manual
		if r.bornAt != "" {
			next.InitVersion = r.bornAt
		}
		return &next
	}
	path := r.path(RecordName)
	return plan.Step{ID: "record", Title: "Record what init wrote in " + RecordName,
		Already: func(context.Context) (bool, error) {
			if r.linked(RecordName) {
				return true, nil
			}
			_, exists, err := fsx.ReadOptional(path)
			if err != nil {
				return false, err
			}
			next := build()
			return exists && reflect.DeepEqual(next.Files, r.prev.Files) && slices.Equal(next.Answers.Types, r.prev.Answers.Types) &&
				next.ManualSha256 == r.prev.ManualSha256, nil
		},
		Run: func(context.Context) (string, error) {
			return "", state.SaveRepo(path, build())
		}}
}

// Libs are a project's dependencies as docs libraries. The go directive is left out: it names the
// standard library, whose pkg.go.dev page at depth 0 is only the package list.
func Libs(p Project) []docs.Lib {
	out := make([]docs.Lib, 0, len(p.Deps))
	for _, d := range p.Deps {
		if d.Ecosystem == "go" && d.Name == "go" {
			continue
		}
		out = append(out, docs.Lib{Name: d.Name, Version: d.Version, Ecosystem: d.Ecosystem})
	}
	return out
}

func writeFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return fsx.WriteAtomic(path, b, 0o644)
}

// workspaceLeavesOut reports a file a folder of repositories does not get: each acts inside a
// repository (git, Renovate, the CI scan and workflows, the env template, the commit hooks).
func workspaceLeavesOut(path string) bool {
	switch path {
	case ".gitignore", "renovate.json", ".grype.yaml", ".env.example", ".pre-commit-config.yaml":
		return true
	}
	return strings.HasPrefix(path, ".github/workflows/") || strings.HasPrefix(path, "ci/")
}

// mentionsGitOnly reports a notice or manual line about one of the files a workspace leaves out.
func mentionsGitOnly(l string) bool {
	for _, w := range []string{".gitignore", "renovate", ".grype", ".env.example", ".pre-commit-config", "prek install", "floor-guard", "gate.yml", "npm ci", "CI caller"} {
		if strings.Contains(l, w) {
			return true
		}
	}
	return false
}

// workspaceNotice names the repositories found, what was left out and why.
func workspaceNotice(p Project) string {
	n := "a folder of repositories (" + strings.Join(p.Repos, ", ") + "): set up for the folder, the repositories untouched; .gitignore, renovate.json, .grype.yaml, .env.example, the commit hooks and the CI workflow are left out because they act inside a repository: run makeit init in a repository to set it up there"
	switch p.Worktrees {
	case 0:
	case 1:
		n += "; 1 worktree skipped"
	default:
		n += fmt.Sprintf("; %d worktrees skipped", p.Worktrees)
	}
	return n
}

// workspaceDrops are the removals of the files an older run wrote here that a workspace leaves
// out, when nobody changed them, and the notices for those it keeps.
func (r *run) workspaceDrops() ([]plan.Step, []string) {
	var steps []plan.Step
	var notes []string
	for _, path := range slices.Sorted(maps.Keys(r.prev.Files)) {
		f := r.prev.Files[path]
		if !workspaceLeavesOut(path) {
			continue
		}
		if f.PreExisted {
			if len(f.Added) > 0 {
				notes = append(notes, "makeit's lines in "+path+" stay (the file was there before): remove them by hand if you want")
			}
			continue
		}
		full := r.path(path)
		steps = append(steps, plan.Step{ID: "drop:" + path, Title: "Remove " + path + " (a folder of repositories does not use it)",
			Already: func(context.Context) (bool, error) {
				// A link is never followed, and only --update removes: otherwise the entry stays
				// recorded, so a later --update still finds it.
				if r.linked(path) || !r.opts.Update {
					r.next[path] = f
					return true, nil
				}
				cur, found, err := fsx.ReadOptional(full)
				if err != nil || !found {
					return true, err
				}
				if fsx.Sha256(cur) != f.WrittenSha256 {
					r.keep(path, "changed since makeit wrote it; a folder of repositories does not use it, so it stays yours")
					return true, nil
				}
				return false, nil
			},
			Run: func(context.Context) (string, error) {
				if err := os.Remove(full); err != nil {
					return "", err
				}
				// The folders it emptied go too (.github/workflows), up to the project, never one
				// that still holds something.
				for d := filepath.Dir(full); d != filepath.Clean(r.Dir) && strings.HasPrefix(d, filepath.Clean(r.Dir)); d = filepath.Dir(d) {
					if os.Remove(d) != nil {
						break
					}
				}
				return "", nil
			}})
	}
	if len(steps) > 0 && !r.opts.Update {
		notes = append(notes, fmt.Sprintf("%d files an older makeit wrote here act only inside a repository: makeit init --update removes the ones you never changed", len(steps)))
	}
	return steps, notes
}

// needsGitInit reports whether init starts a repository (an empty or missing folder outside any
// git work tree) or works without git (a folder with files and no repository).
func needsGitInit(dir string) (gitInit, noGit bool, err error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false, false, err
	}
	if gitTop(abs) != "" {
		return false, false, nil
	}
	entries, err := os.ReadDir(abs)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, false, err
	}
	if len(entries) > 0 {
		return false, true, nil
	}
	return true, false, nil
}

// gitTop is the nearest directory from abs upward holding a .git (a directory, or a file in a
// worktree or submodule), or "". It reads the file system only, so a dry run runs no git.
func gitTop(abs string) string {
	for d := abs; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		if filepath.Dir(d) == d {
			return ""
		}
	}
}

// detectOrEmpty is Detect, with a directory that does not exist yet giving an empty project.
func detectOrEmpty(dir string) (Project, error) {
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return Project{Dir: dir, Manifests: map[string]string{}}, nil
	}
	return Detect(dir)
}

func nothingToDo(r plan.Report) bool {
	for _, o := range r.Outcomes {
		if o.Status != plan.Already {
			return false
		}
	}
	return true
}
