package initrepo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"makeit/internal/catalog"
	"makeit/internal/fsx"
	"makeit/internal/latest"
	"makeit/internal/plan"
	"makeit/internal/state"
)

// ChangesPath is the list of init changes per version in the plugin set.
const ChangesPath = "templates/project/init-changes.json"

// SkillPath is the init skill, whose steps the list also follows.
const SkillPath = "plugins/makeit/skills/init/SKILL.md"

// Change is one change to how init sets a project up: the end state /makeit:init brings an older
// project to, so applying it twice is harmless.
type Change struct {
	Version string   `json:"version"`
	ID      string   `json:"id"`
	Files   []string `json:"files"`
	Prompt  string   `json:"prompt"`
}

// LoadChanges reads the list: the hash of the templates and the skill it was written against,
// and the changes in version order.
func LoadChanges(src catalog.Source) (sources string, all []Change, err error) {
	b, err := src.ReadFile(ChangesPath)
	if err != nil {
		return "", nil, fmt.Errorf("read %s: %w", ChangesPath, err)
	}
	var f struct {
		Sources string   `json:"sources"`
		Changes []Change `json:"changes"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return "", nil, fmt.Errorf("parse %s: %w", ChangesPath, err)
	}
	return f.Sources, f.Changes, nil
}

// Pending are the changes newer than the version a project was brought up to; all of them when
// it has none (set up before the list).
func Pending(all []Change, initVersion string) []Change {
	out := []Change{}
	for _, c := range all {
		if initVersion == "" || latest.Newer(initVersion, c.Version) {
			out = append(out, c)
		}
	}
	return out
}

// Newest is the version of the last change, or "" for none.
func Newest(all []Change) string {
	if len(all) == 0 {
		return ""
	}
	return all[len(all)-1].Version
}

// SourcesHash hashes the files the list follows (path, length and content, in path order); the list
// itself is left out.
func SourcesHash(files map[string][]byte) string {
	paths := make([]string, 0, len(files))
	for p := range files {
		if p != ChangesPath {
			paths = append(paths, p)
		}
	}
	slices.Sort(paths)
	h := sha256.New()
	for _, p := range paths {
		fmt.Fprintf(h, "%s\x00%d\x00", p, len(files[p]))
		h.Write(files[p])
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// pendingChanges are the changes in src newer than initVersion, and the newest change's version.
// A plugin set older than the list has none.
func pendingChanges(src catalog.Source, initVersion string) ([]Change, string, error) {
	_, all, err := LoadChanges(src)
	if errors.Is(err, fs.ErrNotExist) {
		return []Change{}, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	return Pending(all, initVersion), Newest(all), nil
}

// hasOwnDocs reports whether the docs /makeit:init fills in are already in dir, so a first init
// keeps them as they are.
func hasOwnDocs(dir string) bool {
	for _, n := range []string{"CLAUDE.md", "CONSTRAINTS.md"} {
		if _, err := os.Lstat(filepath.Join(dir, n)); err == nil {
			return true
		}
	}
	return false
}

// changesNotice says that init changes wait for /makeit:init.
func changesNotice(n int) string {
	if n == 1 {
		return "1 of makeit's init changes is newer than this project's setup: /makeit:init applies it"
	}
	return fmt.Sprintf("%d of makeit's init changes are newer than this project's setup: /makeit:init applies them", n)
}

// applied is `makeit init --applied <version>`: it records version, the init change the skill
// brought the project to, and adds keep to the kept differences. It writes the record only, and
// needs one: the project was set up.
func applied(ctx context.Context, e Env, mode plan.Mode, version, keep string) (Report, error) {
	path := filepath.Join(e.Dir, RecordName)
	if st, err := os.Lstat(path); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return Report{}, fmt.Errorf("%s is a symlink: makeit does not write through it", path)
	}
	if mode != plan.DryRunOnly {
		lock, err := fsx.Acquire(filepath.Join(e.Stack, "lock"), time.Now())
		if err != nil {
			return Report{}, err
		}
		defer lock.Release()
	}
	prev, info, err := state.LoadRepo(path)
	if err != nil {
		return Report{}, err
	}
	if !info.Found {
		return Report{}, fmt.Errorf("no %s in %s: run makeit init first", RecordName, e.Dir)
	}
	_, all, err := LoadChanges(e.Catalog)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Report{}, err
	}
	if !slices.ContainsFunc(all, func(c Change) bool { return c.Version == version }) {
		return Report{}, fmt.Errorf("no init change at %s: pass the initTarget makeit init --json reported", version)
	}
	next := *prev
	next.InitVersion = version
	if keep != "" && !slices.Contains(next.Keep, keep) {
		next.Keep = append(slices.Clone(next.Keep), keep)
	}
	step := plan.Step{ID: "record", Title: "Record the applied init changes in " + RecordName,
		Already: func(context.Context) (bool, error) {
			return next.InitVersion == prev.InitVersion && slices.Equal(next.Keep, prev.Keep), nil
		},
		Run: func(context.Context) (string, error) { return "", state.SaveRepo(path, &next) }}
	rep := Report{Plan: plan.Execute(ctx, []plan.Step{step}, plan.Options{DryRun: mode == plan.DryRunOnly}),
		Changes: Pending(all, next.InitVersion), Target: Newest(all), Keep: next.Keep}
	return rep, nil
}
