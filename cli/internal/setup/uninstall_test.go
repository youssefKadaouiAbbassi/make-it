package setup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"makeit/internal/catalog"
	"makeit/internal/claudecli"
	"makeit/internal/execx"
	"makeit/internal/jsonx"
	"makeit/internal/plan"
	"makeit/internal/settings"
	"makeit/internal/state"
)

// installed is a machine after `makeit setup --yes`; the bundle brought in two dependencies.
func installed(t *testing.T) *fakeMachine {
	t.Helper()
	m := newMachine(t)
	m.deps = []string{"superpowers@makeit", "typescript-lsp@claude-plugins-official"}
	apply(t, m)
	return m
}

func uninstall(t *testing.T, m *fakeMachine, opts UninstallOptions) Report {
	t.Helper()
	rep, err := Uninstall(context.Background(), m.env, plan.Apply, nil, []string{"uninstall", "--yes"}, opts)
	if err != nil {
		t.Fatalf("uninstall: %v (%s)", err, rep.Plan.Summary())
	}
	return rep
}

func reasonOf(rep Report, item string) string {
	for _, k := range rep.Kept {
		if k.Item == item {
			return k.Reason
		}
	}
	return ""
}

func called(m *fakeMachine, sub string) int {
	for i, c := range m.calls {
		if strings.Contains(c, sub) {
			return i
		}
	}
	return -1
}

func globalPaths(t *testing.T, claude string) []string {
	t.Helper()
	man, err := catalog.EmbeddedManifest()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range man.Files {
		if rel, ok := strings.CutPrefix(f.Path, "templates/global/"); ok {
			out = append(out, filepath.Join(claude, filepath.FromSlash(rel)))
		}
	}
	return out
}

func settingsOf(t *testing.T, m *fakeMachine) *jsonx.Object {
	t.Helper()
	o, err := jsonx.DecodeObject([]byte(read(t, filepath.Join(m.env.Roots.Claude, "settings.json"))))
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func saveSettings(t *testing.T, m *fakeMachine, o *jsonx.Object) {
	t.Helper()
	b, err := jsonx.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(m.env.Roots.Claude, "settings.json"), string(b))
}

