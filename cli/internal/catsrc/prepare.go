package catsrc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"makeit/internal/catalog"
	"makeit/internal/state"
)

var (
	// ErrAdopted is --catalog on a machine where the user added the makeit marketplace.
	ErrAdopted = errors.New("the makeit marketplace on this machine is yours: makeit does not change its source. Your marketplace and your plugins stay as they are; nothing was changed")
	// ErrFresh is a git catalog on a machine that has neither git nor CA certificates yet.
	ErrFresh = errors.New("this plugin set needs git and CA certificates first")
)

// Params is what Prepare reads.
type Params struct {
	// Arg is --catalog; "" keeps the recorded source.
	Arg                  string
	AllowBranch, Offline bool
	// Command is setup or update.
	Command string
	State   *state.State
	Stack   string
	Git     Git
	// Prereq reports whether a platform prerequisite (git, ca-certificates) is present.
	Prereq func(id string) bool
	// Show prints a line before anything is fetched: the full source when it was guessed.
	Show func(string)
}

// Prepared is the catalog a run plans with: its source, the tree the catalog step writes, what
// the state records once it is written, and lines for update's notice.
type Prepared struct {
	Source  catalog.Source
	Tree    *catalog.Tree
	Record  state.Catalog
	Notices []string
}

// Prepare resolves the catalog a run uses (cli/CATALOG-GIT.md). An explicit --catalog that cannot be
// fetched or checked is an error; without one, a source that cannot be refreshed keeps the catalog
// as it is, with one warning.
func Prepare(ctx context.Context, p Params) (Prepared, error) {
	explicit := p.Arg != ""
	dir := filepath.Join(p.Stack, "catalog")
	want, err := wantRequires(dir)
	if err != nil {
		return Prepared{}, err
	}
	if m := p.State.Marketplaces[want.Marketplace]; explicit && m != nil && m.AddedBy == "user" {
		return Prepared{}, ErrAdopted
	}
	var rec *state.Catalog
	if p.State.Catalog.Kind != "" {
		rec = &p.State.Catalog
	}
	var spec Spec
	switch {
	case explicit:
		if spec, err = Parse(p.Arg, p.AllowBranch); err != nil {
			return Prepared{}, err
		}
	case rec != nil && rec.Kind == "git":
		spec = Spec{Kind: "git", Location: rec.Location, Ref: deref(rec.Requested), Commit: rec.RefKind == "commit", AllowBranch: rec.AllowBranch}
	case rec != nil && rec.Kind == "directory":
		spec = Spec{Kind: "directory", Location: rec.Location}
	default:
		spec = Spec{Kind: "embedded"}
	}
	fail := func(err error) (Prepared, error) {
		if explicit {
			return Prepared{}, err
		}
		cur, cerr := current(dir, rec)
		if cerr != nil {
			return Prepared{}, errors.Join(err, cerr)
		}
		at := "where it is"
		if rec != nil && rec.Resolved != "" {
			at = "at " + rec.Resolved[:min(12, len(rec.Resolved))]
		}
		cur.Notices = append(cur.Notices, fmt.Sprintf("could not refresh the plugin set %s: %v; it stays %s", spec.Location, err, at))
		return cur, nil
	}
	switch spec.Kind {
	case "embedded":
		t, err := catalog.EmbeddedTree()
		if err != nil {
			return Prepared{}, err
		}
		return Prepared{Source: t, Tree: t, Record: state.Catalog{Kind: "embedded", Resolved: catalog.Digest(t.Manifest), MarketplaceName: want.Marketplace}}, nil
	case "directory":
		t, err := catalog.DirTree(spec.Location)
		if err == nil {
			err = Check(t, want)
		}
		if err != nil {
			return fail(err)
		}
		return Prepared{Source: t, Tree: t, Record: state.Catalog{Kind: "directory", Location: spec.Location, Resolved: catalog.Digest(t.Manifest), MarketplaceName: want.Marketplace}}, nil
	}
	// CA certificates are for https; an ssh or file source needs only git.
	if !p.Prereq("git") || strings.HasPrefix(spec.Location, "https://") && !p.Prereq("ca-certificates") {
		if p.Command == "setup" && explicit {
			return Prepared{}, fmt.Errorf("%w: run `makeit setup --yes`, then `makeit update --catalog %s --yes`", ErrFresh, p.Arg)
		}
		return fail(fmt.Errorf("the plugin set %s needs git and CA certificates: install them", spec.Location))
	}
	if p.Offline {
		if explicit {
			return Prepared{}, errors.New("network disabled (--offline): a git source cannot be fetched")
		}
		cur, err := current(dir, rec)
		if err != nil {
			return Prepared{}, err
		}
		cur.Notices = append(cur.Notices, "--offline: the plugin set stays where it is")
		return cur, nil
	}
	unreached := func(err error) error {
		return fmt.Errorf("could not fetch %s: %v; a private repository needs a git credential helper or an SSH key", spec.Location, err)
	}
	// A guessed GitHub source could be a mistyped local path: name it before anything is fetched.
	var guessed []string
	if spec.Guessed != "" {
		guessed = []string{fmt.Sprintf("plugin set: %s (no local path %s)", spec.Location, spec.Guessed)}
		if p.Show != nil {
			p.Show(guessed[0])
		}
	}
	refs, err := LsRemote(ctx, p.Git, spec.Location)
	if err != nil {
		return fail(unreached(err))
	}
	choice, notices, err := Choose(spec, refs, rec, explicit)
	if err != nil {
		return fail(err)
	}
	notices = append(guessed, notices...)
	// A missing or changed synced catalog is fetched again from the recorded commit.
	if !explicit && rec != nil && (choice.Hold || choice.Commit == rec.Resolved) && intact(dir) {
		cur, err := current(dir, rec)
		if err != nil {
			return Prepared{}, err
		}
		cur.Notices = append(cur.Notices, notices...)
		return cur, nil
	}
	src := choice.Commit
	switch choice.RefKind {
	case "latest", "tag":
		src = "refs/tags/" + choice.Tag
	case "branch":
		src = "refs/heads/" + choice.Requested
	}
	t, err := Fetch(ctx, p.Git, spec.Location, src, choice.Commit)
	if err != nil {
		err = unreached(err)
	} else {
		err = Check(t, want)
	}
	if err != nil {
		return fail(err)
	}
	requested := choice.Requested
	return Prepared{Source: t, Tree: t, Notices: notices, Record: state.Catalog{Kind: "git", Location: spec.Location,
		Requested: &requested, Resolved: choice.Commit, RefKind: choice.RefKind, Tag: choice.Tag,
		AllowBranch: spec.AllowBranch, MarketplaceName: want.Marketplace}}, nil
}

