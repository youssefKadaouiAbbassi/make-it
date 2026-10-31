package initrepo

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"makeit/internal/catalog"
)

func embeddedReq(t *testing.T) catalog.Requires {
	t.Helper()
	req, err := catalog.LoadRequires(catalog.Embedded)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

const gateRef = "o/makeit@0123456789abcdef0123456789abcdef01234567 # v1.0.0"

// seedsFor detects dir and returns its seeds with ci.gateRef set to ref.
func seedsFor(t *testing.T, dir, ref string) ([]Seed, []MergeSeed, SeedReport) {
	t.Helper()
	p, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	req := embeddedReq(t)
	req.CI.GateRef = ref
	seeds, merges, rep, err := Seeds(p, catalog.Embedded, req, func(string) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	return seeds, merges, rep
}

func seedBody(t *testing.T, seeds []Seed, path string) string {
	t.Helper()
	for _, s := range seeds {
		if s.Path == path {
			return string(s.Body)
		}
	}
	return ""
}

func merged(t *testing.T, dir string, merges []MergeSeed, path string) Merged {
	t.Helper()
	for _, m := range merges {
		if m.Path == path {
			cur, _ := os.ReadFile(filepath.Join(dir, path))
			res, err := m.Apply(cur, nil)
			if err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			return res
		}
	}
	t.Fatalf("no merge for %s", path)
	return Merged{}
}

func hasMerge(merges []MergeSeed, path string) bool {
	return slices.ContainsFunc(merges, func(m MergeSeed) bool { return m.Path == path })
}

func manualHas(rep SeedReport, sub string) bool {
	return slices.ContainsFunc(rep.Manual, func(m string) bool { return strings.Contains(m, sub) })
}

func TestSeedsChecks(t *testing.T) {
	dir := tree(t, map[string]string{"package.json": "{\n  \"name\": \"a\"\n}\n"})
	_, merges, _ := seedsFor(t, dir, "")
	body := string(merged(t, dir, merges, "package.json").Body)
	for _, s := range checkStubs {
		if !strings.Contains(body, `"`+s[0]+`"`) {
			t.Errorf("package.json lacks %s:\n%s", s[0], body)
		}
	}
	if !strings.Contains(body, `"check:full": "echo \"check:full: wire the linter, type checker and tests\" && exit 1"`) {
		t.Errorf("stub body:\n%s", body)
	}

	_, merges, rep := seedsFor(t, tree(t, map[string]string{"go.mod": "module x\n"}), "")
	if hasMerge(merges, "package.json") {
		t.Error("a Go project got a package.json")
	}
	if !manualHas(rep, ".PHONY: check\\:fast check\\:task check\\:full") || !manualHas(rep, "check\\:full:\n\t@echo") {
		t.Errorf("Makefile lines: %q", rep.Manual)
	}
	dir = tree(t, map[string]string{"package.json": "{}", "migrations/": ""})
	_, merges, _ = seedsFor(t, dir, "")
	if body := string(merged(t, dir, merges, "package.json").Body); !strings.Contains(body, `"check:drift": "echo \"check:drift: wire the schema drift check (migrations against the database)\" && exit 1"`) {
		t.Errorf("database: no check:drift stub\n%s", body)
	}
	if _, _, rep := seedsFor(t, tree(t, map[string]string{"go.mod": "module x\n", "migrations/": ""}), ""); !manualHas(rep, "check\\:drift:\n\t@echo") || !manualHas(rep, ".PHONY: check\\:fast check\\:task check\\:full check\\:drift") {
		t.Errorf("database Makefile lines: %q", rep.Manual)
	}
	_, _, rep = seedsFor(t, tree(t, map[string]string{"pyproject.toml": "[project]\nname = \"x\"\n", "uv.lock": ""}), "")
	if !manualHas(rep, "[tool.poe.tasks]") || !manualHas(rep, `"check:full" = { shell = `) || !manualHas(rep, "uv add --dev poethepoet") {
		t.Errorf("poe lines: %q", rep.Manual)
	}
}

func TestSeedsPreCommit(t *testing.T) {
	betterleaks := "  - repo: https://github.com/betterleaks/betterleaks\n    rev: 81aff7a638638aae3a659845d089043e1d8fe9ac # frozen: v1.9.0\n    hooks:\n      - id: betterleaks\n"
	for _, c := range []struct {
		name  string
		files map[string]string
		want  []string
		not   []string
	}{
		{"python", map[string]string{"pyproject.toml": "[project]\n", "uv.lock": ""},
			[]string{"https://github.com/astral-sh/ruff-pre-commit", "rev: 25bcf95026b96e2dd3c0aec3090c3d6ef6ca2e6c # frozen: v0.17.3", "- id: ruff-check", "- id: ruff-format"}, nil},
		{"go", map[string]string{"go.mod": "module x\n"},
			[]string{"entry: gofmt -l -w", "entry: go run honnef.co/go/tools/cmd/staticcheck@v0.8.2 ./...", "pass_filenames: false"}, nil},
		{"rust", map[string]string{"Cargo.toml": "[package]\n"},
			[]string{"entry: cargo fmt --all", "entry: cargo clippy --all-targets --all-features -- -D warnings"}, nil},
		{"node biome", map[string]string{"package.json": "{}", "biome.json": "{}"},
			[]string{"https://github.com/biomejs/pre-commit", "rev: eb45ebf3cb7c3a872b3ff0814883e8ab81fc451f # frozen: v2.6.4", "- id: biome-check"}, []string{"eslint"}},
		{"node eslint and prettier", map[string]string{"package.json": `{"devDependencies": {"prettier": "3.6.2"}}`, "eslint.config.js": ""},
			[]string{"entry: npx --no -- eslint --max-warnings=0 --no-warn-ignored", "entry: npx --no -- prettier --write --ignore-unknown"}, []string{"biome"}},
		{"node eslint without prettier", map[string]string{"package.json": "{}", "eslint.config.js": ""},
			[]string{"id: eslint"}, []string{"prettier"}},
	} {
		seeds, _, _ := seedsFor(t, tree(t, c.files), "")
		body := seedBody(t, seeds, ".pre-commit-config.yaml")
		if !strings.HasPrefix(body, "# Git hooks, run by prek (https://github.com/j178/prek). Enable once per clone: prek install\nupdate:\n  freeze: true\nrepos:\n") || !strings.HasSuffix(body, betterleaks) {
			t.Errorf("%s: frame\n%s", c.name, body)
		}
		for _, w := range c.want {
			if !strings.Contains(body, w) {
				t.Errorf("%s: lacks %q\n%s", c.name, w, body)
			}
		}
		for _, n := range c.not {
			if strings.Contains(body, n) {
				t.Errorf("%s: has %q\n%s", c.name, n, body)
			}
		}
	}
	seeds, _, rep := seedsFor(t, tree(t, map[string]string{"package.json": "{}"}), "")
	if body := seedBody(t, seeds, ".pre-commit-config.yaml"); strings.Contains(body, "biome") || !strings.Contains(body, "betterleaks") {
		t.Errorf("node with no lint config:\n%s", body)
	}
	// The manual line is exact text: both hook blocks, and the command that adds one later.
	if !manualHas(rep, biomeHooks) || !manualHas(rep, eslintHook) || !manualHas(rep, "makeit init --update") {
		t.Errorf("node with no lint config: manual %q", rep.Manual)
	}
	for _, existing := range []string{"prek.toml", ".pre-commit-config.yaml", ".pre-commit-config.yml"} {
		seeds, _, rep := seedsFor(t, tree(t, map[string]string{"go.mod": "module x\n", existing: ""}), "")
		if seedBody(t, seeds, ".pre-commit-config.yaml") != "" || !manualHas(rep, existing) || !manualHas(rep, "betterleaks") {
			t.Errorf("%s present: seeded anyway or no manual line %q", existing, rep.Manual)
		}
	}
}

func TestSeedsEnvAndSettings(t *testing.T) {
	seeds, merges, _ := seedsFor(t, tree(t, map[string]string{"go.mod": "module x\n", "migrations/": ""}), "")
	env := seedBody(t, seeds, ".env.example")
	if !strings.HasPrefix(env, "# Environment for ") || !strings.Contains(env, "\nDBHUB_DSN=\n") {
		t.Errorf(".env.example:\n%s", env)
	}
	if hasMerge(merges, ".claude/settings.json") {
		t.Error("settings merged for a project with no web frontend")
	}
	seeds, _, rep := seedsFor(t, tree(t, map[string]string{"go.mod": "module x\n", "migrations/": "", ".env.example": "A=1\n"}), "")
	if seedBody(t, seeds, ".env.example") != "" || !manualHas(rep, "DBHUB_DSN=") {
		t.Errorf("existing .env.example: %q", rep.Manual)
	}
	dir := tree(t, map[string]string{"package.json": `{"dependencies": {"react": "19.2.0"}}`})
	_, merges, _ = seedsFor(t, dir, "")
	if body := string(merged(t, dir, merges, ".claude/settings.json").Body); !strings.Contains(body, `"makeit-ui@makeit": true`) || strings.Contains(body, "$comment") {
		t.Errorf("web settings:\n%s", body)
	}
}

func TestSeedsCI(t *testing.T) {
	seeds, _, rep := seedsFor(t, tree(t, map[string]string{"package.json": "{}", "package-lock.json": "{}"}), "")
	for _, s := range seeds {
		if s.Class == "ci" && s.Path != ".grype.yaml" {
			t.Errorf("seeded %s with an empty gateRef", s.Path)
		}
	}
	if !slices.Contains(rep.Notices, "the gate caller needs a released makeit (catalog ci.gateRef is empty); init adds it when run again after an update") {
		t.Errorf("notices %q", rep.Notices)
	}
	if body := seedBody(t, seeds, ".grype.yaml"); !strings.Contains(body, "fail-on-severity: high") {
		t.Errorf(".grype.yaml:\n%s", body)
	}

	for _, c := range []struct {
		name  string
		files map[string]string
		with  []string
	}{
		{"npm", map[string]string{"package.json": "{}", "package-lock.json": "{}", ".nvmrc": "22\n"},
			[]string{`runner: "npm run"`, `setup: "npm ci"`, `node-version: "22"`, "has-web-ui: false", "has-openapi: false"}},
		{"pnpm", map[string]string{"package.json": `{"packageManager": "pnpm@9.15.0"}`, "pnpm-lock.yaml": ""},
			[]string{`runner: "pnpm"`, `setup: "npm install -g pnpm@9.15.0 && pnpm install --frozen-lockfile"`}},
		{"uv", map[string]string{"pyproject.toml": "[project]\n", "uv.lock": ""},
			[]string{`runner: "uv run poe"`, `setup: "pipx install uv==0.13.6 && uv sync --frozen"`, `node-version: "24"`}},
		{"go", map[string]string{"go.mod": "module x\n"}, []string{`runner: "make"`, `setup: ""`}},
		{"web with e2e and a spec", map[string]string{"package.json": `{"scripts": {"test:e2e": "playwright test"}, "dependencies": {"react": "19.2.0"}}`, "package-lock.json": "{}", "openapi.yaml": "openapi: 3.1.0\n"},
			[]string{"has-web-ui: true", "has-openapi: true"}},
	} {
		dir := tree(t, c.files)
		seeds, _, rep := seedsFor(t, dir, gateRef)
		caller := seedBody(t, seeds, ".github/workflows/gate.yml")
		if !strings.Contains(caller, "    uses: o/makeit/.github/workflows/gate.yml@0123456789abcdef0123456789abcdef01234567 # v1.0.0\n") ||
			!strings.Contains(caller, "    branches: [main]\n") || strings.Contains(caller, "Copy to <repo>") || !strings.HasPrefix(caller, "# The stack's gate, written by makeit init.") {
			t.Errorf("%s caller:\n%s", c.name, caller)
		}
		for _, w := range c.with {
			if !strings.Contains(caller, "      "+w+"\n") {
				t.Errorf("%s: caller lacks %q\n%s", c.name, w, caller)
			}
		}
		if !strings.Contains(caller, "    types: [opened, synchronize, reopened, edited]\n") {
			t.Errorf("%s: the caller does not re-run on an edited PR\n%s", c.name, caller)
		}
		if !slices.ContainsFunc(rep.Notices, func(n string) bool { return strings.Contains(n, "floor-guard: <reason>") }) {
			t.Errorf("%s: no notice about the floor-guard reason: %q", c.name, rep.Notices)
		}
		guard, _ := catalog.Embedded.ReadFile("ci/floor-guard.mjs")
		if seedBody(t, seeds, "ci/floor-guard.mjs") != string(guard) {
			t.Errorf("%s: floor-guard.mjs not a byte copy", c.name)
		}
		if web := strings.Contains(c.name, "web"); web != (seedBody(t, seeds, "ci/budget.json") != "") {
			t.Errorf("%s: budget.json seeded %v", c.name, !web)
		}
	}
	seeds, _, rep = seedsFor(t, tree(t, map[string]string{"package.json": `{"dependencies": {"react": "19.2.0"}}`, "package-lock.json": "{}",
		".github/workflows/gate.yml": "name: theirs\n", ".git/refs/remotes/origin/HEAD": "ref: refs/remotes/origin/trunk\n", ".git/config": ""}), gateRef)
	caller := seedBody(t, seeds, ".github/workflows/makeit-gate.yml")
	if caller == "" || seedBody(t, seeds, ".github/workflows/gate.yml") != "" || !strings.Contains(caller, "branches: [trunk]") {
		t.Errorf("their gate.yml: caller at makeit-gate.yml on trunk\n%s", caller)
	}
	if !manualHas(rep, "has-web-ui: true") {
		t.Errorf("web without test:e2e: %q", rep.Manual)
	}
	if _, _, rep := seedsFor(t, tree(t, map[string]string{"go.mod": "module x\n", "migrations/": ""}), gateRef); !manualHas(rep, "set has-migrations: true once check:drift exists") {
		t.Errorf("database: %q", rep.Manual)
	}
	for _, c := range []struct {
		name, why string
		files     map[string]string
	}{
		{"bun", "bun", map[string]string{"package.json": "{}", "bun.lock": ""}},
		{"pip", "pip", map[string]string{"pyproject.toml": "[project]\n"}},
	} {
		seeds, _, rep := seedsFor(t, tree(t, c.files), gateRef)
		if seedBody(t, seeds, ".github/workflows/gate.yml") != "" || !manualHas(rep, c.why) {
			t.Errorf("%s: caller seeded or no manual line %q", c.name, rep.Manual)
		}
	}
	if _, _, rep := seedsFor(t, tree(t, map[string]string{"package.json": "{}"}), gateRef); !manualHas(rep, "package-lock.json") {
		t.Errorf("npm without a lockfile: %q", rep.Manual)
	}
	if _, _, rep := seedsFor(t, tree(t, map[string]string{"go.mod": "module x\n"}), "o/makeit@main"); !slices.ContainsFunc(rep.Notices, func(n string) bool { return strings.Contains(n, "is not owner/repo@<40-hex sha> # <tag>") }) {
		t.Errorf("a malformed gateRef: %q", rep.Notices)
	}
}

func TestSeedsRenovate(t *testing.T) {
	dir := tree(t, map[string]string{"go.mod": "module x\n"})
	_, merges, _ := seedsFor(t, dir, "")
	body := string(merged(t, dir, merges, "renovate.json").Body)
	for _, w := range []string{"config:best-practices", ":enablePreCommit", "npx-launched MCP servers", "GitHub Actions: pinned by sha"} {
		if !strings.Contains(body, w) {
			t.Errorf("renovate.json lacks %s", w)
		}
	}
	for _, n := range []string{"automerge", "pinDigestsDisabled", "marketplace"} {
		if strings.Contains(body, n) {
			t.Errorf("renovate.json has %s", n)
		}
	}
	for _, other := range []string{"renovate.json5", ".github/renovate.json", ".renovaterc", ".renovaterc.json"} {
		_, merges, rep := seedsFor(t, tree(t, map[string]string{"go.mod": "module x\n", other: "{}"}), "")
		if hasMerge(merges, "renovate.json") || !manualHas(rep, other) {
			t.Errorf("%s present: renovate.json merged anyway, or no manual line %q", other, rep.Manual)
		}
	}
}

func TestSeedsDBHub(t *testing.T) {
	dir := tree(t, map[string]string{"package.json": `{"dependencies": {"prisma": "6.0.0"}}`})
	seeds, merges, _ := seedsFor(t, dir, "")
	mcp := string(merged(t, dir, merges, ".mcp.json").Body)
	if !strings.Contains(mcp, `"args": [`) || !strings.Contains(mcp, `"@bytebase/dbhub@1.5.0"`) || !strings.Contains(mcp, `".dbhub.toml"`) || strings.Contains(mcp, `"env"`) {
		t.Errorf(".mcp.json:\n%s", mcp)
	}
	want, _ := catalog.Embedded.ReadFile("templates/project/dbhub.toml")
	if got := seedBody(t, seeds, ".dbhub.toml"); got != string(want) || strings.Contains(got, "postgres://") {
		t.Errorf(".dbhub.toml:\n%s", got)
	}
}

func TestSeedsCheckAPI(t *testing.T) {
	dir := tree(t, map[string]string{"package.json": "{}", "openapi.yaml": "openapi: 3.1.0\n", ".git/config": "", ".git/refs/remotes/origin/HEAD": "ref: refs/remotes/origin/trunk\n"})
	_, merges, rep := seedsFor(t, dir, "")
	body := string(merged(t, dir, merges, "package.json").Body)
	for _, w := range []string{
		`REDOCLY_TELEMETRY=off REDOCLY_SUPPRESS_NOTIFICATIONS=true npx --yes @redocly/cli@2.72.1 lint openapi.yaml`,
		`tufin/oasdiff:v1.35.0@sha256:82b1840a77335562bb11ce95f77ad31745a0c6f3a770929333ed1c5ef466e400 breaking --fail-on ERR origin/trunk:./openapi.yaml openapi.yaml`,
		`git cat-file -e origin/trunk:./openapi.yaml`,
	} {
		if !strings.Contains(strings.ReplaceAll(body, `\"`, `"`), w) {
			t.Errorf("check:api lacks %q\n%s", w, body)
		}
	}
	if !slices.ContainsFunc(rep.Notices, func(n string) bool { return strings.Contains(n, "Docker") }) {
		t.Errorf("no Docker prerequisite notice: %q", rep.Notices)
	}
	_, _, rep = seedsFor(t, tree(t, map[string]string{"go.mod": "module x\n", "openapi.yaml": ""}), "")
	if !manualHas(rep, "check\\:api:") || !manualHas(rep, "go run github.com/oasdiff/oasdiff@v1.35.0 breaking --fail-on ERR") {
		t.Errorf("Go check:api lines: %q", rep.Manual)
	}
}

// In a monorepo subfolder GitHub and Renovate would never read a caller or a renovate.json: none
// is seeded, and a notice says they belong at the repository root.
func TestSeedsSubfolder(t *testing.T) {
	for _, git := range map[string]string{"dir": ".git/", "file (worktree)": ".git"} {
		top := tree(t, map[string]string{git: "", "pkg/a/package.json": `{"dependencies": {"react": "19.2.0"}}`, "pkg/a/package-lock.json": "{}"})
		seeds, merges, rep := seedsFor(t, filepath.Join(top, "pkg", "a"), gateRef)
		for _, p := range []string{".github/workflows/gate.yml", "ci/floor-guard.mjs", "ci/budget.json"} {
			if seedBody(t, seeds, p) != "" {
				t.Errorf("%s: %s seeded in a subfolder", git, p)
			}
		}
		if hasMerge(merges, "renovate.json") {
			t.Errorf("%s: renovate.json merged in a subfolder", git)
		}
		if !slices.ContainsFunc(rep.Notices, func(n string) bool { return strings.Contains(n, "belong at the repository root") }) {
			t.Errorf("%s: notices %q", git, rep.Notices)
		}
	}
	if _, merges, _ := seedsFor(t, tree(t, map[string]string{".git": "", "go.mod": "module x\n"}), gateRef); !hasMerge(merges, "renovate.json") {
		t.Error("a .git file at the init dir is the top level: no renovate.json")
	}
}

// The delegate's ruling: without Docker, or with its daemon down, check:api fails with one line
// naming the cause, not with a raw docker error.
func TestCheckAPINeedsDocker(t *testing.T) {
	script := checkAPI("openapi.yaml", "main")
	bin := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("npx", "exit 0\n")
	for _, c := range []struct{ name, docker string }{{"no docker", ""}, {"daemon down", `[ "$1" = info ] && { echo "Cannot connect to the Docker daemon" >&2; exit 1; }; exit 0` + "\n"}} {
		_ = os.Remove(filepath.Join(bin, "docker"))
		if c.docker != "" {
			write("docker", c.docker)
		}
		cmd := exec.Command("/bin/sh", "-c", script)
		cmd.Env = []string{"PATH=" + bin}
		out, err := cmd.CombinedOutput()
		if err == nil || strings.TrimSpace(string(out)) != "check:api needs Docker for oasdiff" {
			t.Errorf("%s: err %v, output %q", c.name, err, out)
		}
	}
}

// The Makefile check:api has checkAPI's guards: a missing Docker fails with one line, and a spec
// the default branch does not have yet passes, so the PR adding the first spec can merge.
func TestMakeAPIGuards(t *testing.T) {
	for _, types := range [][]string{{"go"}, {"rust"}} {
		// make turns $$ into $ before the shell sees the recipe.
		script := strings.ReplaceAll(makeAPI(Project{Types: types}, "openapi.yaml", "main"), "$$", "$")
		bin := t.TempDir()
		write := func(name, body string) {
			if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		sh := func() (string, error) {
			cmd := exec.Command("/bin/sh", "-c", script)
			cmd.Env = []string{"PATH=" + bin}
			out, err := cmd.CombinedOutput()
			return strings.TrimSpace(string(out)), err
		}
		write("go", "echo oasdiff \"$@\"\n")
		write("git", `[ "$1" = cat-file ] && exit 1; echo /g`+"\n")
		if out, err := sh(); err == nil || !strings.HasPrefix(out, "check:api needs Docker for Redocly") {
			t.Errorf("%v no docker: err %v, output %q", types, err, out)
		}
		write("docker", "exit 0\n")
		if out, err := sh(); err != nil || out != "check:api: openapi.yaml is not on origin/main yet, nothing to compare" {
			t.Errorf("%v spec not on origin: err %v, output %q", types, err, out)
		}
		write("git", `[ "$1" = rev-parse ] && echo /g; exit 0`+"\n")
		write("docker", `[ "$1" = run ] && case "$*" in *oasdiff*) echo oasdiff;; esac; exit 0`+"\n")
		if out, err := sh(); err != nil || !strings.HasPrefix(out, "oasdiff") {
			t.Errorf("%v spec on origin: err %v, output %q", types, err, out)
		}
	}
}
