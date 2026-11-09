package main

import (
	"encoding/json"
	"makeit/internal/pathenv"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"makeit/internal/catalog"
	"makeit/internal/jsonx"
	"makeit/internal/settings"
	"makeit/internal/state"
)

// handInstalled builds a hand-installed home: stub git/node/jq, a claude that lists the makeit
// marketplace and the bundle, the stack's global files, and settings.json with every stack
// value (edit changes it before it is written). It returns the claude config dir and MAKEIT_DIR.
func handInstalled(t *testing.T, edit func(*jsonx.Object)) (claudeDir, stack string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stubs")
	}
	home, bin, repo := t.TempDir(), t.TempDir(), t.TempDir()
	stack = t.TempDir()
	// The user's own marketplace: the bundle loads in place from it, superpowers is pinned.
	write(t, filepath.Join(repo, ".claude-plugin", "marketplace.json"), `{"name": "makeit", "plugins": [
		{"name": "makeit-bundle", "source": "./plugins/makeit-bundle"},
		{"name": "superpowers", "source": {"source": "github", "repo": "obra/superpowers", "sha": "2ce8aa15d05bd57213e6f746f953aafb7ded7a8b"}}]}`)
	for _, tool := range []string{"git", "node", "jq", "gh", "google-chrome", "prek", "deja", "grype", "typescript-language-server", "pyright-langserver"} {
		write(t, filepath.Join(bin, tool), "#!/bin/sh\n")
	}
	write(t, filepath.Join(bin, "claude"), `#!/bin/sh
echo "$*" >> "$CALLS"
case "$*" in
  "--version") echo "2.1.319 (Claude Code)" ;;
  "doctor") ;;
  "plugin marketplace list --json") echo '[{"name":"makeit","source":"directory","path":"`+repo+`"},{"name":"claude-plugins-official","source":"github"}]' ;;
  "plugin list --json") echo '[{"id":"makeit-bundle@makeit","version":"3.10.0","scope":"user","enabled":true,"readFromFolder":"`+repo+`/plugins/makeit-bundle"},{"id":"superpowers@makeit","version":"7.0.1","scope":"user","enabled":true}]' ;;
  "plugin marketplace update makeit --json") echo '{"command":"marketplace-update","outcome":"ok"}' ;;
  "plugin update "*) echo '{"command":"update","outcome":"ok","updateOutcome":"up_to_date"}' ;;
  "plugin prune --dry-run --scope user") ;;
  *) echo "unexpected $*" >&2; exit 1 ;;
esac
`)
	claudeDir = filepath.Join(home, ".claude")
	raw, err := catalog.Embedded.ReadFile("settings.baseline.json")
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := jsonx.DecodeObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	complete := settings.Merge(jsonx.NewObject(), settings.Desired(baseline, "makeit"), nil, settings.DefaultOptions()).Settings
	if edit != nil {
		edit(complete)
	}
	data, err := jsonx.Marshal(complete)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(claudeDir, "settings.json"), string(data))
	m, err := catalog.EmbeddedManifest()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range m.Files {
		if rel, ok := strings.CutPrefix(f.Path, "templates/global/"); ok {
			b, _ := catalog.Embedded.ReadFile(f.Path)
			write(t, filepath.Join(claudeDir, rel), string(b))
		}
	}
	// A global file the user changed: adopted, never touched.
	write(t, filepath.Join(claudeDir, "CLAUDE.md"), "# my own\n")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	t.Setenv("MAKEIT_DIR", stack)
	t.Setenv("PATH", bin)
	t.Setenv("CALLS", filepath.Join(t.TempDir(), "calls"))
	// PATH management (0.3) is also already in place by hand: the env file and the profile block.
	t.Setenv("SHELL", "/bin/sh")
	envPath := filepath.Join(stack, "env")
	write(t, envPath, string(pathenv.EnvFile(pathenv.Dirs(home, os.Getenv, dirExists), home)))
	write(t, filepath.Join(home, ".profile"), strings.Join(pathenv.Block(envPath, home), "\n")+"\n")
	return claudeDir, stack
}

func dirExists(p string) bool { _, err := os.Stat(p); return err == nil }