// Check refuses a catalog that would change plugin ids or that this makeit cannot read: its schema,
// its marketplace name (in makeit.json and marketplace.json) and its bundle must match want.
func Check(t *catalog.Tree, want catalog.Requires) error {
	req, err := catalog.LoadRequires(t)
	if err != nil {
		return err
	}
	var mk struct{ Name string }
	if b, err := t.ReadFile(".claude-plugin/marketplace.json"); err != nil {
		return err
	} else if err := json.Unmarshal(b, &mk); err != nil {
		return fmt.Errorf("the plugin set's marketplace.json: %w", err)
	}
	switch {
	case req.Marketplace != want.Marketplace || mk.Name != want.Marketplace:
		return fmt.Errorf("the plugin set names its marketplace %q, not %q: plugin ids would change, so it is refused", mk.Name, want.Marketplace)
	case req.Bundle != want.Bundle:
		return fmt.Errorf("the plugin set's bundle is %s, not %s: makeit cannot switch bundles, so it is refused", req.Bundle, want.Bundle)
	}
	return nil
}

// wantRequires is what a new catalog must match: the catalog synced in dir, or on a fresh machine
// the embedded one.
func wantRequires(dir string) (catalog.Requires, error) {
	if catalog.ReadMarker(dir).Digest != "" {
		if t, err := catalog.DirTree(dir); err == nil {
			if r, err := catalog.LoadRequires(t); err == nil {
				return r, nil
			}
		}
	}
	return catalog.LoadRequires(catalog.Embedded)
}

// intact reports whether dir holds the catalog its marker says was synced.
func intact(dir string) bool {
	mk := catalog.ReadMarker(dir)
	t, err := catalog.DirTree(dir)
	return mk.Digest != "" && err == nil && catalog.Digest(t.Manifest) == mk.Digest
}

// current is the catalog as it is: the synced tree of a recorded directory or git source, or the
// embedded one. A recorded source whose synced copy is gone is an error, never the embedded catalog.
func current(dir string, rec *state.Catalog) (Prepared, error) {
	if rec != nil && rec.Kind != "embedded" {
		if !intact(dir) {
			return Prepared{}, fmt.Errorf("the synced copy of the plugin set %s is missing or changed in %s", rec.Location, dir)
		}
		t, err := catalog.DirTree(dir)
		if err != nil {
			return Prepared{}, err
		}
		return Prepared{Source: t, Tree: t, Record: *rec}, nil
	}
	t, err := catalog.EmbeddedTree()
	if err != nil {
		return Prepared{}, err
	}
	return Prepared{Source: t, Tree: t, Record: state.Catalog{Kind: "embedded", Resolved: catalog.Digest(t.Manifest)}}, nil
}
