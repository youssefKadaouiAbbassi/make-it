package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"time"

	"makeit/internal/catalog"
	"makeit/internal/catsrc"
	"makeit/internal/cli"
	"makeit/internal/docs"
	"makeit/internal/execx"
	"makeit/internal/fsx"
	"makeit/internal/initrepo"
	"makeit/internal/paths"
)

// docsRun starts docs-mcp-server; tests stand in for it.
var docsRun execx.Runner = execx.Run

// docsGit lists a GitHub repository's tags for the npm and PyPI rules.
var docsGit = catsrc.Git{Run: execx.Run, Protocols: "https"}

// runDocs is `makeit docs sync [dir]` (cli/ZERO-CONFIG.md section 2). Every docs problem is a
// line in the report and the exit code stays 0: the hooks run it and must never fail a session.
func runDocs(ctx context.Context, p cli.Parsed, stdout, stderr io.Writer) int {
	if len(p.Positionals) == 0 || p.Positionals[0] != "sync" || len(p.Positionals) > 2 {
		fmt.Fprintf(stderr, "%s: usage: %s docs sync [dir]\n", prog, prog)
		return 2
	}
	dir := "."
	if len(p.Positionals) == 2 {
		dir = p.Positionals[1]
	}
	quiet := p.Bool["quiet"]
	say := func(format string, a ...any) {
		if !quiet {
			fmt.Fprintf(stdout, format+"\n", a...)
		}
	}
	roots, err := paths.Current()
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", prog, err)
		return 1
	}
	if !p.Bool["dry-run"] {
		lock, err := fsx.Acquire(filepath.Join(roots.Stack, "lock"), time.Now())
		if err != nil {
			say("a docs sync or another makeit run is in progress: %v", err)
			return 0
		}
		defer lock.Release()
	}
	proj, err := initrepo.Detect(dir)
	if err != nil {
		say("docs: %v", err)
		return 0
	}
	if p.Bool["if-changed"] && sameManifests(docs.LoadRecord(roots.Stack, dir).Manifests, proj.Manifests) {
		return 0
	}
	libs := initrepo.Libs(proj)
	if p.Bool["dry-run"] {
		say("would sync the docs of %d libraries:", len(libs))
		for _, l := range libs {
			say("  %s %s", l.Name, l.Version)
		}
		return 0
	}
	o, err := docsOptions(roots.Stack)
	if err != nil {
		say("docs: %v", err)
		return 0
	}
	rep := docs.Sync(ctx, libs, o)
	for _, l := range rep.Lines() {
		say("%s", l)
	}
	if rep.Failed() {
		return 0
	}
	// The record holds what Detect read before the sync, so an edit made meanwhile syncs next time.
	if err := docs.SaveRecord(roots.Stack, dir, docs.Record{Dir: dir, Manifests: proj.Manifests, SyncedAt: time.Now().UTC()}); err != nil {
		say("docs: %v", err)
	}
	return 0
}

func sameManifests(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// docsOptions start the server the plugin pins: from the synced catalog when there is one, else
// from the catalog in this binary.
func docsOptions(stack string) (docs.Options, error) {
	var src catalog.Source = catalog.Embedded
	if dir := filepath.Join(stack, "catalog"); catalog.ReadMarker(dir).Digest != "" {
		src = catalog.Dir(dir)
	}
	b, err := src.ReadFile("plugins/makeit/.mcp.json")
	if err != nil {
		return docs.Options{}, err
	}
	server, err := docs.ServerArgv(b)
	if err != nil {
		return docs.Options{}, err
	}
	return docs.Options{Run: docsRun, Server: server, Resolver: docs.Resolver{
		HTTP: &http.Client{Timeout: 20 * time.Second},
		NPM:  "https://registry.npmjs.org",
		PyPI: "https://pypi.org/pypi",
		Tags: func(ctx context.Context, repo string) (map[string]string, error) {
			refs, err := catsrc.LsRemote(ctx, docsGit, repo)
			return refs.Tags, err
		},
	}}, nil
}