type setupReport struct {
	Plan []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"plan"`
	Generation int    `json:"generation"`
	Error      string `json:"error"`
}

func setupYes(t *testing.T) (int, setupReport) {
	t.Helper()
	code, out, errOut := call(t, noGather(t), "setup", "--yes", "--json")
	var r setupReport
	decodeOne(t, out, &r)
	if errOut != "" {
		t.Errorf("stderr %q", errOut)
	}
	return code, r
}

func manifest(t *testing.T, stack string, n int) state.Manifest {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(state.GenerationsDir(stack), strconvItoa(n), "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m state.Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func strconvItoa(n int) string { return strconv.Itoa(n) }

// Fixture (a): a complete hand install comes out byte-identical.
func TestAdoptedComplete(t *testing.T) {
	claudeDir, stack := handInstalled(t, nil)
	before := tree(t, claudeDir)
	code, r := setupYes(t)
	if code != 0 || r.Error != "" {
		t.Fatalf("exit %d: %+v", code, r)
	}
	if after := tree(t, claudeDir); !slices.Equal(before, after) {
		t.Errorf("the hand install changed:\nbefore %v\nafter  %v", before, after)
	}
	if r.Generation != 0 {
		if m := manifest(t, stack, r.Generation); len(m.Ops) != 0 {
			t.Errorf("generation %d changed files: %+v", r.Generation, m.Ops)
		}
	}
}

// Fixture (b): a hand install missing some stack values gets only those keys.
func TestAdoptedMissingValues(t *testing.T) {
	claudeDir, stack := handInstalled(t, func(o *jsonx.Object) {
		o.Delete("cleanupPeriodDays")
		env, _ := o.Get("env")
		env.(*jsonx.Object).Delete("CLAUDE_CODE_ENABLE_TODO_TOOLS")
		o.Set("model", "claude-sonnet-5-5")
	})
	settingsPath := filepath.Join(claudeDir, "settings.json")
	old, _ := os.ReadFile(settingsPath)
	globals := tree(t, filepath.Join(claudeDir, "rules"))
	mine, _ := os.ReadFile(filepath.Join(claudeDir, "CLAUDE.md"))
	code, r := setupYes(t)
	if code != 0 || r.Error != "" || r.Generation == 0 {
		t.Fatalf("exit %d: %+v", code, r)
	}
	got, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	was, _ := jsonx.DecodeObject(old)
	now, err := jsonx.DecodeObject(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range was.Keys() {
		a, _ := was.Get(k)
		b, _ := now.Get(k)
		if k != "env" && !jsonx.Equal(a, b) {
			t.Errorf("%s changed: %v -> %v", k, a, b)
		}
	}
	if v, _ := now.Get("cleanupPeriodDays"); v == nil {
		t.Error("cleanupPeriodDays not added")
	}
	if env, _ := now.Get("env"); env == nil {
		t.Error("env lost")
	} else if v, _ := env.(*jsonx.Object).Get("CLAUDE_CODE_ENABLE_TODO_TOOLS"); v == nil {
		t.Error("env.CLAUDE_CODE_ENABLE_TODO_TOOLS not added")
	}
	if !slices.Equal(globals, tree(t, filepath.Join(claudeDir, "rules"))) {
		t.Error("global rules changed")
	}
	if b, _ := os.ReadFile(filepath.Join(claudeDir, "CLAUDE.md")); string(b) != string(mine) {
		t.Error("the user's CLAUDE.md changed")
	}
	m := manifest(t, stack, r.Generation)
	if len(m.Ops) != 1 || m.Ops[0].Path != "claude:settings.json" {
		t.Fatalf("ops %+v, want only settings.json", m.Ops)
	}
	pre, _ := os.ReadFile(filepath.Join(state.GenerationsDir(stack), strconvItoa(r.Generation), m.Ops[0].PreImage))
	if string(pre) != string(old) {
		t.Error("the settings pre-image is not the old file")
	}
}

func TestSecondRunNothingToDo(t *testing.T) {
	handInstalled(t, nil)
	if code, r := setupYes(t); code != 0 {
		t.Fatalf("first run exit %d: %+v", code, r)
	}
	code, r := setupYes(t)
	if code != 0 || r.Generation != 0 {
		t.Fatalf("second run exit %d, generation %d", code, r.Generation)
	}
	for _, o := range r.Plan {
		if o.Status != "already" {
			t.Errorf("second run: %s is %s", o.ID, o.Status)
		}
	}
}
