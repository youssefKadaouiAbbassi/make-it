package initrepo

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// tree writes files (path -> content) under a new directory; a path ending in "/" is a directory.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for p, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if p[len(p)-1] == '/' {
			if err := os.MkdirAll(full, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const pkg = `{"name": "app", "dependencies": {"zod": "^4.1.12"}}`

func TestDetect(t *testing.T) {
	for _, c := range []struct {
		name        string
		files       map[string]string
		types       []string
		pm, runner  string
		setup, spec string
	}{
		{"node-npm", map[string]string{"package.json": pkg}, []string{"node"}, "npm", "npm run", "npm ci", ""},
		{"node-pnpm", map[string]string{"package.json": pkg, "pnpm-lock.yaml": ""}, []string{"node"}, "pnpm", "pnpm", "npm install -g pnpm@12.16.1 && pnpm install --frozen-lockfile", ""},
		{"node-pnpm-pinned", map[string]string{"package.json": `{"packageManager": "pnpm@9.15.0+sha512.abc"}`, "pnpm-lock.yaml": ""}, []string{"node"}, "pnpm", "pnpm", "npm install -g pnpm@9.15.0 && pnpm install --frozen-lockfile", ""},
		{"yarn", map[string]string{"package.json": pkg, "yarn.lock": ""}, []string{"node"}, "yarn", "yarn", "yarn install --frozen-lockfile", ""},
		{"yarn-berry", map[string]string{"package.json": pkg, "yarn.lock": "", ".yarnrc.yml": "nodeLinker: node-modules\n"}, []string{"node"}, "yarn", "yarn", "yarn install --immutable", ""},
		{"bun", map[string]string{"package.json": pkg, "bun.lock": ""}, []string{"node"}, "bun", "bun", "bun install --frozen-lockfile", ""},
		{"python-uv", map[string]string{"pyproject.toml": "[project]\nname = \"x\"\ndependencies = [\n  \"httpx>=0.28\",\n]\n", "uv.lock": ""}, []string{"python"}, "uv", "uv run poe", "pipx install uv==0.13.6 && uv sync --frozen", ""},
		{"go", map[string]string{"go.mod": "module x\n\ngo 1.27.3\n"}, []string{"go"}, "go", "make", "", ""},
		{"rust", map[string]string{"Cargo.toml": "[package]\nname = \"x\"\n"}, []string{"rust"}, "cargo", "make", "", ""},
		{"web", map[string]string{"package.json": `{"dependencies": {"react": "19.2.0"}}`}, []string{"node", "web"}, "npm", "npm run", "npm ci", ""},
		{"api", map[string]string{"go.mod": "module x\n", "openapi.yaml": "openapi: 3.1.0\n"}, []string{"go", "api"}, "go", "make", "", "openapi.yaml"},
		{"api-nested", map[string]string{"go.mod": "module x\n", "docs/api/openapi.yml": "openapi: 3.1.0\n"}, []string{"go", "api"}, "go", "make", "", "docs/api/openapi.yml"},
		{"database-dir", map[string]string{"go.mod": "module x\n", "migrations/": ""}, []string{"go", "database"}, "go", "make", "", ""},
		{"database-dep", map[string]string{"package.json": `{"dependencies": {"drizzle-orm": "1.0.0"}}`}, []string{"node", "database"}, "npm", "npm run", "npm ci", ""},
		{"database-env", map[string]string{"go.mod": "module x\n", ".env.example": "DATABASE_URL=postgres://x\n"}, []string{"go", "database"}, "go", "make", "", ""},
		{"model", map[string]string{"package.json": `{"dependencies": {"@anthropic-ai/sdk": "1.0.0"}}`}, []string{"node", "model"}, "npm", "npm run", "npm ci", ""},
		{"model-py", map[string]string{"pyproject.toml": "[project]\ndependencies = [\"anthropic==0.70.0\"]\n", "uv.lock": ""}, []string{"python", "model"}, "uv", "uv run poe", "pipx install uv==0.13.6 && uv sync --frozen", ""},
		{"mobile", map[string]string{"package.json": `{"dependencies": {"expo": "54.0.0"}}`}, []string{"node", "mobile"}, "npm", "npm run", "npm ci", ""},
		{"mobile-dir", map[string]string{"go.mod": "module x\n", "android/": ""}, []string{"go", "mobile"}, "go", "make", "", ""},
		{"empty", map[string]string{}, nil, "", "", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, err := Detect(tree(t, c.files))
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(p.Types, c.types) || p.PM != c.pm || p.Runner != c.runner || p.Setup != c.setup || p.Spec != c.spec {
				t.Errorf("got types %v pm %q runner %q setup %q spec %q", p.Types, p.PM, p.Runner, p.Setup, p.Spec)
			}
		})
	}
}

