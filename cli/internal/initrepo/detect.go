// Package initrepo is `makeit init`: it detects a project, seeds the stack's files into it and
// records what it wrote in .makeit.json (cli/INIT.md).
package initrepo

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Dep is one direct dependency read from a manifest. Version is exact when a lockfile, the
// installed package or the manifest itself gives it, else the manifest's range without its
// operator.
type Dep struct {
	Name      string
	Version   string
	Ecosystem string // go, npm, pypi or cargo
	Manifest  string // slash path relative to the project
}

// Project is what init learned about a directory.
type Project struct {
	Dir string
	// Types are drawn from node, python, go, rust (from the root manifests), then web, api,
	// database, model and mobile, in that order.
	Types []string
	// PM is the package manager; Runner prefixes a named task (the gate's runner input) and
	// Setup installs the dependencies (the gate's setup input).
	PM, Runner, Setup string
	NodeVersion       string
	// Spec is the OpenAPI file, relative to Dir, when there is one.
	Spec string
	// Deps are the direct dependencies of every manifest to depth 3, sorted by manifest, then name.
	Deps   []Dep
	GitHub struct{ Owner, Repo string }
}

// pruned are directories no manifest or spec is looked for in (docs.sh prunes the same).
var pruned = map[string]bool{"node_modules": true, "vendor": true, ".git": true, "target": true, ".venv": true}

// manifestNames are the files Detect reads dependencies from.
var manifestNames = map[string]bool{"go.mod": true, "package.json": true, "pyproject.toml": true, "Cargo.toml": true}

var (
	webDeps    = []string{"react", "vue", "svelte", "solid-js", "next", "nuxt", "@sveltejs/kit", "astro", "vite"}
	dbDeps     = []string{"prisma", "@prisma/client", "drizzle-orm", "knex", "typeorm", "sequelize", "kysely", "sqlalchemy", "psycopg", "psycopg2", "asyncpg", "gorm.io/gorm", "github.com/jmoiron/sqlx", "github.com/jackc/pgx/v5", "diesel", "sqlx", "sea-orm"}
	modelDeps  = []string{"@anthropic-ai/sdk", "ai", "openai", "anthropic", "pydantic-ai", "instructor"}
	mobileDeps = []string{"expo", "react-native"}
)

// Detect reads dir's manifests, lockfiles and layout. A directory with no manifest gives a
// Project with no types.
func Detect(dir string) (Project, error) {
	p := Project{Dir: dir}
	manifests, err := findManifests(dir)
	if err != nil {
		return p, err
	}
	root := map[string]bool{}
	for _, m := range manifests {
		deps, err := readDeps(dir, m)
		if err != nil {
			return p, err
		}
		p.Deps = append(p.Deps, deps...)
		if !strings.Contains(m, "/") {
			root[m] = true
		}
	}
	sort.Slice(p.Deps, func(i, j int) bool {
		a, b := p.Deps[i], p.Deps[j]
		if a.Manifest != b.Manifest {
			return a.Manifest < b.Manifest
		}
		return a.Name < b.Name
	})
	rootDeps := map[string]bool{}
	for _, d := range p.Deps {
		if !strings.Contains(d.Manifest, "/") {
			rootDeps[d.Name] = true
		}
	}
	has := func(f string) bool { _, err := os.Stat(filepath.Join(dir, f)); return err == nil }
	anyDep := func(names []string) bool {
		return slices.ContainsFunc(names, func(n string) bool { return rootDeps[n] })
	}

	if root["package.json"] {
		p.Types = append(p.Types, "node")
		switch {
		case has("pnpm-lock.yaml"):
			p.PM, p.Runner, p.Setup = "pnpm", "pnpm", "pnpm install --frozen-lockfile"
		case has("yarn.lock"):
			p.PM, p.Runner, p.Setup = "yarn", "yarn", "yarn install --frozen-lockfile"
		case has("bun.lock") || has("bun.lockb"):
			p.PM, p.Runner, p.Setup = "bun", "bun", "bun install --frozen-lockfile"
		default:
			p.PM, p.Runner, p.Setup = "npm", "npm run", "npm ci"
		}
		p.NodeVersion = nodeVersion(dir)
	}
	if root["pyproject.toml"] {
		p.Types = append(p.Types, "python")
		if p.PM == "" {
			p.PM = "pip"
			if has("uv.lock") {
				p.PM, p.Runner, p.Setup = "uv", "uv run poe", "uv sync --frozen"
			}
		}
	}
	if root["go.mod"] {
		p.Types = append(p.Types, "go")
		if p.PM == "" {
			p.PM, p.Runner = "go", "make"
		}
	}
	if root["Cargo.toml"] {
		p.Types = append(p.Types, "rust")
		if p.PM == "" {
			p.PM, p.Runner = "cargo", "make"
		}
	}
	if anyDep(webDeps) {
		p.Types = append(p.Types, "web")
	}
	if p.Spec = findSpec(dir); p.Spec != "" {
		p.Types = append(p.Types, "api")
	}
	if has("migrations") || anyDep(dbDeps) || envHas(filepath.Join(dir, ".env.example"), "DATABASE_URL") {
		p.Types = append(p.Types, "database")
	}
	if anyDep(modelDeps) {
		p.Types = append(p.Types, "model")
	}
	if has("android") || has("ios") || anyDep(mobileDeps) {
		p.Types = append(p.Types, "mobile")
	}
	p.GitHub.Owner, p.GitHub.Repo = githubRemote(dir)
	return p, nil
}

