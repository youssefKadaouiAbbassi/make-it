package initrepo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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

// Options are init's flags.
type Options struct{ Update, Rulesets bool }

// Report is what an init run did, or would do.
type Report struct {
	Plan    plan.Report
	Kept    []setup.Kept
	Manual  []string
	Notices []string
	Docs    []string
}

// ErrNotRepo refuses a directory with files but no git: every write init makes must be
// reviewable with git diff.
var ErrNotRepo = errors.New("not a git repository: run git init first, or run makeit init in an empty directory")

// RecordName is the committed record init keeps in the repository.
const RecordName = ".makeit.json"

// run is one Run's working state. next is the record as the steps leave it.
type run struct {
	Env
	opts    Options
	p       Project
	prev    *state.Repo
	next    map[string]*state.RepoFile
	kept    []setup.Kept
	pending map[string][]byte
	docs    []string
}

// Run plans init and, as mode allows, applies it. confirm is asked once, with the plan, in
// Prompt mode. A plan with nothing to do applies nothing.
func Run(ctx context.Context, e Env, mode plan.Mode, confirm func([]plan.Outcome) (bool, error), o Options) (Report, error) {
	if !e.Present("git") {
		return Report{}, errors.New("makeit init needs git: install it (makeit setup does), then run init again")
	}
	gitInit, err := needsGitInit(e.Dir)
	if err != nil {
		return Report{}, err
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
	prev, _, err := state.LoadRepo(filepath.Join(e.Dir, RecordName))
	if err != nil {
		return Report{}, err
	}
	r := &run{Env: e, opts: o, p: p, prev: prev}
	steps, seedRep, err := r.steps(gitInit)
	if err != nil {
		return Report{}, err
	}
	r.reset()
	rep := Report{Plan: plan.Execute(ctx, steps, plan.Options{DryRun: true}), Manual: seedRep.Manual, Notices: seedRep.Notices}
	rep.Kept = r.kept
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
	return rep, nil
}

func (r *run) reset() {
	r.next, r.kept, r.pending, r.docs = map[string]*state.RepoFile{}, nil, map[string][]byte{}, nil
}

func (r *run) keep(item, reason string) {
	r.kept = append(r.kept, setup.Kept{Item: item, Reason: reason})
}

func (r *run) path(rel string) string { return filepath.Join(r.Dir, filepath.FromSlash(rel)) }

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
	if err != nil {
		return nil, SeedReport{}, err
	}
	for _, m := range merges {
		steps = append(steps, r.mergeStep(m))
	}
	for _, s := range seeds {
		steps = append(steps, r.seedStep(s))
	}
	steps = append(steps, r.docsStep(), r.recordStep())
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
			case prev.PreExisted || !r.opts.Update:
				r.next[s.Path] = prev
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
	return plan.Step{ID: "merge:" + m.Path, Title: "Add the stack's entries to " + m.Path,
		Already: func(context.Context) (bool, error) {
			if prev != nil && !r.opts.Update {
				r.next[m.Path] = prev
				return true, nil
			}
			cur, exists, err := fsx.ReadOptional(r.path(m.Path))
			if err != nil {
				return false, err
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
				r.keep(m.Path+" "+c, "yours: kept, differs from the stack's")
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
			return "", writeFile(r.path(m.Path), r.pending[m.Path])
		}}
}

// docsStep indexes the dependencies' docs. It runs Detect again, after the merges, so the record
// holds what this sync read; it is already done while no manifest changed since the last sync.
func (r *run) docsStep() plan.Step {
	return plan.Step{ID: "docs", Title: fmt.Sprintf("Index the docs of %d libraries (docs-mcp-server)", len(r.p.Deps)),
		Already: func(context.Context) (bool, error) {
			if len(r.p.Deps) == 0 {
				return true, nil
			}
			now, err := ManifestHashes(r.Dir)
			if err != nil {
				return false, err
			}
			rec := docs.LoadRecord(r.Stack, r.Dir)
			return len(rec.Manifests) > 0 && reflect.DeepEqual(rec.Manifests, now), nil
		},
		Run: func(ctx context.Context) (string, error) {
			p, err := Detect(r.Dir)
			if err != nil {
				return "", err
			}
			rep := docs.Sync(ctx, Libs(p), r.Docs)
			r.docs = rep.Lines()
			if err := docs.SaveRecord(r.Stack, r.Dir, docs.Record{Dir: r.Dir, Manifests: p.Manifests, SyncedAt: r.Now().UTC()}); err != nil {
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
		return &next
	}
	path := r.path(RecordName)
	return plan.Step{ID: "record", Title: "Record what init wrote in " + RecordName,
		Already: func(context.Context) (bool, error) {
			_, exists, err := fsx.ReadOptional(path)
			if err != nil {
				return false, err
			}
			next := build()
			return exists && reflect.DeepEqual(next.Files, r.prev.Files) && slicesEqual(next.Answers.Types, r.prev.Answers.Types), nil
		},
		Run: func(context.Context) (string, error) {
			return "", state.SaveRepo(path, build())
		}}
}

// Libs are a project's dependencies as docs libraries.
func Libs(p Project) []docs.Lib {
	out := make([]docs.Lib, 0, len(p.Deps))
	for _, d := range p.Deps {
		out = append(out, docs.Lib{Name: d.Name, Version: d.Version, Ecosystem: d.Ecosystem})
	}
	return out
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func writeFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return fsx.WriteAtomic(path, b, 0o644)
}

// needsGitInit is true for a missing or empty directory outside any git work tree; a non-empty
// one outside git is refused.
func needsGitInit(dir string) (bool, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false, err
	}
	for d := abs; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return false, nil
		}
		if filepath.Dir(d) == d {
			break
		}
	}
	entries, err := os.ReadDir(abs)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if len(entries) > 0 {
		return false, ErrNotRepo
	}
	return true, nil
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
