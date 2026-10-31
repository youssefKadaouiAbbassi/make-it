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
// retryFailed is how long --if-changed waits before it retries a sync that failed: the hook
// starts one on every tool use, and a library that keeps failing would rerun each time.
const retryFailed = time.Hour

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
	if p.Bool["offline"] {
		say("--offline: no docs sync")
		return 0
	}
	roots, err := paths.Current()
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", prog, err)
		return 1
	}
	if !p.Bool["dry-run"] {
		// Its own lock, not the stack run lock: a sync takes minutes and must not block setup or update.
		lock, err := fsx.Acquire(docs.LockPath(roots.Stack), time.Now())
		if err != nil {
			say("another docs sync is in progress: %v", err)
			return 0
		}
		defer lock.Release()
	}
	proj, err := initrepo.Detect(dir)
	if err != nil {
		say("docs: %v", err)
		return 0
	}
	if rec := docs.LoadRecord(roots.Stack, dir); p.Bool["if-changed"] && sameManifests(rec.Manifests, proj.Manifests) &&
		(!rec.Failed || time.Since(rec.SyncedAt) < retryFailed) {
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
	// The record holds what Detect read before the sync, so an edit made meanwhile syncs next time.
	if err := docs.SaveRecord(roots.Stack, dir, docs.Record{Dir: dir, Manifests: proj.Manifests, SyncedAt: time.Now().UTC(), Failed: rep.Failed()}); err != nil {
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
