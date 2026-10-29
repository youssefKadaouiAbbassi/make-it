// Package setup takes a machine to the full stack: prerequisites, Claude Code, the catalog, the
// marketplaces, settings, the bundle and the global files (cli/SETUP.md). What it finds is
// adopted as the user's and left unchanged; every file it writes is pre-imaged first.
package setup

import (
	"errors"
	"io/fs"

	"makeit/internal/catalog"
	"makeit/internal/claudecli"
	"makeit/internal/execx"
	"makeit/internal/fsx"
	"makeit/internal/paths"
	"makeit/internal/platform"
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
	// Interactive is true when a person can answer sudo's password prompt; Yes is --yes, which
	// makes sudo run with -n. Root skips sudo.
	Interactive, Yes, Root bool
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
	old, found, err := fsx.ReadOptional(path)
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
	if err := fsx.WriteAtomic(path, data, perm); err != nil {
		return err
	}
	post := fsx.Sha256(data)
	op.PostSha256 = &post
	return e.gen.Record(op)
}
