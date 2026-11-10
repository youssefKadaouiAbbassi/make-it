package catalog

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// The embedded set must equal what synccatalog would produce from the live tree now.
func TestEmbeddedMatchesTree(t *testing.T) {
	repo, _ := filepath.Abs("../../..")
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		t.Skip("not inside the catalog repository")
	}
	m, err := EmbeddedManifest()
	if err != nil {
		t.Fatal(err)
	}
	var live Manifest
	tmp := t.TempDir()
	if out, err := exec.Command("go", "run", "../../tools/synccatalog", "-repo", repo, "-out", tmp).CombinedOutput(); err != nil {
		t.Fatalf("synccatalog: %v\n%s", err, out)
	}
	b, _ := os.ReadFile(filepath.Join(tmp, "MANIFEST.json"))
	if err := json.Unmarshal(b, &live); err != nil {
		t.Fatal(err)
	}
	if Digest(live) != Digest(m) {
		t.Fatalf("embedded catalog is stale (%d files embedded, %d in the tree): run go generate ./...", len(m.Files), len(live.Files))
	}
}

func TestManifestRules(t *testing.T) {
	m, err := EmbeddedManifest()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range m.Files {
		if e.Mode == "0755" && filepath.Ext(e.Path) != ".sh" && filepath.Ext(e.Path) != ".mjs" && filepath.Ext(e.Path) != ".py" {
			t.Errorf("unexpected executable %s", e.Path)
		}
		if filepath.Ext(e.Path) == ".out" {
			t.Errorf("build output embedded: %s", e.Path)
		}
		seen[e.Path] = true
	}
	for _, must := range []string{".claude-plugin/marketplace.json", "makeit.json", "settings.baseline.json", "templates/global/CLAUDE.md"} {
		if !seen[must] {
			t.Errorf("%s missing", must)
		}
	}
	r, err := LoadRequires(Embedded)
	if err != nil || r.Requires.ClaudeCode.Hard == "" || r.Bundle == "" {
		t.Fatalf("requires: %+v %v", r, err)
	}
}

func TestSyncFreshUpdateAndInPlace(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "catalog")
	n, err := SyncTree(dir, embeddedTree(t))
	if err != nil || n == 0 {
		t.Fatalf("sync: %d %v", n, err)
	}
	if ok, _ := upToDate(t, dir); !ok {
		t.Fatal("not up to date after sync")
	}
	if err := Valid(dir); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		st, err := os.Stat(filepath.Join(dir, "plugins", "makeit", "hooks", "notify.sh"))
		if err != nil || st.Mode().Perm()&0o111 == 0 {
			t.Fatalf("executable bit lost: %v %v", st, err)
		}
	}
	// A user file and a stale catalog file: the swap replaces the whole tree.
	stale := filepath.Join(dir, "templates", "gone.md")
	_ = os.WriteFile(stale, []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, MarkerName), []byte(`{"digest":"old","files":["templates/gone.md"]}`), 0o644)
	if ok, _ := upToDate(t, dir); ok {
		t.Fatal("old marker read as current")
	}
	if _, err := SyncTree(dir, embeddedTree(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale file kept")
	}
	for _, leftover := range []string{dir + ".next", dir + ".old"} {
		if _, err := os.Stat(leftover); !os.IsNotExist(err) {
			t.Fatalf("%s left behind", leftover)
		}
	}
	// In-place fallback: only files the previous marker listed are removed.
	m := embeddedTree(t)
	mine := filepath.Join(dir, "mine.txt")
	_ = os.WriteFile(mine, []byte("keep"), 0o644)
	_ = os.WriteFile(stale, []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, MarkerName), []byte(`{"digest":"old","files":["templates/gone.md"]}`), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "makeit.json"), []byte("{}"), 0o644)
	n, err = syncInPlace(dir, m)
	if err != nil || n != 1 {
		t.Fatalf("in place: %d %v", n, err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("in place kept a file the old sync wrote")
	}
	if _, err := os.Stat(mine); err != nil {
		t.Fatal("in place removed a file it never wrote")
	}
	if ok, _ := upToDate(t, dir); !ok {
		t.Fatal("in place not up to date")
	}
}

