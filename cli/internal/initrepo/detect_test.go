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
		{"node-pnpm", map[string]string{"package.json": pkg, "pnpm-lock.yaml": ""}, []string{"node"}, "pnpm", "pnpm", "pnpm install --frozen-lockfile", ""},
		{"yarn", map[string]string{"package.json": pkg, "yarn.lock": ""}, []string{"node"}, "yarn", "yarn", "yarn install --frozen-lockfile", ""},
		{"bun", map[string]string{"package.json": pkg, "bun.lock": ""}, []string{"node"}, "bun", "bun", "bun install --frozen-lockfile", ""},
		{"python-uv", map[string]string{"pyproject.toml": "[project]\nname = \"x\"\ndependencies = [\n  \"httpx>=0.28\",\n]\n", "uv.lock": ""}, []string{"python"}, "uv", "uv run poe", "uv sync --frozen", ""},
		{"go", map[string]string{"go.mod": "module x\n\ngo 1.27.3\n"}, []string{"go"}, "go", "make", "", ""},
		{"rust", map[string]string{"Cargo.toml": "[package]\nname = \"x\"\n"}, []string{"rust"}, "cargo", "make", "", ""},
		{"web", map[string]string{"package.json": `{"dependencies": {"react": "19.2.0"}}`}, []string{"node", "web"}, "npm", "npm run", "npm ci", ""},
		{"api", map[string]string{"go.mod": "module x\n", "openapi.yaml": "openapi: 3.1.0\n"}, []string{"go", "api"}, "go", "make", "", "openapi.yaml"},
		{"api-nested", map[string]string{"go.mod": "module x\n", "docs/api/openapi.yml": "openapi: 3.1.0\n"}, []string{"go", "api"}, "go", "make", "", "docs/api/openapi.yml"},
		{"database-dir", map[string]string{"go.mod": "module x\n", "migrations/": ""}, []string{"go", "database"}, "go", "make", "", ""},
		{"database-dep", map[string]string{"package.json": `{"dependencies": {"drizzle-orm": "1.0.0"}}`}, []string{"node", "database"}, "npm", "npm run", "npm ci", ""},
		{"database-env", map[string]string{"go.mod": "module x\n", ".env.example": "DATABASE_URL=postgres://x\n"}, []string{"go", "database"}, "go", "make", "", ""},
		{"model", map[string]string{"package.json": `{"dependencies": {"@anthropic-ai/sdk": "1.0.0"}}`}, []string{"node", "model"}, "npm", "npm run", "npm ci", ""},
		{"model-py", map[string]string{"pyproject.toml": "[project]\ndependencies = [\"anthropic==0.70.0\"]\n", "uv.lock": ""}, []string{"python", "model"}, "uv", "uv run poe", "uv sync --frozen", ""},
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