// findManifests returns the manifests to depth 3 (slash paths), skipping the pruned directories.
func findManifests(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && (pruned[d.Name()] || strings.Count(rel, "/") >= 2) {
				return filepath.SkipDir
			}
			return nil
		}
		if manifestNames[d.Name()] {
			out = append(out, rel)
		}
		return nil
	})
	return out, err
}

func readDeps(dir, manifest string) ([]Dep, error) {
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(manifest)))
	if err != nil {
		return nil, err
	}
	base := filepath.Join(dir, filepath.FromSlash(filepath.ToSlash(filepath.Dir(manifest))))
	switch filepath.Base(manifest) {
	case "go.mod":
		return goDeps(b, manifest), nil
	case "package.json":
		return npmDeps(b, base, manifest), nil
	case "pyproject.toml":
		return withLock(pyDeps(b), filepath.Join(base, "uv.lock"), "pypi", manifest), nil
	default:
		return withLock(cargoDeps(b), filepath.Join(base, "Cargo.lock"), "cargo", manifest), nil
	}
}

// goDeps reads the go directive and the direct requirements, as hooks/docs.sh does.
func goDeps(b []byte, manifest string) []Dep {
	var out []Dep
	add := func(name, v string) {
		out = append(out, Dep{Name: name, Version: v, Ecosystem: "go", Manifest: manifest})
	}
	inReq := false
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		f := strings.Fields(line)
		switch {
		case len(f) == 2 && f[0] == "go":
			add("go", f[1])
		case line == "require (":
			inReq = true
		case inReq && line == ")":
			inReq = false
		case strings.Contains(line, "// indirect"):
		case inReq && len(f) >= 2:
			add(f[0], f[1])
		case len(f) >= 3 && f[0] == "require":
			add(f[1], f[2])
		}
	}
	return out
}

// npmDeps reads dependencies and devDependencies; the version comes from package-lock.json, then
// node_modules/<name>/package.json, then the range.
func npmDeps(b []byte, base, manifest string) []Dep {
	var pj struct{ Dependencies, DevDependencies map[string]string }
	if json.Unmarshal(b, &pj) != nil {
		return nil
	}
	var lock struct {
		Packages map[string]struct{ Version string } `json:"packages"`
	}
	if lb, err := os.ReadFile(filepath.Join(base, "package-lock.json")); err == nil {
		_ = json.Unmarshal(lb, &lock)
	}
	var out []Dep
	for _, m := range []map[string]string{pj.Dependencies, pj.DevDependencies} {
		for name, rng := range m {
			v := lock.Packages["node_modules/"+name].Version
			if v == "" {
				var inst struct{ Version string }
				if ib, err := os.ReadFile(filepath.Join(base, "node_modules", filepath.FromSlash(name), "package.json")); err == nil {
					_ = json.Unmarshal(ib, &inst)
				}
				v = inst.Version
			}
			if v == "" {
				v = stripRange(rng)
			}
			out = append(out, Dep{Name: name, Version: v, Ecosystem: "npm", Manifest: manifest})
		}
	}
	return out
}

var (
	pyReq    = regexp.MustCompile(`^\s*"([A-Za-z0-9_.-]+)(?:\[[^\]]*\])?\s*([^";]*)`)
	tomlKV   = regexp.MustCompile(`^\s*([A-Za-z0-9_-]+)\s*=\s*(.*)$`)
	tomlVer  = regexp.MustCompile(`version\s*=\s*"([^"]*)"`)
	quoted   = regexp.MustCompile(`^"([^"]*)"`)
	lockName = regexp.MustCompile(`^name = "([^"]+)"`)
	lockVer  = regexp.MustCompile(`^version = "([^"]+)"`)
)

