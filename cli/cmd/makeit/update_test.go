package main

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"makeit/internal/buildinfo"
	"makeit/internal/state"
)

func TestUpdateConsentExit2(t *testing.T) {
	fakeSetupEnv(t)
	code, out, errOut := call(t, noGather(t), "update")
	if code != 2 || out != "" || !strings.Contains(errOut, "Run again with --yes") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
}

func TestUpdateJSONFields(t *testing.T) {
	fakeSetupEnv(t)
	code, out, errOut := call(t, noGather(t), "update", "--dry-run", "--json")
	var report map[string]any
	decodeOne(t, out, &report)
	for _, k := range []string{"version", "plan", "generation", "summary"} {
		if _, ok := report[k]; !ok {
			t.Errorf("JSON lacks %q: %v", k, report)
		}
	}
	if code != 0 || errOut != "" {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
}

func TestUpdateNoticeOffline(t *testing.T) {
	fakeSetupEnv(t)
	savedRepo, savedBase := buildinfo.Repo, releaseBase
	t.Cleanup(func() { buildinfo.Repo, releaseBase = savedRepo, savedBase })
	buildinfo.Repo, releaseBase = "o/makeit", "http://127.0.0.1:1"
	code, _, errOut := call(t, noGather(t), "update", "--dry-run")
	if code != 0 || !strings.Contains(errOut, "could not check for a newer makeit") {
		t.Errorf("exit %d, stderr %q; want exit 0 and one could-not-check line", code, errOut)
	}
	if _, _, errOut := call(t, noGather(t), "update", "--dry-run", "--offline"); strings.Contains(errOut, "newer makeit") {
		t.Errorf("--offline still checked: %q", errOut)
	}
}

func TestUninstallDefaultsNo(t *testing.T) {
	_, stack := handInstalled(t, nil)
	if code, _ := setupYes(t); code != 0 {
		t.Fatalf("setup exit %d", code)
	}
	statePath := filepath.Join(stack, "state.json")
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	savedTTY, savedStdin := isTTY, os.Stdin
	t.Cleanup(func() { isTTY, os.Stdin = savedTTY, savedStdin })
	isTTY = func(io.Writer) bool { return true }
	// The plain prompt is under test here; the live prompt has its own tests in internal/ui.
	t.Setenv("CI", "1")
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString("\n"); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	os.Stdin = r
	code, _, errOut := call(t, noGather(t), "uninstall")
	if code != 0 || !strings.Contains(errOut, "[y/N]") {
		t.Errorf("exit %d, stderr %q; want the prompt to default to No and exit 0", code, errOut)
	}
	if after, _ := os.ReadFile(statePath); string(after) != string(before) {
		t.Error("state changed although the prompt was declined")
	}
}

func TestUninstallJSONKept(t *testing.T) {
	handInstalled(t, nil)
	if code, _ := setupYes(t); code != 0 {
		t.Fatalf("setup exit %d", code)
	}
	code, out, _ := call(t, noGather(t), "uninstall", "--dry-run", "--json")
	var report struct {
		Kept []struct{ Item, Reason string } `json:"kept"`
	}
	decodeOne(t, out, &report)
	items := map[string]string{}
	for _, k := range report.Kept {
		items[k.Item] = k.Reason
	}
	for _, item := range []string{"marketplace makeit", "makeit-bundle@makeit", "claude:CLAUDE.md"} {
		if items[item] != "yours" {
			t.Errorf("%s kept as %q, want yours (kept: %v)", item, items[item], report.Kept)
		}
	}
	if code != 0 {
		t.Errorf("exit %d", code)
	}
}

// calls are the argv lines the fake claude logged.
func calls(t *testing.T) []string {
	t.Helper()
	b, _ := os.ReadFile(os.Getenv("CALLS"))
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// mutating are the logged claude calls that change something.
func mutating(lines []string) []string {
	var out []string
	for _, l := range lines {
		if l == "" || l == "--version" || l == "doctor" || strings.HasSuffix(l, "list --json") || strings.Contains(l, "prune --dry-run") {
			continue
		}
		out = append(out, l)
	}
	return out
}

// On an adopted machine the first update moves only versions: the refresh and `claude plugin update`
// for the adopted pinned plugin. With its pin recorded, the second update has nothing to do.
func TestUpdateAdoptedRunsOnlyVersionUpdates(t *testing.T) {
	claudeDir, stack := handInstalled(t, nil)
	if code, _ := setupYes(t); code != 0 {
		t.Fatalf("setup exit %d", code)
	}
	before := snapshot(t, claudeDir)
	gens, _ := os.ReadDir(state.GenerationsDir(stack))
	want := [][]string{{"plugin marketplace update makeit --json", "plugin update superpowers@makeit --scope user --json"}, nil}
	for run := 1; run <= 2; run++ {
		if err := os.WriteFile(os.Getenv("CALLS"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		code, _, errOut := call(t, noGather(t), "update", "--yes")
		if code != 0 {
			t.Fatalf("run %d: exit %d, stderr %q", run, code, errOut)
		}
		if strings.Contains(errOut, "restart Claude Code") {
			t.Errorf("run %d asks for a restart although every plugin was up to date", run)
		}
		if got := mutating(calls(t)); !slices.Equal(got, want[run-1]) {
			t.Errorf("run %d changed %q, want %q", run, got, want[run-1])
		}
	}
	// The first update made one generation, the second none (prune may drop setup's empty one).
	es, _ := os.ReadDir(state.GenerationsDir(stack))
	top := 0
	for _, fi := range es {
		if n, err := strconv.Atoi(fi.Name()); err == nil && n > top {
			top = n
		}
	}
	if top != len(gens)+1 {
		t.Errorf("newest generation %d after setup's %d, want one more: the second update had nothing to do", top, len(gens))
	}
	if after := snapshot(t, claudeDir); !sameFiles(before, after) {
		t.Error("update changed a file in the Claude directory of an adopted machine")
	}
}

func TestUninstallAdoptedDryRun(t *testing.T) {
	claudeDir, _ := handInstalled(t, nil)
	if code, _ := setupYes(t); code != 0 {
		t.Fatalf("setup exit %d", code)
	}
	before := snapshot(t, claudeDir)
	code, _, errOut := call(t, noGather(t), "uninstall", "--dry-run")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	for _, want := range []string{"kept: marketplace makeit: yours", "kept: makeit-bundle@makeit: yours", "kept: claude:CLAUDE.md: yours", "kept: claude:rules/go.md: yours"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("dry run lacks %q:\n%s", want, errOut)
		}
	}
	if after := snapshot(t, claudeDir); !sameFiles(before, after) {
		t.Error("a dry run changed a file")
	}
}

// fakeClaude is a claude that remembers marketplaces and plugins in files under $FAKE_STATE: the
// bundle's install brings superpowers@makeit, prune's dry run lists it once the bundle is gone.
const fakeClaude = `#!/bin/sh
S="$FAKE_STATE"
touch "$S/markets" "$S/plugins"
echo "$*" >> "$CALLS"
plugins() {
  sep=""; printf '['
  while read -r id; do printf '%s{"id":"%s","version":"1.0.0","scope":"user","enabled":true}' "$sep" "$id"; sep=","; done < "$S/plugins"
  printf ']\n'
}
markets() {
  sep=""; printf '['
  while read -r name path; do
    if [ -n "$path" ]; then printf '%s{"name":"%s","source":"directory","path":"%s"}' "$sep" "$name" "$path"
    else printf '%s{"name":"%s","source":"github"}' "$sep" "$name"; fi
    sep=","
  done < "$S/markets"
  printf ']\n'
}
drop() { grep -v -x -e "$2" "$S/$1" > "$S/$1.tmp" || true; mv "$S/$1.tmp" "$S/$1"; }
case "$*" in
  "--version") echo "2.1.319 (Claude Code)" ;;
  "doctor") ;;
  "plugin list --json") plugins ;;
  "plugin marketplace list --json") markets ;;
  "plugin marketplace add anthropics/claude-plugins-official --scope user --json") echo "claude-plugins-official" >> "$S/markets"; echo '{"command":"marketplace-add","outcome":"ok"}' ;;
  "plugin marketplace add "*) echo "makeit $4" >> "$S/markets"; echo '{"command":"marketplace-add","outcome":"ok"}' ;;
  "plugin marketplace update "*) echo '{"command":"marketplace-update","outcome":"ok"}' ;;
  "plugin marketplace remove makeit --scope user --json") drop markets "makeit .*"; grep -v '@makeit$' "$S/plugins" > "$S/p.tmp" || true; mv "$S/p.tmp" "$S/plugins"; echo '{"command":"marketplace-remove","outcome":"ok"}' ;;
  "plugin install makeit-bundle@makeit --scope user --json") grep -q -x makeit-bundle@makeit "$S/plugins" || printf 'makeit-bundle@makeit\nsuperpowers@makeit\n' >> "$S/plugins"; echo '{"command":"install","outcome":"ok"}' ;;
  "plugin update "*) echo '{"command":"update","outcome":"ok","updateOutcome":"up_to_date"}' ;;
  "plugin uninstall "*) drop plugins "$3"; echo '{"command":"uninstall","outcome":"ok","keptData":true}' ;;
  "plugin prune --dry-run --scope user") grep -q -x makeit-bundle@makeit "$S/plugins" || { grep -q -x superpowers@makeit "$S/plugins" && echo "  superpowers@makeit (1.0.0)"; } ; true ;;
  *) echo "unexpected $*" >&2; exit 1 ;;
esac
`

func TestSetupUpdateUninstallRoundTrip(t *testing.T) {
	home, bin, stack, fake := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	stubTools(t, bin)
	write(t, filepath.Join(bin, "claude"), fakeClaude)
	claudeDir := filepath.Join(home, ".claude")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	t.Setenv("MAKEIT_DIR", stack)
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	t.Setenv("FAKE_STATE", fake)
	t.Setenv("CALLS", filepath.Join(fake, "calls"))
	for _, args := range [][]string{{"setup", "--yes"}, {"update", "--yes"}, {"uninstall", "--yes"}} {
		if code, _, errOut := call(t, noGather(t), args...); code != 0 {
			t.Fatalf("%v: exit %d\n%s", args, code, errOut)
		}
	}
	if es, _ := os.ReadDir(stack); len(es) != 1 || es[0].Name() != "generations" {
		t.Errorf("makeit directory holds %v, want only generations/", es)
	}
	if _, err := os.Stat(filepath.Join(claudeDir, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Errorf("CLAUDE.md still there: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	if err != nil || strings.Contains(string(b), "cleanupPeriodDays") || !strings.Contains(string(b), "claude-plugins-official") {
		t.Errorf("settings.json after uninstall (%v):\n%s\nwant only the claude-plugins-official declaration", err, b)
	}
	if plugins, _ := os.ReadFile(filepath.Join(fake, "plugins")); strings.TrimSpace(string(plugins)) != "" {
		t.Errorf("plugins left: %q", plugins)
	}
	if _, _, err := state.LoadState(filepath.Join(stack, "state.json")); err != nil {
		t.Errorf("state: %v", err)
	}
}

// snapshot maps every regular file under dir to its content.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		b, err := os.ReadFile(p)
		out[p] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sameFiles(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}
