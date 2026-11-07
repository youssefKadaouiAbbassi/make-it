package initrepo

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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
	// gh answers gh calls: stdout and exit code for an argv and its stdin.
	gh func(argv []string, stdin string) (string, int)
}

func (f *fakeRunner) run(_ context.Context, c execx.Cmd) (execx.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, strings.Join(c.Argv, " "))
	f.mu.Unlock()
	if c.Argv[0] == "gh" {
		if !slices.Contains(c.Env, "GH_TELEMETRY=false") {
			return execx.Result{}, errors.New("gh run without GH_TELEMETRY=false")
		}
		stdin := ""
		if c.Stdin != nil {
			b, _ := io.ReadAll(c.Stdin)
			stdin = string(b)
		}
		out, code := f.gh(c.Argv, stdin)
		return execx.Result{Stdout: out, Code: code}, nil
	}
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
	want := []string{"doc:CLAUDE.md", "doc:CONSTRAINTS.md", "doc:HANDOFF.md", "merge:package.json", "merge:.gitignore", "merge:renovate.json",
		"settings:.pre-commit-config.yaml", "settings:.env.example", "ci:.grype.yaml", "ci:.github/workflows/gate.yml", "ci:ci/floor-guard.mjs", "docs", "record"}
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
		!slices.Equal(pkg.Added, []string{"/scripts/check:fast", "/scripts/check:task", "/scripts/check:full"}) {
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
	// A folder with files and no git is set up without git: everything but the git-only parts,
	// no repository created, and the run says what it left out.
	busy := tree(t, map[string]string{"main.go": "package main\n"})
	e, f = testEnv(t, busy)
	busyRep, err := Run(context.Background(), e, plan.Apply, nil, Options{})
	if err != nil {
		t.Fatalf("non-empty dir without git: %v", err)
	}
	if _, err := os.Stat(filepath.Join(busy, "CLAUDE.md")); err != nil {
		t.Errorf("not set up: %v", err)
	}
	for _, p := range []string{".git", ".pre-commit-config.yaml"} {
		if _, err := os.Stat(filepath.Join(busy, p)); err == nil {
			t.Errorf("%s written in a folder without git", p)
		}
	}
	if slices.Contains(f.take(), "git init -q") {
		t.Error("git init ran in a folder with files")
	}
	if !slices.ContainsFunc(busyRep.Notices, func(n string) bool { return strings.Contains(n, "no git repository") }) || slices.ContainsFunc(busyRep.Notices, func(n string) bool { return strings.Contains(n, "prek install") }) {
		t.Errorf("notices %v", busyRep.Notices)
	}
	e, _ = testEnv(t, tree(t, map[string]string{"main.go": "package main\n"}))
	if _, err := Run(context.Background(), e, plan.Apply, nil, Options{Rulesets: true}); err == nil || !strings.Contains(err.Error(), "git repository") {
		t.Errorf("--rulesets without git: %v", err)
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

// The go directive names the standard library, whose pkg.go.dev page at depth 0 is only a package
// list: indexing it would claim docs that answer nothing, so it is left out.
func TestLibsLeaveOutTheGoDirective(t *testing.T) {
	p := Project{Deps: []Dep{{Name: "go", Version: "1.27.3", Ecosystem: "go"}, {Name: "golang.org/x/term", Version: "v0.47.0", Ecosystem: "go"}}}
	got := Libs(p)
	if len(got) != 1 || got[0].Name != "golang.org/x/term" {
		t.Errorf("libs %v", got)
	}
}

// A symlinked leaf or directory is never read or written through: a merge would pull the outside
// file's content into the repository, and a linked directory would put writes outside it.
func TestInitDoesNotFollowSymlinks(t *testing.T) {
	outside := tree(t, map[string]string{"ignore": "secret\n", "conf/keep": "x\n"})
	dir := tree(t, map[string]string{".git/": "", "package.json": `{"dependencies": {"react": "19.2.0"}}`})
	for link, target := range map[string]string{".gitignore": "ignore", ".claude": "conf"} {
		if err := os.Symlink(filepath.Join(outside, target), filepath.Join(dir, link)); err != nil {
			t.Fatal(err)
		}
	}
	e, _ := testEnv(t, dir)
	rep, err := Run(context.Background(), e, plan.Apply, nil, Options{})
	if err != nil || rep.Plan.Failed() {
		t.Fatalf("%v %s", err, rep.Plan.Summary())
	}
	if b, _ := os.ReadFile(filepath.Join(outside, "ignore")); string(b) != "secret\n" {
		t.Errorf("the outside file changed: %q", b)
	}
	if st, err := os.Lstat(filepath.Join(dir, ".gitignore")); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Errorf(".gitignore is no longer the link: %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(outside, "conf")); len(entries) != 1 {
		t.Errorf("written outside the project: %v", entries)
	}
	for _, p := range []string{".gitignore", ".claude/settings.json"} {
		if !slices.Contains(rep.Kept, setup.Kept{Item: p, Reason: "a symlink: not followed"}) {
			t.Errorf("no kept line for %s: %v", p, rep.Kept)
		}
	}
	if r := loadRepo(t, dir); r.Files[".gitignore"] != nil || r.Files[".claude/settings.json"] != nil {
		t.Errorf("record names a linked path: %+v", r.Files)
	}
}

// --offline syncs no docs and runs no gh: the docs step is left out with a notice, and --rulesets
// is refused.
func TestInitOffline(t *testing.T) {
	dir := nodeRepo(t)
	e, f := testEnv(t, dir)
	rep, err := Run(context.Background(), e, plan.Apply, nil, Options{Offline: true})
	if err != nil || rep.Plan.Failed() {
		t.Fatalf("%v %s", err, rep.Plan.Summary())
	}
	if slices.Contains(ids(rep), "docs") || !slices.Contains(rep.Notices, "--offline: docs not synced") {
		t.Errorf("steps %v notices %q", ids(rep), rep.Notices)
	}
	if c := f.take(); len(c) != 0 {
		t.Errorf("offline ran %v", c)
	}
	if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); err != nil {
		t.Errorf("files not written: %v", err)
	}
	_, err = Run(context.Background(), e, plan.Apply, nil, Options{Offline: true, Rulesets: true})
	if err == nil || err.Error() != "--rulesets needs the network; drop --offline" {
		t.Errorf("--offline --rulesets: %v", err)
	}
}

// The docs record is saved only when the sync had no failure but unresolvable libraries: a failed
// list or scrape leaves it out, so the next run syncs again.
func TestInitDocsRecordMarksFailedSync(t *testing.T) {
	registry := httptest.NewServer(http.NotFoundHandler())
	defer registry.Close()
	for _, c := range []struct {
		name   string
		list   execx.Result
		failed bool
	}{
		{"list fails", execx.Result{Stderr: "npx: not found", Code: 127}, true},
		{"only unresolvable", execx.Result{Stdout: "[]"}, false},
	} {
		dir := nodeRepo(t)
		e, _ := testEnv(t, dir)
		e.Docs.Resolver = docs.Resolver{NPM: registry.URL}
		e.Docs.Run = func(_ context.Context, cmd execx.Cmd) (execx.Result, error) { return c.list, nil }
		rep, err := Run(context.Background(), e, plan.Apply, nil, Options{})
		if err != nil || rep.Plan.Failed() {
			t.Fatalf("%s: %v %s", c.name, err, rep.Plan.Summary())
		}
		// The record is kept either way, so the hook can wait before retrying a failed sync.
		if rec := docs.LoadRecord(e.Stack, dir); len(rec.Manifests) == 0 || rec.Failed != c.failed {
			t.Errorf("%s: docs record %+v, docs %q", c.name, rec, rep.Docs)
		}
		if !c.failed && !strings.Contains(strings.Join(rep.Docs, "\n"), "no versioned docs URL") {
			t.Errorf("%s: docs %q", c.name, rep.Docs)
		}
		// init is run on purpose, so it syncs a failed index again at once.
		again, err := Run(context.Background(), e, plan.DryRunOnly, nil, Options{})
		if err != nil {
			t.Fatal(err)
		}
		want := plan.Already
		if c.failed {
			want = plan.WouldChange
		}
		if got := stepStatus(again.Plan, "docs"); got != want {
			t.Errorf("%s: second init's docs step %q, want %q", c.name, got, want)
		}
	}
}

func TestInitSkipsDocsWhileSyncRuns(t *testing.T) {
	dir := nodeRepo(t)
	e, _ := testEnv(t, dir)
	calls := 0
	e.Docs.Run = func(context.Context, execx.Cmd) (execx.Result, error) {
		calls++
		return execx.Result{Stdout: "[]"}, nil
	}
	// A sync the docs hook started holds the docs lock; two syncs would write one index at once.
	lock, err := fsx.Acquire(docs.LockPath(e.Stack), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	rep, err := Run(context.Background(), e, plan.Apply, nil, Options{})
	if err != nil || rep.Plan.Failed() {
		t.Fatalf("%v %s", err, rep.Plan.Summary())
	}
	if calls != 0 {
		t.Errorf("docs-mcp-server ran %d times while another sync held the docs lock", calls)
	}
	if got := stepStatus(rep.Plan, "docs"); got != plan.Skipped {
		t.Errorf("docs step %q, want skipped", got)
	}
	if len(docs.LoadRecord(e.Stack, dir).Manifests) > 0 {
		t.Error("a docs record for a sync that did not run")
	}
}

// stepStatus is the status of the step with id in rep, or "".
func stepStatus(rep plan.Report, id string) plan.Status {
	for _, o := range rep.Outcomes {
		if o.ID == id {
			return o.Status
		}
	}
	return ""
}

// A merge step with nothing merged for its path refuses to write rather than empty the file.
func TestMergeRunNeedsMergedBody(t *testing.T) {
	dir := tree(t, map[string]string{".git/": "", "x.json": "{\"a\": 1}\n"})
	e, _ := testEnv(t, dir)
	r := &run{Env: e, prev: &state.Repo{}}
	r.reset()
	st := r.mergeStep(MergeSeed{Path: "x.json", Apply: func([]byte, map[string]bool) (Merged, error) { return Merged{}, nil }})
	if _, err := st.Run(context.Background()); err == nil {
		t.Error("Run wrote with nothing merged")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "x.json")); string(b) != "{\"a\": 1}\n" {
		t.Errorf("x.json is now %q", b)
	}
}

// A renovate.json with comments cannot be merged: it stays byte for byte, with a kept line.
func TestInitKeepsUnparseableMerge(t *testing.T) {
	const jsonc = "{\n  // ours\n  \"extends\": [\"config:recommended\"]\n}\n"
	dir := tree(t, map[string]string{".git/": "", "go.mod": "module x\n", "renovate.json": jsonc})
	e, _ := testEnv(t, dir)
	rep, err := Run(context.Background(), e, plan.Apply, nil, Options{})
	if err != nil || rep.Plan.Failed() {
		t.Fatalf("%v %s", err, rep.Plan.Summary())
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "renovate.json")); string(b) != jsonc {
		t.Errorf("renovate.json changed: %q", b)
	}
	if !slices.ContainsFunc(rep.Kept, func(k setup.Kept) bool {
		return k.Item == "renovate.json" && strings.HasPrefix(k.Reason, "not edited: ")
	}) {
		t.Errorf("kept %v", rep.Kept)
	}
}

