// Package initrepo is `makeit init`: it detects a project, seeds the stack's files into it and
// records what it wrote in .makeit.json (cli/INIT.md).
package initrepo

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"makeit/internal/fsx"
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
	// Repos are the repository children of a workspace (a folder outside git whose direct children
	// hold their own .git directory), sorted; Worktrees counts the worktree children skipped (their
	// .git is a file). Both are empty outside a workspace.
	Repos     []string
	Worktrees int
	// Deps are the direct dependencies of every manifest to depth 3, sorted by manifest, then name.
	Deps   []Dep
	GitHub struct{ Owner, Repo string }
	// DefaultBranch is origin's HEAD, or main when the clone does not record it.
	DefaultBranch string
	// Scripts are the root package.json's script names.
	Scripts map[string]bool
	// Lint is the root's linter setup: "biome" (a biome config, which wins), "eslint" (an eslint
	// config) or "". Prettier is prettier among the root dependencies.
	Lint     string
	Prettier bool
	// Lockfile is a committed package-lock.json, which npm ci needs.
	Lockfile bool
	// Manifests maps each manifest and lockfile Detect read (slash path) to the sha256 of the
	// bytes it read, so a docs sync records what it actually indexed.
	Manifests map[string]string
}

// lockfiles beside a manifest that decide its versions.
var lockfiles = map[string]string{"package.json": "package-lock.json", "pyproject.toml": "uv.lock", "Cargo.toml": "Cargo.lock"}

// ManifestHashes is what Project.Manifests would be for dir now, without parsing anything.
func ManifestHashes(dir string) (map[string]string, error) {
	repos, _ := workspaceChildren(dir)
	manifests, err := findManifests(dir, repos)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, m := range manifests {
		for _, rel := range []string{m, siblingLock(m)} {
			if rel == "" {
				continue
			}
			if b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel))); err == nil {
				out[rel] = fsx.Sha256(b)
			}
		}
	}
	return out, nil
}

// siblingLock is the lockfile next to manifest m, or "" for go.mod.
func siblingLock(m string) string {
	l, ok := lockfiles[path.Base(m)]
	if !ok {
		return ""
	}
	if d := path.Dir(m); d != "." {
		return d + "/" + l
	}
	return l
}

// pruned are directories no manifest or spec is looked for in (docs.sh prunes the same).
var pruned = map[string]bool{"node_modules": true, "vendor": true, ".git": true, "target": true, ".venv": true}

// manifestNames are the files Detect reads dependencies from.
var manifestNames = map[string]bool{"go.mod": true, "package.json": true, "pyproject.toml": true, "Cargo.toml": true, "requirements.txt": true}

var (
	webDeps    = []string{"react", "vue", "svelte", "solid-js", "next", "nuxt", "@sveltejs/kit", "astro", "vite"}
	dbDeps     = []string{"prisma", "@prisma/client", "drizzle-orm", "knex", "typeorm", "sequelize", "kysely", "sqlalchemy", "psycopg", "psycopg2", "asyncpg", "gorm.io/gorm", "github.com/jmoiron/sqlx", "github.com/jackc/pgx/v5", "diesel", "sqlx", "sea-orm"}
	modelDeps  = []string{"@anthropic-ai/sdk", "ai", "openai", "anthropic", "pydantic-ai", "instructor"}
	mobileDeps = []string{"expo", "react-native"}
)

