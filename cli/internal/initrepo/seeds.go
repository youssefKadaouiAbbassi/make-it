package initrepo

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"makeit/internal/catalog"
	"makeit/internal/jsonx"
)

// MergeSeed is one file init merges into rather than writes: Apply returns theirs with what the
// stack adds, leaving out the entries in skip (added by an earlier run and since removed).
// Template names what the additions come from.
type MergeSeed struct {
	Path, Template string
	Apply          func(theirs []byte, skip map[string]bool) (Merged, error)
}

// SeedReport is what Seeds could not do itself: Manual lines for the agent step (exact text to
// add to files makeit does not edit) and Notices for the user.
type SeedReport struct {
	Manual  []string
	Notices []string
}

// Pins of the tools the seeded files run; the sources are in the init ledger
// (docs/superpowers/plans/2026-10-31-init-ledger.md).
const (
	redoclyPin    = "@redocly/cli@2.72.1"
	redoclyImage  = "redocly/cli:2.72.1@sha256:d39a229ebd8250fea62f44c377ddb04797e8a155140eda49db17f1caaa9334ff"
	oasdiffImage  = "tufin/oasdiff:v1.35.0@sha256:82b1840a77335562bb11ce95f77ad31745a0c6f3a770929333ed1c5ef466e400"
	oasdiffModule = "github.com/oasdiff/oasdiff@v1.35.0"
	dbhubPkg      = "@bytebase/dbhub@1.5.0"
)

// checkStubs are the check tasks a project gets when it has none; each fails and says what to
// wire, so a red check:full is honest until the agent step wires the real tools.
var checkStubs = [][2]string{
	{"check:fast", `echo "check:fast: wire the formatter, linter and type checker" && exit 1`},
	{"check:task", `echo "check:task: wire check:fast and the related tests" && exit 1`},
	{"check:full", `echo "check:full: wire the linter, type checker and tests" && exit 1`},
}

// envLines keep real environment files out of git.
var envLines = []string{".env", ".env.*", "!.env.example"}

// Seeds are the files init writes or merges for p beyond the docs (cli/INIT.md, CLI items 4-9).
// ours reports whether init wrote a path on an earlier run: a file that exists and is not ours
// is the user's, so its seed is left out and a Manual line says what to add instead.
func Seeds(p Project, src catalog.Source, req catalog.Requires, ours func(string) bool) ([]Seed, []MergeSeed, SeedReport, error) {
	s := &seeder{p: p, src: src, ours: ours}
	s.checks()
	if err := s.preCommit(); err != nil {
		return nil, nil, SeedReport{}, err
	}
	s.merges = append(s.merges, MergeSeed{Path: ".gitignore", Template: "makeit:env-lines",
		Apply: func(theirs []byte, skip map[string]bool) (Merged, error) {
			return MergeLines(theirs, envLines, skip), nil
		}})
	s.env()
	if err := s.settings(); err != nil {
		return nil, nil, SeedReport{}, err
	}
	if err := s.ci(req.CI.GateRef); err != nil {
		return nil, nil, SeedReport{}, err
	}
	if err := s.dbhub(); err != nil {
		return nil, nil, SeedReport{}, err
	}
	return s.seeds, s.merges, s.rep, nil
}

type seeder struct {
	p      Project
	src    catalog.Source
	ours   func(string) bool
	seeds  []Seed
	merges []MergeSeed
	rep    SeedReport
}

func (s *seeder) is(t string) bool { return slices.Contains(s.p.Types, t) }

func (s *seeder) manual(format string, a ...any) {
	s.rep.Manual = append(s.rep.Manual, fmt.Sprintf(format, a...))
}

// theirs is a file the repository has and init did not write.
func (s *seeder) theirs(rel string) bool {
	_, err := os.Stat(filepath.Join(s.p.Dir, filepath.FromSlash(rel)))
	return err == nil && !s.ours(rel)
}

func (s *seeder) copySeed(path, template, class string) error {
	b, err := s.src.ReadFile(template)
	if err != nil {
		return err
	}
	s.seeds = append(s.seeds, Seed{Path: path, Template: template, Class: class, Body: b})
	return nil
}