// workspaceTree is a folder outside git with two repositories, one worktree and a plain folder.
func workspaceTree(t *testing.T) string {
	return tree(t, map[string]string{
		"a/.git/": "", "a/package.json": `{"dependencies": {"left-pad": "1.0.0"}}`,
		"b/.git/": "", "b/go.mod": "module b\n\ngo 1.24\n",
		"a-wt/.git": "gitdir: ../a/.git/worktrees/a-wt\n", "a-wt/package.json": `{"dependencies": {"lodash": "4.17.21"}}`,
		"notes/readme.md": "x\n",
	})
}

// gitOnly are the files a workspace leaves out: they act inside a repository.
var gitOnly = []string{".gitignore", "renovate.json", ".grype.yaml", ".env.example", ".pre-commit-config.yaml", ".github", "ci"}

func TestInitWorkspaceLeavesGitOnlyFilesOut(t *testing.T) {
	dir := workspaceTree(t)
	e, _ := testEnv(t, dir)
	rep, err := Run(context.Background(), e, plan.Apply, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range gitOnly {
		if _, err := os.Stat(filepath.Join(dir, p)); err == nil {
			t.Errorf("%s written in a folder of repositories", p)
		}
	}
	for _, p := range []string{"CLAUDE.md", "CONSTRAINTS.md", "HANDOFF.md", ".makeit.json"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("%s not written: %v", p, err)
		}
	}
	if !slices.ContainsFunc(rep.Notices, func(n string) bool {
		return strings.Contains(n, "a folder of repositories (a, b)") && strings.Contains(n, "1 worktree")
	}) {
		t.Errorf("notices %v", rep.Notices)
	}
	if slices.ContainsFunc(rep.Notices, func(n string) bool { return strings.Contains(n, "no git repository here") }) {
		t.Errorf("the plain no-git notice in a workspace: %v", rep.Notices)
	}
}

