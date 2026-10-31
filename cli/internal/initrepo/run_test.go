package initrepo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"makeit/internal/buildinfo"
	"makeit/internal/catalog"
	"makeit/internal/docs"
	"makeit/internal/execx"
	"makeit/internal/fsx"
	"makeit/internal/plan"
	"makeit/internal/setup"
	"makeit/internal/state"
)

var runNow = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

// fakeRunner stands in for git and docs-mcp-server: git init makes .git, list says every library
// is indexed and current, and every call is recorded.
type fakeRunner struct {
	mu    sync.Mutex
	calls []string
}

func (f *fakeRunner) run(_ context.Context, c execx.Cmd) (execx.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, strings.Join(c.Argv, " "))
	f.mu.Unlock()
	switch {
	case slices.Equal(c.Argv, []string{"git", "init", "-q"}):
		return execx.Result{}, os.MkdirAll(filepath.Join(c.Dir, ".git"), 0o755)
	case len(c.Argv) > 3 && c.Argv[3] == "list":
		return execx.Result{Stdout: `[{"name": "zod", "versions": [{"version": "4.1.12", "indexedAt": "2026-10-30T00:00:00.000Z"}]}]`}, nil
	}
	return execx.Result{}, nil
}

func (f *fakeRunner) take() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.calls
	f.calls = nil
	return c
}

func testEnv(t *testing.T, dir string) (Env, *fakeRunner) {
	t.Helper()
	req, err := catalog.LoadRequires(catalog.Embedded)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeRunner{}
	return Env{Dir: dir, Stack: t.TempDir(), Catalog: catalog.Embedded, Req: req, Run: f.run,
		Docs:    docs.Options{Run: f.run, Server: []string{"npx", "-y", "@arabold/docs-mcp-server@3.4.1"}, Now: func() time.Time { return runNow }},
		Present: func(string) bool { return true }, Now: func() time.Time { return runNow }, CatalogKind: "embedded"}, f
}

func ids(r Report) []string {
	var out []string
	for _, o := range r.Plan.Outcomes {
		out = append(out, o.ID)
	}
	return out
}