func (s *seeder) projectName() string {
	abs, err := filepath.Abs(s.p.Dir)
	if err != nil {
		return filepath.Base(s.p.Dir)
	}
	return filepath.Base(abs)
}

// driftStub is check:drift for a project with a database: the gate's migrations job runs it.
var driftStub = [2]string{"check:drift", `echo "check:drift: wire the schema drift check (migrations against the database)" && exit 1`}

// checks adds the check tasks: in package.json for node, as Manual lines for other runners.
func (s *seeder) checks() {
	api := s.is("api") && s.p.Spec != ""
	stubs := checkStubs
	if s.is("database") {
		stubs = append(slices.Clip(stubs), driftStub)
	}
	switch {
	case s.is("node"):
		scripts := stubs
		if api {
			scripts = append(slices.Clip(scripts), [2]string{"check:api", checkAPI(s.p.Spec, s.p.DefaultBranch)})
			s.rep.Notices = append(s.rep.Notices, "check:api runs oasdiff in Docker: Docker must be installed and running wherever check:api runs, locally and in CI")
		}
		s.merges = append(s.merges, MergeSeed{Path: "package.json", Template: "makeit:check-stubs",
			Apply: func(theirs []byte, skip map[string]bool) (Merged, error) {
				return PackageScripts(theirs, scripts, skip)
			}})
	case s.p.Runner == "make":
		phony := ".PHONY:"
		for _, st := range stubs {
			phony += " " + strings.ReplaceAll(st[0], ":", "\\:")
		}
		lines := []string{"Makefile: add these targets (make needs the colons escaped):", phony}
		for _, st := range stubs {
			lines = append(lines, strings.ReplaceAll(st[0], ":", "\\:")+":", "\t@"+st[1])
		}
		if api {
			lines = append(lines, "check\\:api:", "\t"+makeAPI(s.p, s.p.Spec, s.p.DefaultBranch))
		}
		s.manual("%s", strings.Join(lines, "\n"))
	case s.p.Runner == "uv run poe":
		lines := []string{"pyproject.toml: add poethepoet as a dev dependency (uv add --dev poethepoet) and these tasks:", "[tool.poe.tasks]"}
		for _, st := range stubs {
			lines = append(lines, strconv.Quote(st[0])+" = { shell = "+strconv.Quote(strings.ReplaceAll(st[1], `"`, `'`))+" }")
		}
		if api {
			lines = append(lines, `"check:api" = { shell = '`+checkAPI(s.p.Spec, s.p.DefaultBranch)+`' }`)
		}
		s.manual("%s", strings.Join(lines, "\n"))
	case len(s.p.Types) > 0 && s.p.Runner == "":
		s.manual("add check:fast, check:task and check:full tasks to the project's task runner; the CI checks call them by name")
	}
}

// checkAPI is check:api for an npm script: Redocly lint, then oasdiff (in Docker) against the
// default branch's copy of the spec. A spec the default branch does not have yet passes, and a
// missing Docker fails with one line naming it.
func checkAPI(spec, branch string) string {
	// git resolves "./" against the current directory, so a spec in a monorepo subfolder is found;
	// oasdiff's container gets the repository's top level and starts in the same directory under it.
	base := shq("origin/" + branch + ":./" + spec)
	return "REDOCLY_TELEMETRY=off REDOCLY_SUPPRESS_NOTIFICATIONS=true npx --yes " + redoclyPin + " lint " + shq(spec) +
		" && if ! docker info >/dev/null 2>&1; then echo \"check:api needs Docker for oasdiff\" >&2; exit 1; " +
		"elif git cat-file -e " + base + " 2>/dev/null; then T=$(git rev-parse --show-toplevel) && G=$(git rev-parse --path-format=absolute --git-common-dir) && " +
		"docker run --rm -v \"$T\":/repo:ro -v \"$G\":\"$G\":ro -w \"/repo/$(git rev-parse --show-prefix)\" -e GIT_CONFIG_COUNT=1 -e GIT_CONFIG_KEY_0=safe.directory -e GIT_CONFIG_VALUE_0=/repo " +
		oasdiffImage + " breaking --fail-on ERR " + base + " " + shq(spec) +
		"; else echo \"check:api: " + spec + " is not on origin/" + branch + " yet, nothing to compare\"; fi"
}