func embeddedTree(t *testing.T) *Tree {
	t.Helper()
	tr, err := EmbeddedTree()
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

// upToDate is the old UpToDate(dir) against the embedded catalog.
func upToDate(t *testing.T, dir string) (bool, error) { return UpToDateTree(dir, embeddedTree(t)), nil }

// claude plugin update clones a "github" source over SSH (no fallback to HTTPS), which fails on a
// machine with no GitHub host key; a "url" source with an https URL updates anywhere.
func TestNoGitHubSources(t *testing.T) {
	t.Parallel()
	tree, err := EmbeddedTree()
	if err != nil {
		t.Fatal(err)
	}
	b, err := tree.ReadFile(".claude-plugin/marketplace.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Plugins []struct {
			Name   string          `json:"name"`
			Source json.RawMessage `json:"source"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, p := range m.Plugins {
		var src struct {
			Source string `json:"source"`
		}
		if json.Unmarshal(p.Source, &src) == nil && src.Source == "github" {
			t.Errorf("%s uses a github source; use {\"source\": \"url\", \"url\": \"https://github.com/<repo>.git\"}", p.Name)
		}
	}
}

func TestBaselineSetupReview(t *testing.T) {
	b, err := Embedded.ReadFile("settings.baseline.json")
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		CrossSessionInbound string            `json:"crossSessionInbound"`
		Env                 map[string]string `json:"env"`
		Permissions         struct {
			Ask []string `json:"ask"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	if s.CrossSessionInbound != "accept" {
		t.Errorf("crossSessionInbound = %q", s.CrossSessionInbound)
	}
	for k, want := range map[string]string{"CLAUDE_CODE_ENABLE_TODO_TOOLS": "1", "CLAUDE_CODE_SUBAGENT_EFFORT": "medium", "DEJA_REPO": "off"} {
		if s.Env[k] != want {
			t.Errorf("env %s = %q, want %q", k, s.Env[k], want)
		}
	}
	if _, ok := s.Env["DOCS_MCP_TELEMETRY"]; ok {
		t.Error("env DOCS_MCP_TELEMETRY still in the baseline")
	}
	has := func(r string) bool {
		for _, a := range s.Permissions.Ask {
			if a == r {
				return true
			}
		}
		return false
	}
	for _, r := range []string{"Bash(gh api *merge*)", "Bash(gh pr merge *)"} {
		if !has(r) {
			t.Errorf("ask lacks %s", r)
		}
	}
	if has("Bash(gh api *merge-async*)") {
		t.Error("ask still has the merge-async rule")
	}
	r, err := LoadRequires(Embedded)
	if err != nil || r.Requires.ClaudeCode.Hard != "2.1.319" {
		t.Errorf("hard floor = %q, %v", r.Requires.ClaudeCode.Hard, err)
	}
}

// The advisor is off in makeit's baseline: m9 measured no quality gain and 4.4x cost per consultation.
func TestBaselineHasNoAdvisor(t *testing.T) {
	b, err := Embedded.ReadFile("settings.baseline.json")
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	if v, ok := s["advisorModel"]; ok {
		t.Errorf("baseline sets advisorModel = %v", v)
	}
}

func TestMCPSetupReview(t *testing.T) {
	b, err := Embedded.ReadFile("plugins/makeit/.mcp.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		MCPServers map[string]struct {
			Args []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	cd, ok := m.MCPServers["chrome-devtools"]
	if len(m.MCPServers) != 1 || !ok {
		t.Fatalf("servers = %v, want only chrome-devtools", m.MCPServers)
	}
	isolated := false
	for _, a := range cd.Args {
		isolated = isolated || a == "--isolated"
	}
	if !isolated {
		t.Errorf("chrome-devtools args %v lack --isolated", cd.Args)
	}
	for _, f := range []string{".claude-plugin/marketplace.json", "plugins/makeit-bundle/.claude-plugin/plugin.json"} {
		d, err := Embedded.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(d), "docs-mcp-server") {
			t.Errorf("%s still mentions docs-mcp-server", f)
		}
	}
}

// The shipped server starts the local copy with node when it is there and npx otherwise, on the
// version .mcp.json pins; the layout is the one mcplocal installs.
func TestMCPLocalFirstNpxFallback(t *testing.T) {
	b, err := Embedded.ReadFile("plugins/makeit/.mcp.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	cd := m.MCPServers["chrome-devtools"]
	if cd.Command != "node" || len(cd.Args) < 4 || cd.Args[0] != "-e" {
		t.Fatalf("command %q args %q, want node -e <script> <package@version> ...", cd.Command, cd.Args)
	}
	script := cd.Args[1]
	local := strings.Index(script, "'mcp','chrome-devtools','node_modules','chrome-devtools-mcp'")
	exists := strings.Index(script, "existsSync(entry)")
	npx := strings.Index(script, "'npx',['-y',spec]")
	if local < 0 || exists < local || npx < exists {
		t.Errorf("script order wrong (local path %d, exists check %d, npx %d)", local, exists, npx)
	}
	// In-process (one node, not two), only when the copy is the pinned version, and npx when the
	// load throws or npx itself cannot start (with one line saying so).
	for _, want := range []string{"import(url.pathToFileURL(entry).href)", "version===spec.split('@').pop()", ".catch(function(e)", "makeit: could not start npx"} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	if strings.Contains(script, "spawn(process.execPath") {
		t.Error("script spawns a second node for the local copy")
	}
	if !regexp.MustCompile(`^chrome-devtools-mcp@\d+\.\d+\.\d+$`).MatchString(cd.Args[2]) {
		t.Errorf("args[2] = %q, want the pinned package", cd.Args[2])
	}
	for _, flag := range []string{"--isolated", "--no-performance-crux"} {
		if !slices.Contains(cd.Args, flag) {
			t.Errorf("args %v lack %s", cd.Args, flag)
		}
	}
	if cd.Env["CHROME_DEVTOOLS_MCP_NO_USAGE_STATISTICS"] != "1" {
		t.Errorf("env %v lacks the telemetry switch", cd.Env)
	}
}