func loadRepo(t *testing.T, dir string) *state.Repo {
	t.Helper()
	r, _, err := state.LoadRepo(filepath.Join(dir, ".makeit.json"))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func sha(t *testing.T, path string) string {
	t.Helper()
	h, err := fsx.FileSha256(path)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func nodeRepo(t *testing.T) string {
	return tree(t, map[string]string{".git/": "", "package.json": "{\n  \"name\": \"shop\",\n  \"dependencies\": {\n    \"zod\": \"4.1.12\"\n  }\n}\n"})
}

func TestInitFresh(t *testing.T) {
	dir := nodeRepo(t)
	e, f := testEnv(t, dir)
	rep, err := Run(context.Background(), e, plan.Apply, nil, Options{})
	if err != nil || rep.Plan.Failed() {
		t.Fatalf("%v %s", err, rep.Plan.Summary())
	}
	want := []string{"doc:CLAUDE.md", "doc:CONSTRAINTS.md", "doc:HANDOFF.md", "merge:package.json", "merge:.gitignore", "docs", "record"}
	if !slices.Equal(ids(rep), want) {
		t.Fatalf("steps %v", ids(rep))
	}
	if c := f.take(); !slices.ContainsFunc(c, func(s string) bool { return strings.HasSuffix(s, " list --output json") }) {
		t.Errorf("no docs sync: %v", c)
	}
	r := loadRepo(t, dir)
	if r.CLI != buildinfo.Version || r.Catalog.Kind != "embedded" || !slices.Equal(r.Answers.Types, []string{"node"}) {
		t.Errorf("record %+v", r)
	}
	claude := r.Files["CLAUDE.md"]
	if claude == nil || claude.Class != "doc" || claude.Template != "templates/project/CLAUDE.md" || claude.PreExisted || claude.WrittenSha256 != sha(t, filepath.Join(dir, "CLAUDE.md")) {
		t.Errorf("CLAUDE.md record %+v", claude)
	}
	pkg := r.Files["package.json"]
	if pkg == nil || pkg.Class != "merge" || !pkg.PreExisted || pkg.WrittenSha256 != sha(t, filepath.Join(dir, "package.json")) ||
		!slices.Equal(pkg.Added, []string{"/scripts"}) {
		t.Errorf("package.json record %+v", pkg)
	}
	if gi := r.Files[".gitignore"]; gi == nil || gi.PreExisted || !slices.Equal(gi.Added, []string{".env", ".env.*", "!.env.example"}) {
		t.Errorf(".gitignore record %+v", gi)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if !strings.HasPrefix(string(body), "# "+filepath.Base(dir)+"\n") || !strings.Contains(string(body), "`npm run check:full`") {
		t.Errorf("CLAUDE.md not rendered:\n%.300s", body)
	}
	if docs.LoadRecord(e.Stack, dir).Manifests["package.json"] == "" {
		t.Error("the docs record was not written")
	}
}

func TestInitSecondRunNothing(t *testing.T) {
	dir := nodeRepo(t)
	e, f := testEnv(t, dir)
	if _, err := Run(context.Background(), e, plan.Apply, nil, Options{}); err != nil {
		t.Fatal(err)
	}
	f.take()
	before := map[string]string{}
	for _, n := range []string{"CLAUDE.md", "package.json", ".gitignore", ".makeit.json"} {
		before[n] = sha(t, filepath.Join(dir, n))
	}
	rep, err := Run(context.Background(), e, plan.Apply, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range rep.Plan.Outcomes {
		if o.Status != plan.Already {
			t.Errorf("second run: %s %s", o.ID, o.Status)
		}
	}
	for n, h := range before {
		if sha(t, filepath.Join(dir, n)) != h {
			t.Errorf("%s was rewritten", n)
		}
	}
	if c := f.take(); len(c) != 0 {
		t.Errorf("second run ran %v", c)
	}
}

func TestInitKeepsExisting(t *testing.T) {
	dir := nodeRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e, _ := testEnv(t, dir)
	rep, err := Run(context.Background(), e, plan.Apply, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md")); string(b) != "# mine\n" {
		t.Errorf("CLAUDE.md changed: %q", b)
	}
	if !slices.Contains(rep.Kept, setup.Kept{Item: "CLAUDE.md", Reason: "yours: kept as it is"}) {
		t.Errorf("kept %v", rep.Kept)
	}
	if r := loadRepo(t, dir).Files["CLAUDE.md"]; r == nil || !r.PreExisted || r.WrittenSha256 != "" {
		t.Errorf("record %+v", r)
	}
}

func TestInitGit(t *testing.T) {
	empty := t.TempDir()
	e, f := testEnv(t, empty)
	rep, err := Run(context.Background(), e, plan.Apply, nil, Options{})
	if err != nil || len(rep.Plan.Outcomes) == 0 || rep.Plan.Outcomes[0].ID != "git" {
		t.Fatalf("empty dir: %v %v", ids(rep), err)
	}
	if c := f.take(); c[0] != "git init -q" {
		t.Errorf("first call %q", c[0])
	}
	missing := filepath.Join(t.TempDir(), "new")
	e, _ = testEnv(t, missing)
	if _, err := Run(context.Background(), e, plan.Apply, nil, Options{}); err != nil {
		t.Fatalf("missing dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(missing, "CLAUDE.md")); err != nil {
		t.Errorf("missing dir not set up: %v", err)
	}
	busy := tree(t, map[string]string{"main.go": "package main\n"})
	e, f = testEnv(t, busy)
	_, err = Run(context.Background(), e, plan.Apply, nil, Options{})
	if err == nil || err.Error() != "not a git repository: run git init first, or run makeit init in an empty directory" {
		t.Fatalf("non-empty dir without git: %v", err)
	}
	if entries, _ := os.ReadDir(busy); len(entries) != 1 || len(f.take()) != 0 {
		t.Errorf("something was written or run: %v", entries)
	}
	e, _ = testEnv(t, t.TempDir())
	e.Present = func(name string) bool { return name != "git" }
	if _, err := Run(context.Background(), e, plan.Apply, nil, Options{}); err == nil || !strings.Contains(err.Error(), "needs git") {
		t.Errorf("no git: %v", err)
	}
}

func TestInitDryRun(t *testing.T) {
	for _, dir := range []string{nodeRepo(t), t.TempDir()} {
		e, f := testEnv(t, dir)
		before, _ := os.ReadDir(dir)
		rep, err := Run(context.Background(), e, plan.DryRunOnly, nil, Options{})
		if err != nil || len(rep.Plan.Outcomes) == 0 {
			t.Fatalf("%v %v", ids(rep), err)
		}
		for _, o := range rep.Plan.Outcomes {
			// An empty directory has no manifests, so there are no docs to index.
			if o.Status != plan.WouldChange && !(o.ID == "docs" && o.Status == plan.Already && len(before) == 0) {
				t.Errorf("%s: %s", o.ID, o.Status)
			}
		}
		after, _ := os.ReadDir(dir)
		if len(after) != len(before) || len(f.take()) != 0 {
			t.Errorf("a dry run wrote or ran something: %v", after)
		}
		if _, err := os.Stat(filepath.Join(e.Stack, "docs")); !os.IsNotExist(err) {
			t.Error("a dry run wrote the docs record")
		}
	}
}

func TestInitConsent(t *testing.T) {
	dir := nodeRepo(t)
	e, _ := testEnv(t, dir)
	_, err := Run(context.Background(), e, plan.NeedConsent, nil, Options{})
	if !errors.Is(err, setup.ErrConsent) {
		t.Fatalf("err %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Error("written without consent")
	}
	_, err = Run(context.Background(), e, plan.Prompt, func([]plan.Outcome) (bool, error) { return false, nil }, Options{})
	if !errors.Is(err, setup.ErrDeclined) {
		t.Errorf("declined: %v", err)
	}
}