// makeAPI is check:api as one Makefile recipe line ($ doubled for make) with checkAPI's guards.
// Docker comes first for every project, Go included, because Redocly lints in Docker (so no Node
// is needed); oasdiff (go run for Go projects, Docker otherwise) runs only once the default
// branch has the spec.
func makeAPI(p Project, spec, branch string) string {
	lint := "docker run --rm -e REDOCLY_TELEMETRY=off -v \"$$PWD\":/spec " + redoclyImage + " lint " + shq(spec)
	base := shq("origin/" + branch + ":./" + spec)
	needs := "Redocly and oasdiff"
	diff := "T=$$(git rev-parse --show-toplevel) && G=$$(git rev-parse --path-format=absolute --git-common-dir) && " +
		"docker run --rm -v \"$$T\":/repo:ro -v \"$$G\":\"$$G\":ro -w \"/repo/$$(git rev-parse --show-prefix)\" " +
		"-e GIT_CONFIG_COUNT=1 -e GIT_CONFIG_KEY_0=safe.directory -e GIT_CONFIG_VALUE_0=/repo " + oasdiffImage + " breaking --fail-on ERR " + base + " " + shq(spec)
	if slices.Contains(p.Types, "go") {
		needs, diff = "Redocly", "go run "+oasdiffModule+" breaking --fail-on ERR "+base+" "+shq(spec)
	}
	return "if ! docker info >/dev/null 2>&1; then echo \"check:api needs Docker for " + needs + "\" >&2; exit 1; " +
		"elif git cat-file -e " + base + " 2>/dev/null; then " + lint + " && " + diff + "; " +
		"else " + lint + " && echo \"check:api: " + spec + " is not on origin/" + branch + " yet, nothing to compare\"; fi"
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9._/:@+-]+$`)

// shq quotes s for sh when it holds anything beyond a plain path.
func shq(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

const (
	preCommitHead = "# Git hooks, run by prek (https://github.com/j178/prek). Enable once per clone: prek install\nupdate:\n  freeze: true\nrepos:\n"
	betterleaks   = "  - repo: https://github.com/betterleaks/betterleaks\n    rev: 81aff7a638638aae3a659845d089043e1d8fe9ac # frozen: v1.9.0\n    hooks:\n      - id: betterleaks\n"
	ruffHooks     = "  - repo: https://github.com/astral-sh/ruff-pre-commit\n    rev: 25bcf95026b96e2dd3c0aec3090c3d6ef6ca2e6c # frozen: v0.17.3\n    hooks:\n      - id: ruff-check\n      - id: ruff-format\n"
	biomeHooks    = "  - repo: https://github.com/biomejs/pre-commit\n    rev: eb45ebf3cb7c3a872b3ff0814883e8ab81fc451f # frozen: v2.6.4\n    hooks:\n      - id: biome-check\n"
	goHooks       = "  - repo: local\n    hooks:\n      - id: gofmt\n        name: gofmt\n        language: system\n        entry: gofmt -l -w\n        types: [go]\n" +
		"      - id: staticcheck\n        name: staticcheck\n        language: system\n        entry: go run honnef.co/go/tools/cmd/staticcheck@v0.8.2 ./...\n        pass_filenames: false\n        types: [go]\n"
	rustHooks = "  - repo: local\n    hooks:\n      - id: cargo-fmt\n        name: cargo fmt\n        language: system\n        entry: cargo fmt --all\n        pass_filenames: false\n        types: [rust]\n" +
		"      - id: cargo-clippy\n        name: cargo clippy\n        language: system\n        entry: cargo clippy --all-targets --all-features -- -D warnings\n        pass_filenames: false\n        types: [rust]\n"
	prettierHook = "      - id: prettier\n        name: prettier\n        language: system\n        entry: npx --no -- prettier --write --ignore-unknown\n        types: [text]\n"
)

// eslintHook runs eslint on the staged files. --no-warn-ignored (no warning for a file the
// config ignores) exists only for a flat eslint.config.*: ESLint 8 with a legacy .eslintrc
// rejects it, and the hook would fail every commit.
func eslintHook(flat bool) string {
	entry := "npx --no -- eslint --max-warnings=0"
	if flat {
		entry += " --no-warn-ignored"
	}
	return "      - id: eslint\n        name: eslint\n        language: system\n        entry: " + entry + "\n        types_or: [javascript, jsx, ts, tsx]\n"
}

// flatESLint is a flat config at the root, the form ESLint 9 reads by default.
var flatESLint = []string{"eslint.config.js", "eslint.config.mjs", "eslint.config.cjs", "eslint.config.ts", "eslint.config.mts", "eslint.config.cts"}

// preCommit seeds .pre-commit-config.yaml for prek when the repository has no hook config: the
// language's formatter and linter, then betterleaks. A config of theirs gets a Manual line.
func (s *seeder) preCommit() error {
	for _, f := range []string{"prek.toml", ".pre-commit-config.yaml", ".pre-commit-config.yml"} {
		if s.theirs(f) {
			s.manual("%s exists, so makeit wrote no hook config: add the betterleaks hook to it (makeit does not edit YAML or TOML):\n%s", f, betterleaks)
			return nil
		}
	}
	var b strings.Builder
	b.WriteString(preCommitHead)
	if s.is("node") {
		switch s.p.Lint {
		case "biome":
			b.WriteString(biomeHooks)
		case "eslint":
			b.WriteString("  - repo: local\n    hooks:\n")
			if s.p.Prettier {
				b.WriteString(prettierHook)
			}
			b.WriteString(eslintHook(slices.ContainsFunc(flatESLint, func(f string) bool {
				_, err := os.Stat(filepath.Join(s.p.Dir, f))
				return err == nil
			})))
		default:
			s.manual(".pre-commit-config.yaml: once the project has a biome or an eslint config, run makeit init --update (it adds the matching hook while the file is unchanged), or add the block for it under repos: by hand.\nbiome:\n%s\neslint (with the prettier hook only when prettier is installed; without --no-warn-ignored for a legacy .eslintrc):\n  - repo: local\n    hooks:\n%s%s", biomeHooks, prettierHook, eslintHook(true))
		}
	}
	if s.is("python") {
		b.WriteString(ruffHooks)
	}
	if s.is("go") {
		b.WriteString(goHooks)
	}
	if s.is("rust") {
		b.WriteString(rustHooks)
	}
	b.WriteString(betterleaks)
	s.seeds = append(s.seeds, Seed{Path: ".pre-commit-config.yaml", Template: "makeit:pre-commit", Class: "settings", Body: []byte(b.String())})
	s.rep.Notices = append(s.rep.Notices, "enable the git hooks once per clone: prek install (it refuses while git's core.hooksPath is set)")
	return nil
}

// env seeds .env.example when the repository has none; with a database it names DBHUB_DSN.
func (s *seeder) env() {
	db := s.is("database")
	if s.theirs(".env.example") {
		if db {
			s.manual(".env.example: add the line DBHUB_DSN= (DBHub's connection string; the real value goes in .env)")
		}
		return
	}
	body := "# Environment for " + s.projectName() + ": copy to .env (gitignored, never committed) and fill in.\n"
	if db {
		body += "# DBHub (.dbhub.toml) reads its connection string from DBHUB_DSN; use a least-privilege user.\nDBHUB_DSN=\n"
	}
	s.seeds = append(s.seeds, Seed{Path: ".env.example", Template: "makeit:env-example", Class: "settings", Body: []byte(body)})
}

// settings adds project.settings.example.json's keys to .claude/settings.json for a web frontend
// (the only key it has today enables makeit-ui).
func (s *seeder) settings() error {
	if !s.is("web") {
		return nil
	}
	b, err := s.src.ReadFile("project.settings.example.json")
	if err != nil {
		return err
	}
	o, err := jsonx.DecodeObject(b)
	if err != nil {
		return err
	}
	o.Delete("$comment")
	ours, err := jsonx.Compact(o)
	if err != nil {
		return err
	}
	s.merges = append(s.merges, MergeSeed{Path: ".claude/settings.json", Template: "project.settings.example.json",
		Apply: func(theirs []byte, skip map[string]bool) (Merged, error) { return MergeJSON(theirs, ours, nil, skip) }})
	return nil
}

var gateRefPattern = regexp.MustCompile(`^([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)@([0-9a-f]{40}) # (\S+)$`)

// renovateConfigs are the other places Renovate reads its config from; with one of them present
// a renovate.json would not be read, so none is added.
var renovateConfigs = []string{"renovate.json5", ".github/renovate.json", ".github/renovate.json5", ".gitlab/renovate.json", ".gitlab/renovate.json5", ".renovaterc", ".renovaterc.json", ".renovaterc.json5"}

// ci seeds .grype.yaml and renovate.json, and, once the catalog names a released gate, the gate
// caller with ci/floor-guard.mjs (and ci/budget.json for a web frontend).
func (s *seeder) ci(gateRef string) error {
	if !s.theirs(".grype.yaml") {
		if err := s.copySeed(".grype.yaml", "ci/.grype.yaml", "ci"); err != nil {
			return err
		}
	}
	if abs, err := filepath.Abs(s.p.Dir); err == nil {
		if top := gitTop(abs); top != "" && top != abs {
			s.rep.Notices = append(s.rep.Notices, "this is a subfolder of the git repository at "+top+": the gate caller, the Renovate config and floor-guard belong at the repository root, where GitHub and Renovate read them, so makeit added none here")
			return nil
		}
	}
	if i := slices.IndexFunc(renovateConfigs, s.theirs); i >= 0 {
		s.manual("%s exists, so makeit added no renovate.json: merge the entries of makeit's templates/project/renovate.json into it", renovateConfigs[i])
	} else {
		tpl, err := s.src.ReadFile("templates/project/renovate.json")
		if err != nil {
			return err
		}
		s.merges = append(s.merges, MergeSeed{Path: "renovate.json", Template: "templates/project/renovate.json",
			Apply: func(theirs []byte, skip map[string]bool) (Merged, error) {
				return MergeJSON(theirs, tpl, map[string]string{"/customManagers": "description", "/packageRules": "description"}, skip)
			}})
	}
	if len(s.p.Types) == 0 {
		return nil
	}
	if gateRef == "" {
		s.rep.Notices = append(s.rep.Notices, "the CI caller needs a released makeit (the plugin set's ci.gateRef is empty); init adds it when run again after an update")
		return nil
	}
	m := gateRefPattern.FindStringSubmatch(gateRef)
	if m == nil {
		s.rep.Notices = append(s.rep.Notices, fmt.Sprintf("the plugin set's ci.gateRef %q is not owner/repo@<40-hex sha> # <tag>: no CI caller", gateRef))
		return nil
	}
	switch {
	case s.p.PM == "bun":
		s.manual("makeit seeds no CI caller for bun: the GitHub runner image has no bun; write the caller with a setup that installs it")
		return nil
	case s.p.Runner == "":
		s.manual("makeit seeds no CI caller for a %s project without a task runner (pip): move to uv, or write the caller by hand", s.p.PM)
		return nil
	}
	path := ".github/workflows/gate.yml"
	if s.theirs(path) {
		path = ".github/workflows/makeit-gate.yml"
	}
	tpl, err := s.src.ReadFile("ci/.github/workflows/gate-caller.yml")
	if err != nil {
		return err
	}
	web := s.is("web") && s.p.Scripts["test:e2e"]
	api := s.is("api") && s.is("node")
	nodeVersion := s.p.NodeVersion
	if nodeVersion == "" {
		nodeVersion = "24"
	}
	values := map[string]string{
		"runner": strconv.Quote(s.p.Runner), "setup": strconv.Quote(s.p.Setup), "node-version": strconv.Quote(nodeVersion),
		"has-web-ui": strconv.FormatBool(web), "has-openapi": strconv.FormatBool(api),
	}
	uses := m[1] + "/" + m[2] + "/.github/workflows/gate.yml@" + m[3] + " # " + m[4]
	s.seeds = append(s.seeds, Seed{Path: path, Template: "ci/.github/workflows/gate-caller.yml", Class: "ci", Body: caller(tpl, uses, s.p.DefaultBranch, values)})
	s.rep.Notices = append(s.rep.Notices, "the gate's floor-guard fails a PR that changes a workflow, a check task, a checker or hook config, or ci/floor-guard.mjs unless its description has a line floor-guard: <reason>; the PR with init's changes is one, as is one where makeit init --update rewrites the guard")
	if err := s.copySeed("ci/floor-guard.mjs", "ci/floor-guard.mjs", "ci"); err != nil {
		return err
	}
	if s.is("web") {
		if err := s.copySeed("ci/budget.json", "ci/budget.json", "ci"); err != nil {
			return err
		}
		if !web {
			s.manual("%s: set has-web-ui: true once the project has a test:e2e task (the Playwright + axe step adds it)", path)
		}
		s.manual("%s: set budget-url to a preview URL to run the sitespeed.io budget", path)
	}
	if s.is("api") && !api {
		s.manual("%s: set has-openapi: true once check:api exists", path)
	}
	if s.is("database") {
		s.manual("%s: set has-migrations: true once check:drift exists (the job also runs squawk on ./migrations/*.sql)", path)
	}
	if s.p.PM == "npm" && !s.p.Lockfile {
		s.manual("the CI checks run npm ci, which needs a committed package-lock.json: run npm install and commit it")
	}
	return nil
}

var (
	usesLine   = regexp.MustCompile(`(?m)^(\s*uses: ).*$`)
	branchLine = regexp.MustCompile(`(?m)^(\s*branches: )\[main\]$`)
)

// caller fills gate-caller.yml: its usage comment goes, the uses line pins the gate, the push
// branch is the default branch, and the inputs in values replace the template's.
func caller(tpl []byte, uses, branch string, values map[string]string) []byte {
	lines := strings.Split(string(tpl), "\n")
	for len(lines) > 0 && strings.HasPrefix(lines[0], "#") {
		lines = lines[1:]
	}
	for i, l := range lines {
		key, _, ok := strings.Cut(strings.TrimSpace(l), ":")
		if v, known := values[key]; ok && known && strings.HasPrefix(l, "      ") {
			lines[i] = l[:len(l)-len(strings.TrimLeft(l, " "))] + key + ": " + v
		}
	}
	out := "# makeit's CI checks, written by makeit init. makeit init --update rewrites it while nobody has\n# changed it; Renovate keeps the pinned sha current.\n" + strings.Join(lines, "\n")
	out = usesLine.ReplaceAllString(out, "${1}"+strings.ReplaceAll(uses, "$", "$$"))
	return []byte(branchLine.ReplaceAllString(out, "${1}["+strings.ReplaceAll(branch, "$", "$$")+"]"))
}

// dbhub adds DBHub to .mcp.json and seeds its two-source .dbhub.toml (no secret in it) for a
// project with a database.
func (s *seeder) dbhub() error {
	if !s.is("database") {
		return nil
	}
	server := `{"mcpServers": {"dbhub": {"type": "stdio", "command": "npx", "args": ["-y", "` + dbhubPkg + `", "--transport", "stdio", "--config", ".dbhub.toml"]}}}`
	s.merges = append(s.merges, MergeSeed{Path: ".mcp.json", Template: "makeit:dbhub",
		Apply: func(theirs []byte, skip map[string]bool) (Merged, error) {
			return MergeJSON(theirs, []byte(server), nil, skip)
		}})
	if s.theirs(".dbhub.toml") {
		s.manual(".dbhub.toml exists, so makeit kept it: makeit's version is templates/project/dbhub.toml (read and write sources, writes behind the ask rule)")
		return nil
	}
	return s.copySeed(".dbhub.toml", "templates/project/dbhub.toml", "settings")
}
