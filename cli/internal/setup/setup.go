// Package setup takes a machine to the full stack: prerequisites, Claude Code, the catalog, the
// marketplaces, settings, the bundle and the global files (cli/SETUP.md). What it finds is
// adopted as the user's and left unchanged; every file it writes is pre-imaged first.
package setup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"makeit/internal/catalog"
	"makeit/internal/claudecli"
	"makeit/internal/execx"
	"makeit/internal/fsx"
	"makeit/internal/paths"
	"makeit/internal/platform"
	"makeit/internal/pluginrule"
	"makeit/internal/state"
)

// Env is the machine setup runs on. The caller fills it from the real machine; tests fake it.
type Env struct {
	Roots    paths.Roots
	Platform platform.Platform
	// Present reports whether a program is on PATH.
	Present func(string) bool
	Run     execx.Runner
	Catalog catalog.Source
	// Locate finds the claude binaries on the machine (claudecli.Locate in production).
	Locate func(ctx context.Context) []claudecli.Install
	// Interactive is true when a person can answer sudo's password prompt; Yes is --yes, which
	// makes sudo run with -n. Root skips sudo.
	Interactive, Yes, Root bool
	// Getenv reads the environment (os.Getenv in production); nil is an empty environment.
	Getenv func(string) string
}

// env is one run: the machine plus what earlier steps learned and the run's record.
type env struct {
	Env
	st  *state.State
	gen *state.Generation // nil in a dry run
	req catalog.Requires
	// claude is set by the Claude Code step once a usable claude is known.
	claude *claudecli.CLI
	// skipped are the optional prerequisites left for the user to install by hand.
	skipped []platform.Prereq
	// command is the run's command: setup, update or uninstall.
	command string
	// kept and hints are what the run left alone and how adopted plugins update, for the report.
	kept  []Kept
	hints []Hint
	// synced is set when the catalog step wrote a new catalog in this run.
	synced bool
	// inputs caches the plugin listing for the plugin phase; a step that changes plugins clears it.
	inputs *pluginrule.Inputs
	// stateRemoved: uninstall removed state.json, so the journal must not write it again.
	stateRemoved bool
	// purge: uninstall deletes the plugins' data and the makeit directory once the run is over.
	purge bool
	// keepSettings is uninstall's --keep-settings; catalogLeft: removing the catalog failed.
	keepSettings, catalogLeft bool
}

// removedByYou is the reason update gives for not putting back what the user removed.
const removedByYou = "removed by you; makeit setup puts it back"

// keep reports item as left alone, once per item.
func (e *env) keep(item, reason string) {
	for _, k := range e.kept {
		if k.Item == item {
			return
		}
	}
	e.kept = append(e.kept, Kept{Item: item, Reason: reason})
}

// errSymlink: a path is a symlink, so whoever linked it owns it and makeit does not remove it.
var errSymlink = errors.New("is a symlink, so it is yours")

// remove deletes path after keeping its pre-image in the run's generation, and records the
// removal there. A symlink is refused with errSymlink.
func (e *env) remove(id, path string) error {
	if e.gen == nil {
		return errors.New("setup: a dry run removes nothing")
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%s %w", path, errSymlink)
	}
	old, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	op := state.Op{ID: id, Kind: "remove", Path: e.Roots.Portable(path)}
	if op.PreImage, err = e.gen.PreImage(old); err != nil {
		return err
	}
	pre := fsx.Sha256(old)
	op.PreSha256 = &pre
	if err := os.Remove(path); err != nil {
		return err
	}
	return e.gen.Record(op)
}

// prompting is true when sudo may ask for a password on the terminal.
func (e *env) prompting() bool { return e.Interactive && !e.Yes }

// sudo wraps argv for root: as is when setup runs as root, with -n when no one can type a password.
func (e *env) sudo(argv ...string) execx.Cmd {
	if e.Root {
		return execx.Cmd{Argv: argv}
	}
	pre := []string{"sudo"}
	if !e.prompting() {
		pre = append(pre, "-n")
	}
	return execx.Cmd{Argv: append(pre, argv...), Inherit: e.prompting()}
}

// write replaces path with data after keeping its pre-image in the run's generation, and
// records the change there, so rollback can restore it.
func (e *env) write(id, path string, data []byte, perm fs.FileMode) error {
	if e.gen == nil {
		return errors.New("setup: a dry run writes nothing")
	}
	dst, perm, err := landing(path, perm)
	if err != nil {
		return err
	}
	old, found, err := fsx.ReadOptional(dst)
	if err != nil {
		return err
	}
	op := state.Op{ID: id, Kind: "write", Path: e.Roots.Portable(path)}
	if found {
		if op.PreImage, err = e.gen.PreImage(old); err != nil {
			return err
		}
		pre := fsx.Sha256(old)
		op.PreSha256 = &pre
	}
	created := e.missingDirs(filepath.Dir(dst))
	if err := fsx.WriteAtomic(dst, data, perm); err != nil {
		return err
	}
	for _, d := range created {
		e.st.Dirs = initMap(e.st.Dirs)
		e.st.Dirs[e.Roots.Portable(d)] = &state.Dir{CreatedByUs: true}
	}
	post := fsx.Sha256(data)
	op.PostSha256 = &post
	return e.gen.Record(op)
}

// missingDirs are the directories from dir up that do not exist yet and lie strictly inside the
// Claude directory: the ones a write there creates, which uninstall may remove when empty.
func (e *env) missingDirs(dir string) []string {
	var out []string
	for {
		rel, err := filepath.Rel(e.Roots.Claude, dir)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return out
		}
		if _, err := os.Lstat(dir); !errors.Is(err, fs.ErrNotExist) {
			return out
		}
		out = append(out, dir)
		dir = filepath.Dir(dir)
	}
}

// landing is where a write to path lands, and with which mode. A symlink (stow and home-manager
// link these files) is written through, so the link survives; a symlink to a missing file is
// refused. An existing file keeps its mode; perm applies to a new one, set exactly by
// fsx.WriteAtomic's chmod (umask does not apply), so callers pass owner-only modes.
func landing(path string, perm fs.FileMode) (string, fs.FileMode, error) {
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return path, perm, nil
	}
	if err != nil {
		return "", 0, err
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		target, err := filepath.EvalSymlinks(path)
		if err != nil {
			return "", 0, fmt.Errorf("%s is a symlink to a missing file; fix the link first: %w", path, err)
		}
		if fi, err = os.Stat(target); err != nil {
			return "", 0, err
		}
		path = target
	}
	return path, fi.Mode().Perm(), nil
}