// Detect reads dir's manifests, lockfiles and layout. A directory with no manifest gives a
// Project with no types.
func Detect(dir string) (Project, error) {
	p := Project{Dir: dir, Manifests: map[string]string{}, Scripts: map[string]bool{}}
	p.Repos, p.Worktrees = workspaceChildren(dir)
	manifests, err := findManifests(dir, p.Repos)
	if err != nil {
		return p, err
	}
	root := map[string]bool{}
	for _, m := range manifests {
		deps, err := readDeps(dir, m, p.Manifests)
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
		// requirements.txt feeds the docs only; the types stay those of the other manifests.
		if !strings.Contains(d.Manifest, "/") && d.Manifest != "requirements.txt" {
			rootDeps[d.Name] = true
		}
	}
	has := func(f string) bool { _, err := os.Stat(filepath.Join(dir, f)); return err == nil }
	var rootPkg struct {
		PackageManager string            `json:"packageManager"`
		Scripts        map[string]string `json:"scripts"`
	}
	if b, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil {
		_ = json.Unmarshal(b, &rootPkg)
	}
	anyDep := func(names []string) bool {
		return slices.ContainsFunc(names, func(n string) bool { return rootDeps[n] })
	}

	if root["package.json"] {
		p.Types = append(p.Types, "node")
		switch {
		case has("pnpm-lock.yaml"):
			// The runner image has no pnpm; the version packageManager names wins.
			v := pnpmPin
			if pm, ver, ok := strings.Cut(rootPkg.PackageManager, "@"); ok && pm == "pnpm" {
				v, _, _ = strings.Cut(ver, "+")
			}
			p.PM, p.Runner, p.Setup = "pnpm", "pnpm", "npm install -g pnpm@"+v+" && pnpm install --frozen-lockfile"
		case has("yarn.lock") && has(".yarnrc.yml"):
			p.PM, p.Runner, p.Setup = "yarn", "yarn", "yarn install --immutable"
		case has("yarn.lock"):
			p.PM, p.Runner, p.Setup = "yarn", "yarn", "yarn install --frozen-lockfile"
		case has("bun.lock") || has("bun.lockb"):
			p.PM, p.Runner, p.Setup = "bun", "bun", "bun install --frozen-lockfile"
		default:
			p.PM, p.Runner, p.Setup = "npm", "npm run", "npm ci"
		}
		p.NodeVersion = nodeVersion(dir)
		p.Lockfile = has("package-lock.json")
		for name := range rootPkg.Scripts {
			p.Scripts[name] = true
		}
		switch {
		case has("biome.json") || has("biome.jsonc"):
			p.Lint = "biome"
		case anyFile(dir, eslintConfigs):
			p.Lint = "eslint"
		}
		p.Prettier = rootDeps["prettier"]
	}
	if root["pyproject.toml"] {
		p.Types = append(p.Types, "python")
		if p.PM == "" {
			p.PM = "pip"
			if has("uv.lock") {
				// The runner image has no uv; pipx puts it on PATH without sudo.
				p.PM, p.Runner, p.Setup = "uv", "uv run poe", "pipx install uv=="+uvPin+" && uv sync --frozen"
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
	gitDir := findGitDir(dir)
	p.GitHub.Owner, p.GitHub.Repo = githubRemote(gitDir)
	p.DefaultBranch = defaultBranch(gitDir)
	return p, nil
}

// pnpmPin and uvPin bootstrap the package managers the runner image lacks.
const (
	pnpmPin = "12.16.1"
	uvPin   = "0.13.6"
)

var eslintConfigs = []string{"eslint.config.js", "eslint.config.mjs", "eslint.config.cjs", "eslint.config.ts", "eslint.config.mts", "eslint.config.cts",
	".eslintrc", ".eslintrc.js", ".eslintrc.cjs", ".eslintrc.json", ".eslintrc.yml", ".eslintrc.yaml"}

func anyFile(dir string, names []string) bool {
	return slices.ContainsFunc(names, func(n string) bool { _, err := os.Stat(filepath.Join(dir, n)); return err == nil })
}

// findGitDir is the git directory holding config and refs for dir or the nearest parent with a
// .git, or "". A .git file (a submodule, a linked worktree) names the git directory in its
// "gitdir:" line, relative to the file's directory; a "commondir" there names the directory with
// the shared config and refs, relative to the git directory, as git reads them.
func findGitDir(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for d := abs; ; d = filepath.Dir(d) {
		dotGit := filepath.Join(d, ".git")
		if st, err := os.Stat(dotGit); err == nil && st.IsDir() {
			return dotGit
		} else if err == nil {
			b, err := os.ReadFile(dotGit)
			gd, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
			if err != nil || !ok {
				return ""
			}
			if gd = strings.TrimSpace(gd); !filepath.IsAbs(gd) {
				gd = filepath.Join(d, gd)
			}
			if c, err := os.ReadFile(filepath.Join(gd, "commondir")); err == nil {
				if cd := strings.TrimSpace(string(c)); filepath.IsAbs(cd) {
					gd = cd
				} else {
					gd = filepath.Join(gd, cd)
				}
			}
			return gd
		}
		if filepath.Dir(d) == d {
			return ""
		}
	}
}

// defaultBranch reads origin's HEAD as the clone recorded it; main when it did not.
func defaultBranch(gitDir string) string {
	if gitDir != "" {
		if b, err := os.ReadFile(filepath.Join(gitDir, "refs", "remotes", "origin", "HEAD")); err == nil {
			if ref, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "ref: refs/remotes/origin/"); ok && ref != "" {
				return ref
			}
		}
	}
	return "main"
}

// workspaceChildren are, for a dir outside any git work tree, the direct children holding their
// own .git directory, sorted, and the number of worktrees of another repository (a .git file
// pointing into its worktrees folder).
func workspaceChildren(dir string) (repos []string, worktrees int) {
	if abs, err := filepath.Abs(dir); err != nil || gitTop(abs) != "" {
		return nil, 0
	}
	entries, _ := os.ReadDir(dir)
	for _, en := range entries {
		if !en.IsDir() {
			continue
		}
		git := filepath.Join(dir, en.Name(), ".git")
		fi, err := os.Lstat(git)
		switch {
		case err != nil:
		case fi.IsDir():
			repos = append(repos, en.Name())
		case fi.Mode().IsRegular():
			// A worktree's .git points into another repository's worktrees folder.
			if b, err := os.ReadFile(git); err == nil && strings.Contains(string(b), "/worktrees/") {
				worktrees++
			}
		}
	}
	return repos, worktrees
}

// findManifests returns the manifests to depth 3 (slash paths), skipping the pruned directories
// and any directory below dir it cannot read (a database bind mount owned by another user); with
// repos (a workspace), a top-level folder is read only when it is one of them.
func findManifests(dir string, repos []string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == dir || d == nil || !d.IsDir() {
				return err
			}
			return filepath.SkipDir
		}
		rel, _ := filepath.Rel(dir, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && (pruned[d.Name()] || strings.Count(rel, "/") >= 2) {
				return filepath.SkipDir
			}
			if len(repos) > 0 && !strings.Contains(rel, "/") && rel != "." && !slices.Contains(repos, rel) {
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

// readDeps parses one manifest and its lockfile, recording the sha256 of each file it read.
func readDeps(dir, manifest string, read map[string]string) ([]Dep, error) {
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(manifest)))
	if err != nil {
		return nil, err
	}
	read[manifest] = fsx.Sha256(b)
	var lock []byte
	if l := siblingLock(manifest); l != "" {
		if lb, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(l))); err == nil {
			lock, read[l] = lb, fsx.Sha256(lb)
		}
	}
	base := filepath.Join(dir, filepath.FromSlash(path.Dir(manifest)))
	switch path.Base(manifest) {
	case "go.mod":
		return goDeps(b, manifest), nil
	case "package.json":
		return npmDeps(b, lock, base, manifest), nil
	case "pyproject.toml":
		return withLock(pyDeps(b), lock, "pypi", manifest), nil
	case "requirements.txt":
		return withLock(reqDeps(b), nil, "pypi", manifest), nil
	default:
		return withLock(cargoDeps(b), lock, "cargo", manifest), nil
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
func npmDeps(b, lockJSON []byte, base, manifest string) []Dep {
	var pj struct{ Dependencies, DevDependencies map[string]string }
	if json.Unmarshal(b, &pj) != nil {
		return nil
	}
	var lock struct {
		Packages map[string]struct{ Version string } `json:"packages"`
	}
	_ = json.Unmarshal(lockJSON, &lock)
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
	reqLine  = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9_.-]*)(?:\[[^\]]*\])?\s*((?:[=<>~!].*)?)$`)
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

// reqDeps reads requirement lines: "name==1.2.3" exact, other specifiers stripped. Comments,
// environment markers, options (-r, -e, --index-url), URLs, direct references and paths are
// skipped.
func reqDeps(b []byte) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		t, _, _ := strings.Cut(line, "#")
		t, _, _ = strings.Cut(t, ";")
		t = strings.TrimSpace(t)
		if t == "" || strings.HasPrefix(t, "-") || strings.Contains(t, "://") || strings.Contains(t, "@") {
			continue
		}
		if m := reqLine.FindStringSubmatch(t); m != nil {
			out[strings.ToLower(m[1])] = stripRange(m[2])
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
func withLock(deps map[string]string, b []byte, eco, manifest string) []Dep {
	locked := map[string]string{}
	if b != nil {
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
	return strings.TrimLeft(r, "^~>=<! v")
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

// githubRemote reads origin's URL from the git directory's config.
func githubRemote(gitDir string) (owner, repo string) {
	if gitDir == "" {
		return "", ""
	}
	b, err := os.ReadFile(filepath.Join(gitDir, "config"))
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