func dataDir(t *testing.T, m *fakeMachine, id string) string {
	t.Helper()
	dir := filepath.Join(m.env.Roots.Claude, "plugins", "data", DataID(id))
	writeFile(t, filepath.Join(dir, "marker"), "kept\n")
	writeFile(t, filepath.Join(dir, "pkg", "cli.js"), "x\n")
	if err := os.MkdirAll(filepath.Join(dir, "node_modules", ".bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../pkg/cli.js", filepath.Join(dir, "node_modules", ".bin", "tool")); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDataID(t *testing.T) {
	for id, want := range map[string]string{"my-plugin@my-marketplace": "my-plugin-my-marketplace", "a.b@c": "a-b-c", "x_y@z": "x_y-z"} {
		if got := DataID(id); got != want {
			t.Errorf("DataID(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestUninstallWithoutState(t *testing.T) {
	m := newMachine(t)
	if _, err := Uninstall(context.Background(), m.env, plan.Apply, nil, nil, UninstallOptions{}); !errors.Is(err, ErrNothing) {
		t.Errorf("err = %v, want ErrNothing", err)
	}
}

func TestUninstallRoundTrip(t *testing.T) {
	m := installed(t)
	rep := uninstall(t, m, UninstallOptions{})
	if rep.Plan.Failed() {
		t.Fatalf("failed: %+v", rep.Plan.Outcomes)
	}
	for _, want := range []string{
		"/usr/bin/claude plugin uninstall makeit-bundle@makeit --scope user --keep-data --json",
		"/usr/bin/claude plugin uninstall superpowers@makeit --scope user --keep-data --json",
		"/usr/bin/claude plugin uninstall typescript-lsp@claude-plugins-official --scope user --keep-data --json",
		"/usr/bin/claude plugin marketplace remove makeit --scope user --json",
	} {
		if called(m, want) < 0 {
			t.Errorf("missing %s in %v", want, m.calls)
		}
	}
	if called(m, "--prune") >= 0 || called(m, "marketplace remove claude-plugins-official") >= 0 {
		t.Errorf("pruned or removed the official marketplace: %v", m.calls)
	}
	if called(m, "uninstall makeit-bundle@makeit") > called(m, "uninstall superpowers@makeit") {
		t.Error("a dependency was uninstalled before the bundle")
	}
	for _, p := range globalPaths(t, m.env.Roots.Claude) {
		if _, err := os.Stat(p); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s still there: %v", p, err)
		}
	}
	if es, _ := os.ReadDir(m.env.Roots.Stack); len(es) != 1 || es[0].Name() != "generations" {
		t.Errorf("stack dir holds %v, want only generations/", es)
	}
}

// The settings goal: beyond the pre-setup content (none here), only the declaration of
// claude-plugins-official remains, because that marketplace stays.
func TestUninstallKeepsOfficialDeclaration(t *testing.T) {
	m := installed(t)
	uninstall(t, m, UninstallOptions{})
	live := settingsOf(t, m)
	raw, err := catalog.Embedded.ReadFile("settings.baseline.json")
	if err != nil {
		t.Fatal(err)
	}
	base, _ := jsonx.DecodeObject(raw)
	mk, _ := base.Get("extraKnownMarketplaces")
	official, _ := mk.(*jsonx.Object).Get("claude-plugins-official")
	want := jsonx.NewObject()
	wantMk := jsonx.NewObject()
	wantMk.Set("claude-plugins-official", official)
	want.Set("extraKnownMarketplaces", wantMk)
	if !jsonx.Equal(live, want) {
		got, _ := jsonx.Marshal(live)
		t.Errorf("settings after uninstall:\n%s\nwant only the claude-plugins-official declaration", got)
	}
}

func TestUninstallKeepsChangedValueAndFile(t *testing.T) {
	m := installed(t)
	o := settingsOf(t, m)
	o.Set("cleanupPeriodDays", json.Number("999"))
	saveSettings(t, m, o)
	rules := filepath.Join(m.env.Roots.Claude, "rules", "go.md")
	writeFile(t, rules, "mine\n")
	rep := uninstall(t, m, UninstallOptions{})
	if v, _ := settingsOf(t, m).Get("cleanupPeriodDays"); !jsonx.Equal(v, json.Number("999")) {
		t.Errorf("cleanupPeriodDays = %v, want the user's 999 kept", v)
	}
	if read(t, rules) != "mine\n" || reasonOf(rep, "claude:rules/go.md") != "changed since makeit wrote it" {
		t.Errorf("rules %q, kept %q", read(t, rules), reasonOf(rep, "claude:rules/go.md"))
	}
	if reasonOf(rep, "/cleanupPeriodDays") == "" {
		t.Errorf("kept %+v, want the changed value listed", rep.Kept)
	}
}

func TestUninstallKeepsAdopted(t *testing.T) {
	m := newMachine(t)
	m.markets = []string{"makeit", "claude-plugins-official"}
	m.plugins = []string{"makeit-bundle@makeit"}
	for _, p := range globalPaths(t, m.env.Roots.Claude) {
		writeFile(t, p, "mine\n")
	}
	apply(t, m)
	before := len(m.calls)
	rep := uninstall(t, m, UninstallOptions{})
	for _, c := range m.calls[before:] {
		if strings.Contains(c, "plugin uninstall") || strings.Contains(c, "marketplace remove") {
			t.Errorf("touched an adopted item: %s", c)
		}
	}
	for _, item := range []string{"marketplace makeit", "makeit-bundle@makeit", "claude:CLAUDE.md", "claude:rules/go.md"} {
		if r := reasonOf(rep, item); r != "yours" {
			t.Errorf("%s kept as %q, want yours", item, r)
		}
	}
	if read(t, filepath.Join(m.env.Roots.Claude, "CLAUDE.md")) != "mine\n" {
		t.Error("an adopted file changed")
	}
}

func TestUninstallBacksUpDataBeforeRemove(t *testing.T) {
	m := installed(t)
	dataDir(t, m, "superpowers@makeit")
	backedUp := false
	m.onRemove = func() {
		marks, _ := filepath.Glob(filepath.Join(state.GenerationsDir(m.env.Roots.Stack), "*", "data", "superpowers-makeit", "marker"))
		backedUp = len(marks) == 1
	}
	uninstall(t, m, UninstallOptions{})
	if !backedUp {
		t.Fatal("the marketplace was removed before its plugins' data was copied into the generation")
	}
	links, _ := filepath.Glob(filepath.Join(state.GenerationsDir(m.env.Roots.Stack), "*", "data", "superpowers-makeit", "node_modules", ".bin", "tool"))
	if len(links) != 1 {
		t.Fatalf("symlink not copied: %v", links)
	}
	if fi, err := os.Lstat(links[0]); err != nil || fi.Mode()&fs.ModeSymlink == 0 {
		t.Errorf("the backup's link is %v, %v; want a symlink", fi, err)
	}
}

func TestUninstallUserPluginKeepsMarketplace(t *testing.T) {
	m := installed(t)
	m.plugins = append(m.plugins, "makeit-ui@makeit")
	rep := uninstall(t, m, UninstallOptions{})
	if called(m, "marketplace remove") >= 0 {
		t.Errorf("removed a marketplace a plugin of the user's comes from: %v", m.calls)
	}
	if r := reasonOf(rep, "marketplace makeit"); !strings.Contains(r, "makeit-ui@makeit") {
		t.Errorf("kept %q, want the reason naming makeit-ui@makeit", r)
	}
	st, _, err := state.LoadState(filepath.Join(m.env.Roots.Stack, "state.json"))
	if err != nil || st.Marketplaces["makeit"] == nil || st.Marketplaces["makeit"].AddedBy != "makeit" {
		t.Errorf("state %v, %+v; want the marketplace still recorded as makeit's", err, st.Marketplaces)
	}
	if _, err := os.Stat(filepath.Join(m.env.Roots.Stack, "catalog")); err != nil {
		t.Errorf("catalog removed while its marketplace stays: %v", err)
	}
}

func TestUninstallDependentKept(t *testing.T) {
	m := installed(t)
	m.needed = map[string]bool{"superpowers@makeit": true}
	rep := uninstall(t, m, UninstallOptions{})
	if called(m, "uninstall superpowers@makeit") >= 0 {
		t.Errorf("uninstalled a plugin another plugin needs: %v", m.calls)
	}
	if r := reasonOf(rep, "superpowers@makeit"); r != "another installed plugin still needs it" {
		t.Errorf("kept %q", r)
	}
}

func TestUninstallRerunAfterFailure(t *testing.T) {
	m := installed(t)
	m.failUninstall = "superpowers@makeit"
	rep, err := Uninstall(context.Background(), m.env, plan.Apply, nil, nil, UninstallOptions{})
	if err != nil || !rep.Plan.Failed() {
		t.Fatalf("first run: %v, failed = %v", err, rep.Plan.Failed())
	}
	statePath := filepath.Join(m.env.Roots.Stack, "state.json")
	st, _, err := state.LoadState(statePath)
	if err != nil || st.Plugins["superpowers@makeit"] == nil {
		t.Fatalf("state %v, %+v; want the failed plugin still recorded", err, st.Plugins)
	}
	m.failUninstall = ""
	rep = uninstall(t, m, UninstallOptions{})
	if rep.Plan.Failed() || called(m, "marketplace remove makeit") < 0 {
		t.Errorf("rerun failed or kept the marketplace: %+v", rep.Plan.Outcomes)
	}
	if _, err := os.Stat(statePath); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("state.json still there after the rerun: %v", err)
	}
}

func TestUninstallUnmergesAtApplyTime(t *testing.T) {
	m := installed(t)
	confirm := func([]plan.Outcome) (bool, error) {
		o := settingsOf(t, m)
		o.Set("theme", "dark")
		saveSettings(t, m, o)
		return true, nil
	}
	if _, err := Uninstall(context.Background(), m.env, plan.Prompt, confirm, nil, UninstallOptions{}); err != nil {
		t.Fatal(err)
	}
	if v, ok := settingsOf(t, m).Get("theme"); !ok || v != "dark" {
		t.Errorf("theme = %v, %v; a value written after the plan must stay", v, ok)
	}
}

func TestUninstallKeepsSymlinkedFile(t *testing.T) {
	m := installed(t)
	path := filepath.Join(m.env.Roots.Claude, "rules", "go.md")
	target := filepath.Join(t.TempDir(), "go.md")
	writeFile(t, target, read(t, path))
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	rep := uninstall(t, m, UninstallOptions{})
	if fi, err := os.Lstat(path); err != nil || fi.Mode()&fs.ModeSymlink == 0 {
		t.Errorf("link gone: %v", err)
	}
	if r := reasonOf(rep, "claude:rules/go.md"); r != "a link, so it is yours" {
		t.Errorf("kept %q", r)
	}
}

func tree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			out[p] = "link:" + target
			return err
		case d.Type().IsRegular():
			b, err := os.ReadFile(p)
			sum := sha256.Sum256(b)
			out[p] = hex.EncodeToString(sum[:])
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestUninstallDryRunChangesNothing(t *testing.T) {
	m := installed(t)
	before := tree(t, m.env.Roots.Home)
	rep, err := Uninstall(context.Background(), m.env, plan.DryRunOnly, nil, nil, UninstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	after := tree(t, m.env.Roots.Home)
	if len(before) != len(after) {
		t.Errorf("file count %d, then %d", len(before), len(after))
	}
	for p, h := range before {
		if after[p] != h {
			t.Errorf("%s changed in a dry run", p)
		}
	}
	if !slices.ContainsFunc(rep.Plan.Outcomes, func(o plan.Outcome) bool { return o.Status == plan.WouldChange }) {
		t.Errorf("the dry run shows nothing to remove: %+v", rep.Plan.Outcomes)
	}
}

func TestUninstallPurge(t *testing.T) {
	m := installed(t)
	data := dataDir(t, m, "superpowers@makeit")
	uninstall(t, m, UninstallOptions{Purge: true})
	for _, p := range []string{m.env.Roots.Stack, data} {
		if _, err := os.Stat(p); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s still there after --purge: %v", p, err)
		}
	}
}

// Final review C1: --purge must not delete what the same run kept.
func TestPurgeKeepsWhatTheRunKept(t *testing.T) {
	m := installed(t)
	m.plugins = append(m.plugins, "makeit-ui@makeit")
	rep := uninstall(t, m, UninstallOptions{Purge: true})
	for _, p := range []string{filepath.Join(m.env.Roots.Stack, "catalog"), filepath.Join(m.env.Roots.Stack, "state.json")} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s deleted by --purge although the marketplace stays: %v", p, err)
		}
	}
	for _, o := range rep.Plan.Outcomes {
		if o.ID == "purge" && o.Status != plan.Skipped {
			t.Errorf("purge %s; want skipped while something of makeit remains", o.Status)
		}
	}
}

func TestPurgeKeepsDataOfKeptPlugin(t *testing.T) {
	m := installed(t)
	m.needed = map[string]bool{"superpowers@makeit": true}
	data := dataDir(t, m, "superpowers@makeit")
	uninstall(t, m, UninstallOptions{Purge: true})
	if _, err := os.Stat(data); err != nil {
		t.Errorf("data of the still-installed superpowers@makeit deleted by --purge: %v", err)
	}
}

// Final review I5: purge deletes makeit's own files, not the directory MAKEIT_DIR names.
func TestPurgeDeletesOnlyMakeitsOwn(t *testing.T) {
	m := installed(t)
	notes := filepath.Join(m.env.Roots.Stack, "notes.txt")
	writeFile(t, notes, "mine\n")
	uninstall(t, m, UninstallOptions{Purge: true})
	if read(t, notes) != "mine\n" {
		t.Error("purge deleted a file it did not write")
	}
	if _, err := os.Stat(state.GenerationsDir(m.env.Roots.Stack)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("generations survived --purge: %v", err)
	}
}

// Final review C2: a settings step that fails at apply time keeps the records, so a rerun finishes.
func TestSettingsFailureKeepsState(t *testing.T) {
	m := installed(t)
	inner := m.env.Run
	m.env.Run = func(ctx context.Context, c execx.Cmd) (execx.Result, error) {
		if strings.Join(c.Argv, " ") == "/usr/bin/claude doctor" {
			return execx.Result{Code: 1, Stderr: "doctor broke"}, nil
		}
		return inner(ctx, c)
	}
	if rep, _ := Uninstall(context.Background(), m.env, plan.Apply, nil, nil, UninstallOptions{}); !rep.Plan.Failed() {
		t.Fatal("the settings step did not fail")
	}
	if _, err := os.Stat(filepath.Join(m.env.Roots.Stack, "state.json")); err != nil {
		t.Fatalf("state.json removed although makeit's settings values are still there: %v", err)
	}
	m.env.Run = inner
	if _, err := Uninstall(context.Background(), m.env, plan.Apply, nil, nil, UninstallOptions{}); err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if _, ok := settingsOf(t, m).Get("cleanupPeriodDays"); ok {
		t.Error("the rerun left makeit's cleanupPeriodDays in settings.json")
	}
}

// Final review C2: a catalog that could not be removed keeps state.json, so a rerun retries.
func TestCatalogFailureKeepsState(t *testing.T) {
	m := installed(t)
	locked := filepath.Join(m.env.Roots.Stack, "catalog", "plugins")
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if rep, _ := Uninstall(context.Background(), m.env, plan.Apply, nil, nil, UninstallOptions{}); !rep.Plan.Failed() {
		t.Fatal("the catalog removal did not fail")
	}
	if _, err := os.Stat(filepath.Join(m.env.Roots.Stack, "state.json")); err != nil {
		t.Errorf("state.json removed although the catalog is still there: %v", err)
	}
}

// Every value makeit's merge recorded was already there: nothing to take out, and no record keeps
// state.json alive.
func TestUninstallAdoptedSettingsLeaveNoRecord(t *testing.T) {
	m := newMachine(t)
	raw, err := catalog.Embedded.ReadFile("settings.baseline.json")
	if err != nil {
		t.Fatal(err)
	}
	base, err := jsonx.DecodeObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	complete := settings.Merge(jsonx.NewObject(), settings.Desired(base, "makeit"), nil, settings.DefaultOptions()).Settings
	saveSettings(t, m, complete)
	apply(t, m)
	uninstall(t, m, UninstallOptions{})
	if _, err := os.Stat(filepath.Join(m.env.Roots.Stack, "state.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("state.json kept for settings records with nothing to take out: %v", err)
	}
}

// The ruling: --keep-settings leaves the values, so their records do not keep state.json.
func TestKeepSettingsRemovesState(t *testing.T) {
	m := installed(t)
	uninstall(t, m, UninstallOptions{KeepSettings: true})
	if _, ok := settingsOf(t, m).Get("cleanupPeriodDays"); !ok {
		t.Error("--keep-settings took makeit's values out")
	}
	if _, err := os.Stat(filepath.Join(m.env.Roots.Stack, "state.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("state.json kept after --keep-settings: %v", err)
	}
}

// Final review I4: stow's tree folding links the whole rules/ directory into the user's dotfiles;
// what is under a link is the user's.
func TestUninstallKeepsLinkedDirectory(t *testing.T) {
	m := installed(t)
	rules := filepath.Join(m.env.Roots.Claude, "rules")
	dotfiles := filepath.Join(t.TempDir(), "dotfiles", "claude", "rules")
	if err := os.MkdirAll(filepath.Dir(dotfiles), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(rules, dotfiles); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dotfiles, rules); err != nil {
		t.Fatal(err)
	}
	rep := uninstall(t, m, UninstallOptions{})
	if _, err := os.Stat(filepath.Join(dotfiles, "go.md")); err != nil {
		t.Errorf("uninstall deleted go.md from the user's dotfiles: %v", err)
	}
	if fi, err := os.Lstat(rules); err != nil || fi.Mode()&fs.ModeSymlink == 0 {
		t.Errorf("uninstall removed the user's rules link: %v", err)
	}
	if r := reasonOf(rep, "claude:rules/go.md"); r != "a link, so it is yours" {
		t.Errorf("kept %q", r)
	}
	if _, err := os.Stat(filepath.Join(m.env.Roots.Stack, "state.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("state.json kept for files that are the user's now: %v", err)
	}
}

// A file makeit wrote and the user changed is theirs now: kept, and it does not hold state.json.
func TestUninstallChangedFileHoldsNoRecord(t *testing.T) {
	m := installed(t)
	writeFile(t, filepath.Join(m.env.Roots.Claude, "rules", "go.md"), "mine\n")
	uninstall(t, m, UninstallOptions{})
	if _, err := os.Stat(filepath.Join(m.env.Roots.Stack, "state.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("state.json kept for a file that is the user's now: %v", err)
	}
}

// The delegate's ruling: without a usable Claude Code, uninstall removes what it can (makeit's
// files, settings values and catalog), keeps state.json with the plugins and marketplace left,
// says how to finish, and never touches plugin folders.
func TestUninstallWithoutClaude(t *testing.T) {
	m := installed(t)
	data := dataDir(t, m, "superpowers@makeit")
	located := m.env.Locate
	m.env.Locate = func(context.Context) []claudecli.Install { return nil }
	before := len(m.calls)
	rep, err := Uninstall(context.Background(), m.env, plan.Apply, nil, nil, UninstallOptions{})
	if err != nil || rep.Plan.Failed() {
		t.Fatalf("err %v, failed %v: %+v", err, rep.Plan.Failed(), rep.Plan.Outcomes)
	}
	if len(m.calls) != before {
		t.Errorf("ran claude without Claude Code: %v", m.calls[before:])
	}
	for _, p := range globalPaths(t, m.env.Roots.Claude) {
		if _, err := os.Stat(p); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s still there: %v", p, err)
		}
	}
	if _, ok := settingsOf(t, m).Get("cleanupPeriodDays"); ok {
		t.Error("makeit's settings values stayed")
	}
	if _, err := os.Stat(filepath.Join(m.env.Roots.Stack, "catalog")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("catalog still there: %v", err)
	}
	if _, err := os.Stat(data); err != nil {
		t.Errorf("a plugin's data folder was touched: %v", err)
	}
	st, _, err := state.LoadState(filepath.Join(m.env.Roots.Stack, "state.json"))
	if err != nil || st.Plugins["superpowers@makeit"] == nil || st.Marketplaces["makeit"] == nil ||
		len(st.Files) != 0 || len(st.Dirs) != 0 || len(st.Settings.Paths) != 0 {
		t.Errorf("state %v: plugins %v, marketplaces %v, files %v, dirs %v, settings %d; want only the plugins and marketplace",
			err, st.Plugins, st.Marketplaces, st.Files, st.Dirs, len(st.Settings.Paths))
	}
	if r := reasonOf(rep, "claude"); !strings.Contains(r, "makeit uninstall") {
		t.Errorf("kept claude: %q, want the command that finishes the job", r)
	}
	m.env.Locate = located
	rep = uninstall(t, m, UninstallOptions{})
	if rep.Plan.Failed() || called(m, "marketplace remove makeit") < 0 {
		t.Errorf("the rerun with Claude Code back did not finish: %+v", rep.Plan.Outcomes)
	}
	if _, err := os.Stat(filepath.Join(m.env.Roots.Stack, "state.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("state.json still there after the rerun: %v", err)
	}
}

// The delegate's ruling: --keep-settings hands the values to the user, and the output says so.
func TestKeepSettingsSaysTheyAreYours(t *testing.T) {
	m := installed(t)
	m.needed = map[string]bool{"superpowers@makeit": true} // something stays, so state.json stays
	rep := uninstall(t, m, UninstallOptions{KeepSettings: true})
	if r := reasonOf(rep, "settings"); r != "now yours; makeit no longer tracks them" {
		t.Errorf("kept settings: %q", r)
	}
	st, _, err := state.LoadState(filepath.Join(m.env.Roots.Stack, "state.json"))
	if err != nil || len(st.Settings.Paths) != 0 {
		t.Errorf("state %v keeps %d settings records after --keep-settings", err, len(st.Settings.Paths))
	}
}

// A value the user had before makeit and changed since is kept, and the run's report says so even
// when nothing else in settings.json changes.
func TestAdoptedChangedValueReportedAfterApply(t *testing.T) {
	m := newMachine(t)
	raw, err := catalog.Embedded.ReadFile("settings.baseline.json")
	if err != nil {
		t.Fatal(err)
	}
	base, err := jsonx.DecodeObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	saveSettings(t, m, settings.Merge(jsonx.NewObject(), settings.Desired(base, "makeit"), nil, settings.DefaultOptions()).Settings)
	apply(t, m)
	o := settingsOf(t, m)
	o.Set("cleanupPeriodDays", json.Number("999"))
	saveSettings(t, m, o)
	rep := uninstall(t, m, UninstallOptions{})
	if reasonOf(rep, "/cleanupPeriodDays") == "" {
		t.Errorf("the changed value is not in the run's kept list: %+v", rep.Kept)
	}
}