// An older makeit set the folder up as plain no-git: --update in what is now a workspace removes
// the git-only files it created and nobody changed, keeps a changed one, and leaves its lines in
// a file that was there before.
func TestInitWorkspaceUpdateRemovesOldFiles(t *testing.T) {
	dir := tree(t, map[string]string{"notes/readme.md": "x\n", ".gitignore": "node_modules\n"})
	e, _ := testEnv(t, dir)
	if _, err := Run(context.Background(), e, plan.Apply, nil, Options{}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"renovate.json", ".grype.yaml", ".env.example"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Fatalf("the old setup did not write %s: %v", p, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, ".env.example"), []byte("MINE=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"a/.git", "b/.git"} {
		if err := os.MkdirAll(filepath.Join(dir, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	rep, err := Run(context.Background(), e, plan.Apply, nil, Options{Update: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"renovate.json", ".grype.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err == nil {
			t.Errorf("%s, written by the old setup and unchanged, is still there", p)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".env.example")); string(b) != "MINE=1\n" {
		t.Errorf(".env.example changed by you was touched: %q", b)
	}
	if !slices.ContainsFunc(rep.Kept, func(k setup.Kept) bool { return k.Item == ".env.example" }) {
		t.Errorf("kept %+v, want .env.example listed", rep.Kept)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".gitignore")); !strings.HasPrefix(string(b), "node_modules\n") {
		t.Errorf(".gitignore you had lost its lines: %q", b)
	}
	if !slices.ContainsFunc(rep.Notices, func(n string) bool { return strings.Contains(n, ".gitignore") && strings.Contains(n, "by hand") }) {
		t.Errorf("notices %v, want one naming makeit's lines in .gitignore", rep.Notices)
	}
	rec := loadRepo(t, dir)
	for _, p := range []string{"renovate.json", ".grype.yaml", ".env.example", ".gitignore"} {
		if rec.Files[p] != nil {
			t.Errorf("%s still in the record", p)
		}
	}
}

// oldNoGitSetup is a plain no-git folder an older makeit set up, then turned into a workspace.
func oldNoGitSetup(t *testing.T) (string, Env) {
	t.Helper()
	dir := tree(t, map[string]string{"notes/readme.md": "x\n"})
	e, _ := testEnv(t, dir)
	if _, err := Run(context.Background(), e, plan.Apply, nil, Options{}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"a/.git", "b/.git"} {
		if err := os.MkdirAll(filepath.Join(dir, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir, e
}

// The cleanup is --update's (the docs say so): a plain run, as /makeit:init's first step is,
// leaves the old files on disk and in the record for a later --update.
func TestInitWorkspaceRemovesOnlyOnUpdate(t *testing.T) {
	dir, e := oldNoGitSetup(t)
	if _, err := Run(context.Background(), e, plan.Apply, nil, Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "renovate.json")); err != nil {
		t.Fatalf("a run without --update removed renovate.json: %v", err)
	}
	if loadRepo(t, dir).Files["renovate.json"] == nil {
		t.Fatal("renovate.json left the record, so --update could no longer remove it")
	}
	if _, err := Run(context.Background(), e, plan.Apply, nil, Options{Update: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "renovate.json")); err == nil {
		t.Error("--update did not remove renovate.json")
	}
}

// A removal never follows a symlink: a linked folder or file stays, with its target (a ci/ linked
// elsewhere keeps its floor-guard.mjs).
func TestInitWorkspaceDropFollowsNoLink(t *testing.T) {
	dir, e := oldNoGitSetup(t)
	elsewhere := t.TempDir()
	body, err := os.ReadFile(filepath.Join(dir, "renovate.json"))
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(elsewhere, "renovate.json")
	if err := os.WriteFile(target, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "renovate.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "renovate.json")); err != nil {
		t.Fatal(err)
	}
	rep, err := Run(context.Background(), e, plan.Apply, nil, Options{Update: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("the link's target is gone: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "renovate.json")); err != nil {
		t.Errorf("the link itself was removed: %v", err)
	}
	if !slices.ContainsFunc(rep.Kept, func(k setup.Kept) bool { return k.Item == "renovate.json" && strings.Contains(k.Reason, "symlink") }) {
		t.Errorf("kept %+v, want renovate.json as a symlink", rep.Kept)
	}
}

// After the drops, a folder they emptied goes too (.github/workflows), never one with files.
func TestInitWorkspaceDropLeavesNoEmptyFolder(t *testing.T) {
	dir, e := oldNoGitSetup(t)
	wf := filepath.Join(dir, ".github", "workflows")
	if err := os.MkdirAll(wf, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := loadRepo(t, dir)
	b := []byte("name: gate\n")
	if err := os.WriteFile(filepath.Join(wf, "gate.yml"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".github", "CODEOWNERS"), []byte("* @x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec.Files[".github/workflows/gate.yml"] = &state.RepoFile{Class: "ci", Template: "ci/.github/workflows/gate-caller.yml", WrittenSha256: fsx.Sha256(b)}
	if err := state.SaveRepo(filepath.Join(dir, ".makeit.json"), rec); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), e, plan.Apply, nil, Options{Update: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wf); err == nil {
		t.Error(".github/workflows, emptied, is still there")
	}
	if _, err := os.Stat(filepath.Join(dir, ".github", "CODEOWNERS")); err != nil {
		t.Errorf(".github with your CODEOWNERS was touched: %v", err)
	}
}

// A workspace has no CI workflow, so the lines about it do not show: the npm ci
// lockfile line for a root package.json, the CI caller notice.
func TestInitWorkspaceNoCILines(t *testing.T) {
	dir := workspaceTree(t)
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name": "ws", "scripts": {}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	e, _ := testEnv(t, dir)
	rep, err := Run(context.Background(), e, plan.Apply, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range append(append([]string{}, rep.Manual...), rep.Notices...) {
		if strings.Contains(l, "npm ci") || strings.Contains(l, "CI caller") {
			t.Errorf("a CI line in a workspace: %q", l)
		}
	}
}