// pyDeps reads [project] dependencies, one requirement per quoted string, line by line.
func pyDeps(b []byte) map[string]string {
	out := map[string]string{}
	section, inDeps := "", false
	for _, line := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") && !inDeps {
			section = t
			continue
		}
		if section != "[project]" {
			continue
		}
		if strings.HasPrefix(t, "dependencies") && strings.Contains(t, "[") {
			inDeps = true
			t = t[strings.Index(t, "[")+1:]
		}
		if !inDeps {
			continue
		}
		for _, part := range strings.Split(t, ",") {
			if m := pyReq.FindStringSubmatch(part); m != nil {
				out[strings.ToLower(m[1])] = stripRange(strings.TrimSpace(m[2]))
			}
		}
		if strings.Contains(t, "]") && !strings.Contains(t, "[") {
			inDeps = false
		}
	}
	return out
}

// cargoDeps reads [dependencies]: name = "1" or name = { version = "1", ... }.
func cargoDeps(b []byte) map[string]string {
	out := map[string]string{}
	section := ""
	for _, line := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") {
			section = t
			continue
		}
		if section != "[dependencies]" {
			continue
		}
		m := tomlKV.FindStringSubmatch(t)
		if m == nil {
			continue
		}
		v := ""
		if q := quoted.FindStringSubmatch(m[2]); q != nil {
			v = q[1]
		} else if q := tomlVer.FindStringSubmatch(m[2]); q != nil {
			v = q[1]
		}
		out[m[1]] = stripRange(v)
	}
	return out
}

// withLock turns name -> range into Deps, taking exact versions from a uv.lock or Cargo.lock.
func withLock(deps map[string]string, lockPath, eco, manifest string) []Dep {
	locked := map[string]string{}
	if b, err := os.ReadFile(lockPath); err == nil {
		name := ""
		for _, line := range strings.Split(string(b), "\n") {
			if m := lockName.FindStringSubmatch(line); m != nil {
				name = strings.ToLower(m[1])
			} else if m := lockVer.FindStringSubmatch(line); m != nil && name != "" {
				if _, ok := locked[name]; !ok {
					locked[name] = m[1]
				}
				name = ""
			}
		}
	}
	var out []Dep
	for name, v := range deps {
		if l := locked[strings.ToLower(name)]; l != "" {
			v = l
		}
		out = append(out, Dep{Name: name, Version: v, Ecosystem: eco, Manifest: manifest})
	}
	return out
}

// stripRange drops a version range's operator: "^4.1.12" -> "4.1.12", ">=0.28,<1" -> "0.28".
func stripRange(r string) string {
	r = strings.TrimSpace(strings.SplitN(r, ",", 2)[0])
	return strings.TrimLeft(r, "^~>=<!= v")
}

func nodeVersion(dir string) string {
	for _, f := range []string{".nvmrc", ".node-version"} {
		if b, err := os.ReadFile(filepath.Join(dir, f)); err == nil {
			if v := strings.TrimPrefix(strings.TrimSpace(string(b)), "v"); v != "" {
				return v
			}
		}
	}
	return "24"
}

// findSpec returns the first OpenAPI file at the root, else to directory depth 2.
func findSpec(dir string) string {
	isSpec := func(n string) bool {
		return n == "openapi.yaml" || n == "openapi.yml" || n == "openapi.json"
	}
	found := ""
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || found != "" {
			return filepath.SkipAll
		}
		rel, _ := filepath.Rel(dir, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && (pruned[d.Name()] || strings.Count(rel, "/") >= 2) {
				return filepath.SkipDir
			}
			return nil
		}
		if isSpec(d.Name()) {
			found = rel
		}
		return nil
	})
	return found
}

func envHas(path, key string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), key+"=") {
			return true
		}
	}
	return false
}

var githubURL = regexp.MustCompile(`github\.com[:/]([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+?)(?:\.git)?/?$`)

// githubRemote reads origin's URL from .git/config.
func githubRemote(dir string) (owner, repo string) {
	b, err := os.ReadFile(filepath.Join(dir, ".git", "config"))
	if err != nil {
		return "", ""
	}
	inOrigin := false
	for _, line := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") {
			inOrigin = t == `[remote "origin"]`
			continue
		}
		if k, v, ok := strings.Cut(t, "="); inOrigin && ok && strings.TrimSpace(k) == "url" {
			if m := githubURL.FindStringSubmatch(strings.TrimSpace(v)); m != nil {
				return m[1], m[2]
			}
		}
	}
	return "", ""
}