func TestDetectGitHub(t *testing.T) {
	for _, url := range []string{"git@github.com:o/r.git", "https://github.com/o/r.git", "https://github.com/o/r"} {
		dir := tree(t, map[string]string{".git/config": "[remote \"origin\"]\n\turl = " + url + "\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n"})
		p, err := Detect(dir)
		if err != nil || p.GitHub.Owner != "o" || p.GitHub.Repo != "r" {
			t.Errorf("%s: %+v %v", url, p.GitHub, err)
		}
	}
	p, _ := Detect(tree(t, map[string]string{".git/config": "[remote \"origin\"]\n\turl = https://gitlab.com/o/r.git\n"}))
	if p.GitHub.Owner != "" {
		t.Errorf("gitlab taken as GitHub: %+v", p.GitHub)
	}
}

// Review Focus 5: manifests below the root feed the docs; types and CI come from the root.
func TestDetectMonorepo(t *testing.T) {
	p, err := Detect(tree(t, map[string]string{
		"package.json":                       pkg,
		"apps/api/go.mod":                    "module api\n\ngo 1.27.3\n\nrequire (\n\tgolang.org/x/term v0.47.0\n\tgolang.org/x/sys v0.40.0 // indirect\n)\n",
		"node_modules/zod/package.json":      `{"name": "zod", "version": "4.1.13"}`,
		"node_modules/other/go.mod":          "module skipped\n",
		"apps/web/node_modules/q/Cargo.toml": "[dependencies]\nq = \"1\"\n",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.Types, []string{"node"}) {
		t.Errorf("types %v", p.Types)
	}
	want := []Dep{
		{Name: "go", Version: "1.27.3", Ecosystem: "go", Manifest: "apps/api/go.mod"},
		{Name: "golang.org/x/term", Version: "v0.47.0", Ecosystem: "go", Manifest: "apps/api/go.mod"},
		{Name: "zod", Version: "4.1.13", Ecosystem: "npm", Manifest: "package.json"},
	}
	if !slices.Equal(p.Deps, want) {
		t.Errorf("deps\n got %v\nwant %v", p.Deps, want)
	}
}

func TestDetectVersions(t *testing.T) {
	p, _ := Detect(tree(t, map[string]string{"package.json": pkg,
		"package-lock.json": `{"packages": {"": {}, "node_modules/zod": {"version": "4.1.13"}}}`}))
	if len(p.Deps) != 1 || p.Deps[0].Version != "4.1.13" {
		t.Errorf("lockfile version: %v", p.Deps)
	}
	p, _ = Detect(tree(t, map[string]string{"package.json": pkg}))
	if len(p.Deps) != 1 || p.Deps[0].Version != "4.1.12" {
		t.Errorf("range stripped: %v", p.Deps)
	}
	p, _ = Detect(tree(t, map[string]string{
		"pyproject.toml": "[project]\ndependencies = [\n  \"httpx>=0.28\",\n  \"pydantic[email]~=2.12\",\n]\n",
		"uv.lock":        "[[package]]\nname = \"httpx\"\nversion = \"0.28.1\"\n\n[[package]]\nname = \"pydantic\"\nversion = \"2.12.3\"\n",
		"Cargo.toml":     "[package]\nname = \"x\"\n\n[dependencies]\nserde = { version = \"1.0\", features = [\"derive\"] }\nanyhow = \"1\"\n\n[dev-dependencies]\nproptest = \"1.5\"\n",
		"Cargo.lock":     "[[package]]\nname = \"serde\"\nversion = \"1.0.228\"\n\n[[package]]\nname = \"anyhow\"\nversion = \"1.0.100\"\n",
	}))
	want := []Dep{
		{Name: "anyhow", Version: "1.0.100", Ecosystem: "cargo", Manifest: "Cargo.toml"},
		{Name: "serde", Version: "1.0.228", Ecosystem: "cargo", Manifest: "Cargo.toml"},
		{Name: "httpx", Version: "0.28.1", Ecosystem: "pypi", Manifest: "pyproject.toml"},
		{Name: "pydantic", Version: "2.12.3", Ecosystem: "pypi", Manifest: "pyproject.toml"},
	}
	if !slices.Equal(p.Deps, want) {
		t.Errorf("toml deps\n got %v\nwant %v", p.Deps, want)
	}
}

// requirements.txt feeds the docs (as docs.sh lists it) but does not make a python project.
func TestDetectRequirements(t *testing.T) {
	dir := tree(t, map[string]string{
		"requirements.txt": "# pinned\nhttpx==0.28.1  # client\nRich>=14.0,<15\npydantic[email]~=2.12 ; python_version >= \"3.10\"\n" +
			"-r dev.txt\n-e .\n--index-url https://pypi.org/simple\nfoo @ https://example.com/foo.whl\nhttps://example.com/bar.tar.gz\n./vendored\n\nplain\nsqlalchemy==2.0.44\n",
		"services/api/requirements.txt": "anthropic==0.70.0\n",
	})
	p, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Types != nil {
		t.Errorf("types %v", p.Types)
	}
	want := []Dep{
		{Name: "httpx", Version: "0.28.1", Ecosystem: "pypi", Manifest: "requirements.txt"},
		{Name: "plain", Version: "", Ecosystem: "pypi", Manifest: "requirements.txt"},
		{Name: "pydantic", Version: "2.12", Ecosystem: "pypi", Manifest: "requirements.txt"},
		{Name: "rich", Version: "14.0", Ecosystem: "pypi", Manifest: "requirements.txt"},
		{Name: "sqlalchemy", Version: "2.0.44", Ecosystem: "pypi", Manifest: "requirements.txt"},
		{Name: "anthropic", Version: "0.70.0", Ecosystem: "pypi", Manifest: "services/api/requirements.txt"},
	}
	if !slices.Equal(p.Deps, want) {
		t.Errorf("deps\n got %v\nwant %v", p.Deps, want)
	}
	h, _ := ManifestHashes(dir)
	if keys := slices.Sorted(maps.Keys(p.Manifests)); !slices.Equal(keys, []string{"requirements.txt", "services/api/requirements.txt"}) || !maps.Equal(h, p.Manifests) {
		t.Errorf("read %v, on disk %v", p.Manifests, h)
	}
}

// The docs record compares these: what Detect read, and what is on disk now.
func TestManifestHashes(t *testing.T) {
	dir := tree(t, map[string]string{"package.json": pkg, "package-lock.json": `{"packages": {}}`,
		"apps/api/go.mod": "module api\n", "node_modules/zod/package.json": `{"version": "4.1.13"}`})
	p, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	now, err := ManifestHashes(dir)
	if err != nil {
		t.Fatal(err)
	}
	keys := slices.Sorted(maps.Keys(p.Manifests))
	if !slices.Equal(keys, []string{"apps/api/go.mod", "package-lock.json", "package.json"}) || !maps.Equal(p.Manifests, now) {
		t.Fatalf("read %v, on disk %v", p.Manifests, now)
	}
	if err := os.WriteFile(filepath.Join(dir, "package-lock.json"), []byte(`{"packages": {"node_modules/zod": {"version": "4.1.14"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if after, _ := ManifestHashes(dir); maps.Equal(after, p.Manifests) {
		t.Error("a lockfile change was not seen")
	}
}

func TestDetectRepoFacts(t *testing.T) {
	dir := tree(t, map[string]string{
		".git/config":                   "[remote \"origin\"]\n\turl = git@github.com:o/r.git\n",
		".git/refs/remotes/origin/HEAD": "ref: refs/remotes/origin/trunk\n",
		"package.json":                  `{"scripts": {"test:e2e": "playwright test"}, "devDependencies": {"prettier": "3.6.2"}}`,
		"eslint.config.js":              "export default [];\n",
		"package-lock.json":             `{"packages": {}}`,
	})
	p, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.DefaultBranch != "trunk" || !p.Scripts["test:e2e"] || p.Lint != "eslint" || !p.Prettier || !p.Lockfile {
		t.Errorf("%+v", p)
	}
	sub := filepath.Join(dir, "apps", "web")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "package.json"), []byte(`{"name": "web"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if p, _ := Detect(sub); p.GitHub.Owner != "o" || p.DefaultBranch != "trunk" {
		t.Errorf("a subfolder of the repository: %+v %q", p.GitHub, p.DefaultBranch)
	}
	if p, _ := Detect(tree(t, map[string]string{"package.json": "{}", "biome.jsonc": "{}", "eslint.config.mjs": ""})); p.Lint != "biome" || p.DefaultBranch != "main" || p.Lockfile {
		t.Errorf("biome wins, main by default, no lockfile: %+v", p)
	}
}

// A submodule and a linked worktree have a .git file: the remote and default branch come from the
// git directory it names (and that directory's commondir), not from the superproject or nothing.
func TestDetectGitFile(t *testing.T) {
	remote := func(r string) string { return "[remote \"origin\"]\n\turl = git@github.com:o/" + r + ".git\n" }
	super := tree(t, map[string]string{
		".git/config":                               remote("super"),
		".git/refs/remotes/origin/HEAD":             "ref: refs/remotes/origin/main\n",
		".git/modules/sub/config":                   remote("sub"),
		".git/modules/sub/refs/remotes/origin/HEAD": "ref: refs/remotes/origin/dev\n",
		"sub/.git":   "gitdir: ../.git/modules/sub\n",
		"sub/go.mod": "module sub\n",
	})
	if p, err := Detect(filepath.Join(super, "sub")); err != nil || p.GitHub.Repo != "sub" || p.DefaultBranch != "dev" {
		t.Errorf("submodule: %+v %q %v", p.GitHub, p.DefaultBranch, err)
	}
	main := tree(t, map[string]string{
		".git/config":                   remote("main"),
		".git/refs/remotes/origin/HEAD": "ref: refs/remotes/origin/trunk\n",
		".git/worktrees/wt/commondir":   "../..\n",
		".git/worktrees/wt/HEAD":        "ref: refs/heads/feature\n",
	})
	wt := tree(t, map[string]string{".git": "gitdir: " + filepath.Join(main, ".git", "worktrees", "wt") + "\n", "go.mod": "module wt\n"})
	if p, err := Detect(wt); err != nil || p.GitHub.Repo != "main" || p.DefaultBranch != "trunk" {
		t.Errorf("linked worktree: %+v %q %v", p.GitHub, p.DefaultBranch, err)
	}
}

// One unreadable directory (a database bind mount owned by another user) is skipped, not fatal.
func TestDetectUnreadableDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a 0000 directory")
	}
	dir := tree(t, map[string]string{"package.json": pkg, "pgdata/go.mod": "module hidden\n", "apps/api/go.mod": "module api\n"})
	locked := filepath.Join(dir, "pgdata")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	p, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if !slices.Equal(p.Types, []string{"node"}) {
		t.Errorf("types %v", p.Types)
	}
	h, err := ManifestHashes(dir)
	if err != nil || !maps.Equal(h, p.Manifests) || len(h) != 2 {
		t.Errorf("ManifestHashes %v %v; Detect read %v", h, err, p.Manifests)
	}
}

// A folder outside git whose direct children are repositories is a workspace: each repository's
// manifests count, a worktree child (.git is a file) and a plain folder do not (2026-11-06,
// the Uni-CE root: 3 repositories and 23 worktrees, 2,176 libraries).
func TestDetectWorkspace(t *testing.T) {
	dir := tree(t, map[string]string{
		"a/.git/":            "",
		"a/package.json":     `{"dependencies": {"left-pad": "1.0.0"}}`,
		"b/.git/":            "",
		"b/go.mod":           "module b\n\ngo 1.24\n\nrequire golang.org/x/term v0.47.0\n",
		"a-wt/.git":          "gitdir: ../a/.git/worktrees/a-wt\n",
		"a-wt/package.json":  `{"dependencies": {"lodash": "4.17.21"}}`,
		"notes/package.json": `{"dependencies": {"chalk": "5.0.0"}}`,
	})
	p, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.Repos, []string{"a", "b"}) || p.Worktrees != 1 {
		t.Errorf("repos %v worktrees %d, want [a b] and 1", p.Repos, p.Worktrees)
	}
	names := map[string]bool{}
	for _, d := range p.Deps {
		names[d.Name] = true
	}
	if !names["left-pad"] || !names["golang.org/x/term"] || names["lodash"] || names["chalk"] {
		t.Errorf("deps %v: want left-pad and golang.org/x/term only", slices.Sorted(maps.Keys(names)))
	}
	// The docs hook's --if-changed compares these: the same files Detect read.
	if h, err := ManifestHashes(dir); err != nil || !maps.Equal(h, p.Manifests) {
		t.Errorf("ManifestHashes %v (%v), want what Detect read %v", slices.Sorted(maps.Keys(h)), err, slices.Sorted(maps.Keys(p.Manifests)))
	}
}

// A folder with files and no repository child keeps 0.3.3's no-git reading.
func TestDetectPlainFolderUnchanged(t *testing.T) {
	dir := tree(t, map[string]string{"notes/package.json": `{"dependencies": {"chalk": "5.0.0"}}`})
	p, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Repos != nil || p.Worktrees != 0 || !slices.ContainsFunc(p.Deps, func(d Dep) bool { return d.Name == "chalk" }) {
		t.Errorf("repos %v worktrees %d deps %v", p.Repos, p.Worktrees, p.Deps)
	}
}

// Only a .git file that points into another repository's worktrees counts as a worktree.
func TestDetectCountsOnlyWorktrees(t *testing.T) {
	dir := tree(t, map[string]string{
		"a/.git/":  "",
		"wt/.git":  "gitdir: /x/a/.git/worktrees/wt\n",
		"mod/.git": "gitdir: ../.git/modules/mod\n",
	})
	if p, err := Detect(dir); err != nil || p.Worktrees != 1 {
		t.Errorf("worktrees %d (%v), want 1", p.Worktrees, err)
	}
}
