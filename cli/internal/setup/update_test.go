package setup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"makeit/internal/catalog"
	"makeit/internal/execx"
	"makeit/internal/fsx"
	"makeit/internal/plan"
	"makeit/internal/state"
)

// updateEnv is globalEnv run as update.
func updateEnv(t *testing.T) *env {
	t.Helper()
	e := globalEnv(t)
	e.command = "update"
	return e
}

// keptReason is the reason recorded for item, or "".
func keptReason(e *env, item string) string {
	for _, k := range e.kept {
		if k.Item == item {
			return k.Reason
		}
	}
	return ""
}

func catalogText(t *testing.T, rel string) string {
	t.Helper()
	b, err := catalog.Embedded.ReadFile("templates/global/" + rel)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func owned(content string) *state.File {
	return &state.File{Class: "owned", Sha256: fsx.Sha256([]byte(content))}
}

func TestUpdateGlobalWritesNew(t *testing.T) {
	e := updateEnv(t)
	s := step(t, e.updateGlobalSteps(), "global.rules/go.md")
	if done, err := s.Already(context.Background()); done || err != nil {
		t.Fatalf("already = %v, %v; a new catalog file must be written", done, err)
	}
	if _, err := s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r := e.st.Files["claude:rules/go.md"]; r == nil || r.PreExisted || r.Sha256 != fsx.Sha256([]byte(catalogText(t, "rules/go.md"))) {
		t.Errorf("record %+v, want owned with the catalog's sha256", r)
	}
}

func TestUpdateGlobalReplacesUnchanged(t *testing.T) {
	e := updateEnv(t)
	path := filepath.Join(e.Roots.Claude, "rules", "go.md")
	writeFile(t, path, "old rules\n")
	e.st.Files = map[string]*state.File{"claude:rules/go.md": owned("old rules\n")}
	s := step(t, e.updateGlobalSteps(), "global.rules/go.md")
	if done, _ := s.Already(context.Background()); done {
		t.Fatal("an unchanged owned file with a newer catalog version counts as current")
	}
	if _, err := s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); got != catalogText(t, "rules/go.md") {
		t.Errorf("file not replaced with the catalog's version")
	}
	ops := e.gen.Manifest.Ops
	if len(ops) != 1 || ops[0].PreImage == "" || read(t, filepath.Join(e.gen.Dir, ops[0].PreImage)) != "old rules\n" {
		t.Errorf("ops %+v, want the old file kept as a pre-image", ops)
	}
	if r := e.st.Files["claude:rules/go.md"]; r.Sha256 != fsx.Sha256([]byte(catalogText(t, "rules/go.md"))) {
		t.Errorf("record %+v, want the new sha256", r)
	}
}

func TestUpdateGlobalKeepsChanged(t *testing.T) {
	e := updateEnv(t)
	path := filepath.Join(e.Roots.Claude, "rules", "go.md")
	writeFile(t, path, "my rules\n")
	e.st.Files = map[string]*state.File{"claude:rules/go.md": owned("old rules\n")}
	if done, err := step(t, e.updateGlobalSteps(), "global.rules/go.md").Already(context.Background()); !done || err != nil {
		t.Fatalf("already = %v, %v; a file the user changed is kept", done, err)
	}
	if read(t, path) != "my rules\n" || keptReason(e, "claude:rules/go.md") != "changed since makeit wrote it" {
		t.Errorf("file %q, kept %q", read(t, path), keptReason(e, "claude:rules/go.md"))
	}
}

func TestUpdateGlobalKeepsAdopted(t *testing.T) {
	e := updateEnv(t)
	path := filepath.Join(e.Roots.Claude, "rules", "go.md")
	writeFile(t, path, "my rules\n")
	writeFile(t, filepath.Join(e.Roots.Claude, "CLAUDE.md"), catalogText(t, "CLAUDE.md"))
	e.st.Files = map[string]*state.File{
		"claude:rules/go.md": {Class: "owned", PreExisted: true},
		"claude:CLAUDE.md":   {Class: "owned", PreExisted: true},
	}
	steps := e.updateGlobalSteps()
	for _, id := range []string{"global.rules/go.md", "global.CLAUDE.md"} {
		if done, err := step(t, steps, id).Already(context.Background()); !done || err != nil {
			t.Errorf("%s already = %v, %v; an adopted file is kept", id, done, err)
		}
	}
	if read(t, path) != "my rules\n" || !strings.Contains(keptReason(e, "claude:rules/go.md"), "yours") {
		t.Errorf("file %q, kept %q", read(t, path), keptReason(e, "claude:rules/go.md"))
	}
	if r := keptReason(e, "claude:CLAUDE.md"); r != "" {
		t.Errorf("an adopted file equal to the catalog was reported: %q", r)
	}
}

func TestUpdateGlobalRemovedByYou(t *testing.T) {
	e := updateEnv(t)
	e.st.Files = map[string]*state.File{"claude:rules/go.md": owned(catalogText(t, "rules/go.md"))}
	if done, err := step(t, e.updateGlobalSteps(), "global.rules/go.md").Already(context.Background()); !done || err != nil {
		t.Fatalf("already = %v, %v; a file the user removed stays removed", done, err)
	}
	if _, err := os.Stat(filepath.Join(e.Roots.Claude, "rules", "go.md")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("removed file came back: %v", err)
	}
	if r := keptReason(e, "claude:rules/go.md"); r != "removed by you; makeit setup puts it back" {
		t.Errorf("kept %q", r)
	}
}

func TestUpdateGlobalDropped(t *testing.T) {
	e := updateEnv(t)
	gone := filepath.Join(e.Roots.Claude, "rules", "old.md")
	mine := filepath.Join(e.Roots.Claude, "rules", "old2.md")
	writeFile(t, gone, "old\n")
	writeFile(t, mine, "edited\n")
	e.st.Files = map[string]*state.File{"claude:rules/old.md": owned("old\n"), "claude:rules/old2.md": owned("old\n")}
	steps := e.updateGlobalSteps()
	s := step(t, steps, "global.drop.rules/old.md")
	if done, _ := s.Already(context.Background()); done {
		t.Fatal("an unchanged owned file the catalog dropped counts as current")
	}
	if _, err := s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(gone); !errors.Is(err, os.ErrNotExist) || e.st.Files["claude:rules/old.md"] != nil {
		t.Errorf("dropped file still there (%v) or still recorded", err)
	}
	if ops := e.gen.Manifest.Ops; len(ops) != 1 || ops[0].Kind != "remove" || ops[0].PostSha256 != nil ||
		read(t, filepath.Join(e.gen.Dir, ops[0].PreImage)) != "old\n" {
		t.Errorf("ops %+v, want a remove with the pre-image", ops)
	}
	if done, _ := step(t, steps, "global.drop.rules/old2.md").Already(context.Background()); !done {
		t.Error("a changed file the catalog dropped would be removed")
	}
	if read(t, mine) != "edited\n" || keptReason(e, "claude:rules/old2.md") != "no longer in the catalog; changed since makeit wrote it" {
		t.Errorf("kept %q", keptReason(e, "claude:rules/old2.md"))
	}
}

func TestUpdateMarketplaceRemovedByYou(t *testing.T) {
	e, f := stackEnv(t, "[]", "[]")
	e.command = "update"
	e.st.Marketplaces = map[string]*state.Marketplace{"makeit": {AddedBy: "makeit", Kind: "directory"}}
	if done, err := step(t, e.stackSteps(), "marketplace").Already(context.Background()); !done || err != nil {
		t.Fatalf("already = %v, %v; a marketplace the user removed stays removed", done, err)
	}
	for _, c := range f.argvs() {
		if strings.Contains(c, "marketplace add") {
			t.Errorf("re-added: %s", c)
		}
	}
	if r := keptReason(e, "marketplace makeit"); r != "removed by you; makeit setup puts it back" {
		t.Errorf("kept %q", r)
	}
}

func TestUpdateBundleRemovedByYou(t *testing.T) {
	e, f := stackEnv(t, "[]", "[]")
	e.command = "update"
	e.st.Plugins = map[string]*state.Plugin{"makeit-bundle@makeit": {InstalledBy: "makeit"}}
	if done, err := step(t, e.stackSteps(), "bundle").Already(context.Background()); !done || err != nil {
		t.Fatalf("already = %v, %v; a bundle the user removed stays removed", done, err)
	}
	for _, c := range f.argvs() {
		if strings.Contains(c, "plugin install") {
			t.Errorf("reinstalled: %s", c)
		}
	}
	if r := keptReason(e, "makeit-bundle@makeit"); r != "removed by you; makeit setup puts it back" {
		t.Errorf("kept %q", r)
	}
}

func TestRemoveKeepsPreImage(t *testing.T) {
	e := globalEnv(t)
	path := filepath.Join(e.Roots.Claude, "x.md")
	writeFile(t, path, "x\n")
	if err := e.remove("file.x", path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("file still there: %v", err)
	}
	ops := e.gen.Manifest.Ops
	if len(ops) != 1 || ops[0].Kind != "remove" || ops[0].PreSha256 == nil || *ops[0].PreSha256 != fsx.Sha256([]byte("x\n")) ||
		ops[0].PostSha256 != nil || read(t, filepath.Join(e.gen.Dir, ops[0].PreImage)) != "x\n" {
		t.Errorf("ops %+v", ops)
	}
}

func TestRemoveRefusesSymlink(t *testing.T) {
	e := globalEnv(t)
	target := filepath.Join(t.TempDir(), "dotfiles-go.md")
	writeFile(t, target, "mine\n")
	link := filepath.Join(e.Roots.Claude, "rules", "go.md")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := e.remove("file.go", link); !errors.Is(err, errSymlink) {
		t.Fatalf("err = %v, want errSymlink", err)
	}
	if _, err := os.Lstat(link); err != nil || read(t, target) != "mine\n" {
		t.Errorf("link or target touched: %v", err)
	}
}

func TestLaterPhaseSkippedAfterFailure(t *testing.T) {
	m := newMachine(t)
	ran := false
	build := func(*env) []phase {
		return []phase{
			{steps: []plan.Step{{ID: "a", Title: "a", Run: func(context.Context) (string, error) { return "", errors.New("boom") }}}},
			{steps: []plan.Step{{ID: "b", Title: "b", Run: func(context.Context) (string, error) { ran = true; return "", nil }}}, keepGoing: true},
		}
	}
	rep, _, err := execute(context.Background(), m.env, "test", plan.Apply, nil, nil, build)
	if err != nil {
		t.Fatal(err)
	}
	if ran || !rep.Plan.Failed() {
		t.Fatalf("ran = %v, failed = %v; a later phase must not start after a failure", ran, rep.Plan.Failed())
	}
	last := rep.Plan.Outcomes[len(rep.Plan.Outcomes)-1]
	if last.ID != "b" || last.Status != plan.Skipped || !strings.Contains(last.Detail, "an earlier step failed") {
		t.Errorf("b = %+v", last)
	}
}

// pluginEnv is stackEnv run as update, with a local catalog: the bundle declares deps, the
// marketplace file holds entries, and the makeit marketplace is listed from that directory.
func pluginEnv(t *testing.T, deps, entries, listed string) (*env, *fakeRunner) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "plugins/makeit-bundle/.claude-plugin/plugin.json"), `{"name":"makeit-bundle","dependencies":`+deps+`}`)
	writeFile(t, filepath.Join(dir, ".claude-plugin/marketplace.json"), `{"name":"makeit","plugins":`+entries+`}`)
	e, f := stackEnv(t, `[{"name":"makeit","source":"directory","path":"`+dir+`"},{"name":"claude-plugins-official","source":"github"}]`, listed)
	e.Catalog = catalog.Dir(dir)
	e.command = "update"
	g, err := state.NewGeneration(e.Roots.Stack, 0, "update", nil)
	if err != nil {
		t.Fatal(err)
	}
	e.gen = g
	return e, f
}

const (
	bundleEntry = `{"name":"makeit-bundle","source":"./plugins/makeit-bundle"}`
	spEntry     = `{"name":"superpowers","source":{"source":"github","repo":"o/s","sha":"bbb"}}`
	bundleRow   = `{"id":"makeit-bundle@makeit","version":"3.10.0","enabled":true,"readFromFolder":"/c/plugins/makeit-bundle"}`
	updated     = `{"command":"update","outcome":"ok","updateOutcome":"updated"}`
	upToDate    = `{"command":"update","outcome":"ok","updateOutcome":"up_to_date"}`
)

func ownedAt(pin string) *state.Plugin {
	return &state.Plugin{InstalledBy: "makeit", Scope: "user", Observed: state.PluginObserved{GitCommitSha: pin}}
}

func has(argvs []string, sub string) bool {
	for _, a := range argvs {
		if strings.Contains(a, sub) {
			return true
		}
	}
	return false
}

func TestOwnedCurrentIsAlready(t *testing.T) {
	e, f := pluginEnv(t, `["superpowers"]`, `[`+bundleEntry+`,{"name":"superpowers","source":{"source":"github","repo":"o/s","sha":"aaa"}}]`,
		`[`+bundleRow+`,{"id":"superpowers@makeit","version":"7.0.1","enabled":true}]`)
	e.st.Plugins = map[string]*state.Plugin{"makeit-bundle@makeit": ownedAt(""), "superpowers@makeit": ownedAt("aaa")}
	if done, err := step(t, e.pluginSteps(), "plugin.superpowers@makeit").Already(context.Background()); !done || err != nil {
		t.Errorf("already = %v, %v; the recorded pin equals the marketplace's", done, err)
	}
	if has(f.argvs(), "plugin update") {
		t.Errorf("updated a current plugin: %v", f.argvs())
	}
}

func TestOwnedBehindUpdates(t *testing.T) {
	e, f := pluginEnv(t, `["superpowers"]`, `[`+bundleEntry+`,`+spEntry+`]`,
		`[`+bundleRow+`,{"id":"superpowers@makeit","version":"7.0.1","enabled":true}]`)
	e.st.Plugins = map[string]*state.Plugin{"makeit-bundle@makeit": ownedAt(""), "superpowers@makeit": ownedAt("aaa")}
	f.answers["claude plugin update superpowers@makeit --scope user --json"] = func(execx.Cmd) execx.Result {
		f.answers["claude plugin list --json"] = text(`[` + bundleRow + `,{"id":"superpowers@makeit","version":"7.1.0","enabled":true}]`)
		return execx.Result{Stdout: updated}
	}
	s := step(t, e.pluginSteps(), "plugin.superpowers@makeit")
	if done, _ := s.Already(context.Background()); done {
		t.Fatal("a plugin behind its marketplace pin counts as current")
	}
	if _, err := s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !has(f.argvs(), "claude plugin update superpowers@makeit --scope user --json") || has(f.argvs(), " -y") {
		t.Errorf("argv %v, want the update without -y", f.argvs())
	}
	if r := e.st.Plugins["superpowers@makeit"]; r.Observed.GitCommitSha != "bbb" || r.Observed.Version != "7.1.0" {
		t.Errorf("record %+v, want pin bbb and version 7.1.0", r.Observed)
	}
	if got := e.gen.Manifest.Plugins.Updated; len(got) != 1 || got[0] != "superpowers@makeit" {
		t.Errorf("generation updated %v", got)
	}
}

func TestOwnedShownCommandRefused(t *testing.T) {
	e, f := pluginEnv(t, `["superpowers"]`, `[`+bundleEntry+`,`+spEntry+`]`, `[`+bundleRow+`,{"id":"superpowers@makeit","enabled":true}]`)
	e.st.Plugins = map[string]*state.Plugin{"makeit-bundle@makeit": ownedAt(""), "superpowers@makeit": ownedAt("aaa")}
	f.answers["claude plugin update superpowers@makeit --scope user --json"] = text(`{"command":"update","outcome":"failed","shownCommand":{"sha256":"abc123"}}`)
	if _, err := step(t, e.pluginSteps(), "plugin.superpowers@makeit").Run(context.Background()); err == nil || !strings.Contains(err.Error(), "abc123") {
		t.Errorf("err = %v, want the refused command's sha256", err)
	}
}

func TestStalePinReinstalls(t *testing.T) {
	e, f := pluginEnv(t, `["superpowers"]`, `[`+bundleEntry+`,`+spEntry+`]`, `[`+bundleRow+`,{"id":"superpowers@makeit","version":"7.0.1","enabled":true}]`)
	e.st.Plugins = map[string]*state.Plugin{"makeit-bundle@makeit": ownedAt(""), "superpowers@makeit": ownedAt("aaa")}
	f.answers["claude plugin update superpowers@makeit --scope user --json"] = text(upToDate)
	f.answers["claude plugin uninstall superpowers@makeit --scope user --keep-data --json"] = text(`{"command":"uninstall","outcome":"ok"}`)
	f.answers["claude plugin install makeit-bundle@makeit --scope user --json"] = text(ok)
	if _, err := step(t, e.pluginSteps(), "plugin.superpowers@makeit").Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	var seq []string
	for _, a := range f.argvs() {
		if strings.Contains(a, "plugin update") || strings.Contains(a, "plugin uninstall") || strings.Contains(a, "plugin install") {
			seq = append(seq, a)
		}
	}
	want := []string{"claude plugin update superpowers@makeit --scope user --json",
		"claude plugin uninstall superpowers@makeit --scope user --keep-data --json",
		"claude plugin install makeit-bundle@makeit --scope user --json"}
	if strings.Join(seq, "\n") != strings.Join(want, "\n") {
		t.Errorf("argv\n%s\nwant\n%s", strings.Join(seq, "\n"), strings.Join(want, "\n"))
	}
	if r := e.st.Plugins["superpowers@makeit"]; r.Observed.GitCommitSha != "bbb" {
		t.Errorf("pin %q, want bbb", r.Observed.GitCommitSha)
	}
}

func TestStalePinKeepsUserConfig(t *testing.T) {
	e, f := pluginEnv(t, `["superpowers"]`, `[`+bundleEntry+`,`+spEntry+`]`,
		`[`+bundleRow+`,{"id":"superpowers@makeit","version":"7.0.1","enabled":true,"hasUserConfig":true}]`)
	e.st.Plugins = map[string]*state.Plugin{"makeit-bundle@makeit": ownedAt(""), "superpowers@makeit": ownedAt("aaa")}
	f.answers["claude plugin update superpowers@makeit --scope user --json"] = text(upToDate)
	if _, err := step(t, e.pluginSteps(), "plugin.superpowers@makeit").Run(context.Background()); !errors.Is(err, plan.ErrSkip) {
		t.Fatalf("err = %v, want skipped: the plugin is kept", err)
	}
	if has(f.argvs(), "plugin uninstall") {
		t.Errorf("reinstalled a plugin with saved options: %v", f.argvs())
	}
	if r := keptReason(e, "superpowers@makeit"); !strings.Contains(r, "saved options and secrets") || !strings.Contains(r, "claude plugin uninstall superpowers@makeit") {
		t.Errorf("kept %q, want the reason and the commands", r)
	}
}

func TestAdoptedMakeitUpdatesOnly(t *testing.T) {
	cdm := `{"name":"cdm","source":{"source":"git-subdir","url":"u","path":"p","sha":"bbbbbbbbbbbbcccc"}}`
	e, f := pluginEnv(t, `["superpowers","cdm"]`, `[`+bundleEntry+`,`+spEntry+`,`+cdm+`]`,
		`[`+bundleRow+`,{"id":"superpowers@makeit","version":"7.0.1","enabled":true},{"id":"cdm@makeit","version":"bbbbbbbbbbbb-32c1cf49","enabled":true}]`)
	e.st.Plugins = map[string]*state.Plugin{"makeit-bundle@makeit": {InstalledBy: "user"}}
	f.answers["claude plugin update superpowers@makeit --scope user --json"] = text(upToDate)
	s := step(t, e.pluginSteps(), "plugins.adopted")
	if done, _ := s.Already(context.Background()); done {
		t.Fatal("an adopted plugin whose version cannot show its pin counts as current")
	}
	if _, err := s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !has(f.argvs(), "claude plugin update superpowers@makeit --scope user --json") {
		t.Errorf("superpowers not updated: %v", f.argvs())
	}
	for _, bad := range []string{"cdm@makeit", "plugin uninstall", "plugin install", "disable", "configure"} {
		if has(f.argvs(), bad) {
			t.Errorf("an adopted plugin got more than a version update (%s): %v", bad, f.argvs())
		}
	}
}

func TestAdoptedOtherMarketplaceNoCommand(t *testing.T) {
	e, f := pluginEnv(t, `["typescript-lsp@claude-plugins-official"]`, `[`+bundleEntry+`]`,
		`[`+bundleRow+`,{"id":"typescript-lsp@claude-plugins-official","version":"1.0.0","enabled":true}]`)
	e.st.Plugins = map[string]*state.Plugin{"makeit-bundle@makeit": {InstalledBy: "user"}}
	runPhases(context.Background(), []phase{{steps: e.pluginSteps(), keepGoing: true}}, plan.Options{})
	if has(f.argvs(), "typescript-lsp") && !has(f.argvs(), "plugin list") {
		t.Errorf("a command named the plugin: %v", f.argvs())
	}
	for _, a := range f.argvs() {
		if strings.Contains(a, "typescript-lsp") {
			t.Errorf("a command named the plugin: %s", a)
		}
	}
	var hint *Hint
	for i := range e.hints {
		if e.hints[i].ID == "typescript-lsp@claude-plugins-official" {
			hint = &e.hints[i]
		}
	}
	if hint == nil || hint.Rule != "auto-update" || hint.Text != "updated by Claude Code (claude-plugins-official auto-updates)" {
		t.Errorf("hint %+v", hint)
	}
}

func TestBundleDepsInstallsMissing(t *testing.T) {
	e, f := pluginEnv(t, `["superpowers","newdep"]`, `[`+bundleEntry+`,`+spEntry+`,{"name":"newdep","source":"./plugins/newdep"}]`,
		`[`+bundleRow+`,{"id":"superpowers@makeit","version":"7.0.1","enabled":true}]`)
	e.st.Plugins = map[string]*state.Plugin{"makeit-bundle@makeit": ownedAt(""), "superpowers@makeit": ownedAt("bbb")}
	f.answers["claude plugin install makeit-bundle@makeit --scope user --json"] = func(execx.Cmd) execx.Result {
		f.answers["claude plugin list --json"] = text(`[` + bundleRow + `,{"id":"superpowers@makeit","version":"7.0.1","enabled":true},{"id":"newdep@makeit","version":"1.0.0","enabled":true}]`)
		return execx.Result{Stdout: ok}
	}
	s := step(t, e.pluginSteps(), "bundle.deps")
	if done, _ := s.Already(context.Background()); done {
		t.Fatal("a missing declared dependency counts as installed")
	}
	if _, err := s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r := e.st.Plugins["newdep@makeit"]; r == nil || r.InstalledBy != "makeit" {
		t.Errorf("newdep recorded as %+v, want makeit's", r)
	}
}

func TestDropUndeclaredOwned(t *testing.T) {
	e, f := pluginEnv(t, `["superpowers"]`, `[`+bundleEntry+`,`+spEntry+`]`,
		`[`+bundleRow+`,{"id":"superpowers@makeit","enabled":true},{"id":"old@makeit","enabled":true},{"id":"kept@makeit","enabled":true}]`)
	e.st.Plugins = map[string]*state.Plugin{"makeit-bundle@makeit": ownedAt(""), "superpowers@makeit": ownedAt("bbb"),
		"old@makeit": ownedAt(""), "kept@makeit": ownedAt("")}
	f.answers["claude plugin prune --dry-run --scope user"] = text("1 auto-installed plugin no longer needed at user scope:\n  old@makeit (1.0.0)\n(dry run — nothing removed)\n")
	f.answers["claude plugin uninstall old@makeit --scope user --keep-data --json"] = text(`{"command":"uninstall","outcome":"ok"}`)
	steps := e.pluginSteps()
	if _, err := step(t, steps, "plugin.drop.old@makeit").Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.st.Plugins["old@makeit"] != nil || len(e.gen.Manifest.Plugins.Uninstalled) != 1 {
		t.Errorf("old@makeit still recorded or not in the generation: %+v", e.gen.Manifest.Plugins)
	}
	if _, err := step(t, steps, "plugin.drop.kept@makeit").Run(context.Background()); !errors.Is(err, plan.ErrSkip) {
		t.Errorf("err = %v, want skipped: another plugin still needs it", err)
	}
	if has(f.argvs(), "uninstall kept@makeit") || keptReason(e, "kept@makeit") != "another installed plugin still needs it" {
		t.Errorf("kept@makeit: %v, kept %q", f.argvs(), keptReason(e, "kept@makeit"))
	}
}

func TestRefreshRunsBeforePluginSteps(t *testing.T) {
	e, f := pluginEnv(t, `["superpowers"]`, `[`+bundleEntry+`,`+spEntry+`]`, `[`+bundleRow+`,{"id":"superpowers@makeit","enabled":true}]`)
	f.answers["claude plugin marketplace update makeit --json"] = text(`{"command":"marketplace-update","outcome":"ok"}`)
	steps := e.pluginSteps()
	if steps[0].ID != "marketplace.refresh" {
		t.Fatalf("first plugin step %s, want the refresh", steps[0].ID)
	}
	if done, _ := steps[0].Already(context.Background()); done {
		t.Fatal("refresh skipped with a pinned makeit plugin installed")
	}
	if _, err := steps[0].Run(context.Background()); err != nil || !has(f.argvs(), "claude plugin marketplace update makeit --json") {
		t.Errorf("refresh: %v, %v", err, f.argvs())
	}
	e2, _ := pluginEnv(t, `[]`, `[`+bundleEntry+`]`, `[`+bundleRow+`]`)
	if done, _ := e2.pluginSteps()[0].Already(context.Background()); !done {
		t.Error("refresh runs with only in-place makeit plugins installed")
	}
}

func TestPluginFailureKeepsGoing(t *testing.T) {
	e, f := pluginEnv(t, `["a","b"]`, `[`+bundleEntry+`,{"name":"a","source":{"source":"github","repo":"o/a","sha":"a2"}},{"name":"b","source":{"source":"github","repo":"o/b","sha":"b2"}}]`,
		`[`+bundleRow+`,{"id":"a@makeit","enabled":true},{"id":"b@makeit","enabled":true}]`)
	e.st.Plugins = map[string]*state.Plugin{"makeit-bundle@makeit": ownedAt(""), "a@makeit": ownedAt("a1"), "b@makeit": ownedAt("b1")}
	f.answers["claude plugin marketplace update makeit --json"] = text(`{"command":"marketplace-update","outcome":"ok"}`)
	f.answers["claude plugin update a@makeit --scope user --json"] = text(`{"command":"update","outcome":"failed","message":"network down"}`)
	f.answers["claude plugin update b@makeit --scope user --json"] = text(updated)
	rep := runPhases(context.Background(), []phase{{steps: e.pluginSteps(), keepGoing: true}}, plan.Options{})
	if !rep.Failed() || !has(f.argvs(), "claude plugin update b@makeit") {
		t.Errorf("failed = %v, argv %v: b must still be updated after a fails", rep.Failed(), f.argvs())
	}
}

// The delegate's guard: the moved-pin reinstall copies the plugin's data into the generation
// before the uninstall.
func TestStalePinBacksUpData(t *testing.T) {
	e, f := pluginEnv(t, `["superpowers"]`, `[`+bundleEntry+`,`+spEntry+`]`, `[`+bundleRow+`,{"id":"superpowers@makeit","version":"7.0.1","enabled":true}]`)
	e.st.Plugins = map[string]*state.Plugin{"makeit-bundle@makeit": ownedAt(""), "superpowers@makeit": ownedAt("aaa")}
	writeFile(t, filepath.Join(e.Roots.Claude, "plugins", "data", "superpowers-makeit", "marker"), "kept\n")
	f.answers["claude plugin update superpowers@makeit --scope user --json"] = text(upToDate)
	backedUp := false
	f.answers["claude plugin uninstall superpowers@makeit --scope user --keep-data --json"] = func(execx.Cmd) execx.Result {
		_, err := os.Stat(filepath.Join(e.gen.Dir, "data", "superpowers-makeit", "marker"))
		backedUp = err == nil
		return execx.Result{Stdout: `{"command":"uninstall","outcome":"ok"}`}
	}
	f.answers["claude plugin install makeit-bundle@makeit --scope user --json"] = text(ok)
	if _, err := step(t, e.pluginSteps(), "plugin.superpowers@makeit").Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !backedUp {
		t.Error("the plugin was uninstalled before its data was copied into the generation")
	}
}

// adoptedMachine had the makeit marketplace, the bundle and every global file before setup.
func adoptedMachine(t *testing.T) *fakeMachine {
	t.Helper()
	m := newMachine(t)
	m.markets = []string{"makeit", "claude-plugins-official"}
	m.plugins = []string{"makeit-bundle@makeit"}
	for _, p := range globalPaths(t, m.env.Roots.Claude) {
		writeFile(t, p, "mine\n")
	}
	apply(t, m)
	return m
}

// Final review I1: what the user removed stays removed, adopted or not.
func TestUpdateLeavesRemovedAdoptedBundle(t *testing.T) {
	m := adoptedMachine(t)
	m.plugins = nil
	before := len(m.calls)
	rep, err := Update(context.Background(), m.env, plan.Apply, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range m.calls[before:] {
		if strings.Contains(c, "plugin install makeit-bundle@makeit") {
			t.Errorf("update reinstalled the adopted bundle the user removed: %s", c)
		}
	}
	if r := keptReason(&env{kept: rep.Kept}, "makeit-bundle@makeit"); r != removedByYou {
		t.Errorf("kept %q", r)
	}
}

func TestUpdateLeavesRemovedAdoptedMarketplace(t *testing.T) {
	m := adoptedMachine(t)
	m.markets = []string{"claude-plugins-official"}
	m.plugins = nil
	before := len(m.calls)
	if _, err := Update(context.Background(), m.env, plan.Apply, nil, nil); err != nil {
		t.Fatal(err)
	}
	for _, c := range m.calls[before:] {
		if strings.Contains(c, "marketplace add") && strings.Contains(c, ".makeit") {
			t.Errorf("update re-added the makeit marketplace the user removed: %s", c)
		}
	}
	if _, err := os.Stat(filepath.Join(m.env.Roots.Stack, "catalog")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("update wrote a catalog for a marketplace the user removed: %v", err)
	}
}

// Final review I2: a new binary moves a pin. The plan shown for consent lists the plugin update
// the apply then runs, so its pins come from the binary's catalog, not the old copy on disk.
func TestUpdatePlanShowsPinMove(t *testing.T) {
	m := installed(t)
	const newPin, oldPin = "2ce8aa15d05bd57213e6f746f953aafb7ded7a8b", "1111111111111111111111111111111111111111"
	mp := filepath.Join(m.env.Roots.Stack, "catalog", ".claude-plugin", "marketplace.json")
	b, err := os.ReadFile(mp)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, mp, strings.Replace(string(b), newPin, oldPin, 1))
	writeFile(t, filepath.Join(m.env.Roots.Stack, "catalog", ".makeit-catalog.json"), `{"digest":"old"}`)
	statePath := filepath.Join(m.env.Roots.Stack, "state.json")
	sb, err := os.ReadFile(statePath)
	if err != nil || !strings.Contains(string(sb), newPin) {
		t.Fatalf("state does not record superpowers' pin: %v", err)
	}
	writeFile(t, statePath, strings.ReplaceAll(string(sb), newPin, oldPin))
	inner := m.env.Run
	m.env.Run = func(ctx context.Context, c execx.Cmd) (execx.Result, error) {
		cmd := strings.Join(c.Argv, " ")
		switch {
		case strings.HasPrefix(cmd, "/usr/bin/claude plugin update "):
			return execx.Result{Stdout: `{"command":"update","outcome":"ok","updateOutcome":"updated"}`}, nil
		case strings.HasPrefix(cmd, "/usr/bin/claude plugin marketplace update "):
			return execx.Result{Stdout: `{"command":"marketplace-update","outcome":"ok"}`}, nil
		}
		return inner(ctx, c)
	}
	var shown []plan.Outcome
	confirm := func(outs []plan.Outcome) (bool, error) { shown = outs; return true, nil }
	if _, err := Update(context.Background(), m.env, plan.Prompt, confirm, nil); err != nil {
		t.Fatal(err)
	}
	for _, o := range shown {
		if o.ID == "plugin.superpowers@makeit" && o.Status != plan.WouldChange {
			t.Errorf("the plan shows superpowers@makeit as %s; its pin moved", o.Status)
		}
	}
}

// Final review I3: right after setup, with nothing new, update has nothing to do: no consent, no
// generation, no restart.
// converged is a machine right after setup whose plugin list reports every plugin installed, so
// a plain update has nothing to do.
func converged(t *testing.T) *fakeMachine {
	t.Helper()
	m := newMachine(t)
	m.deps = []string{"makeit@makeit", "superpowers@makeit", "ponytail@makeit", "chrome-devtools-mcp@makeit",
		"docs-mcp-server@makeit", "deja-vu@makeit", "security-audit@makeit",
		"typescript-lsp@claude-plugins-official", "pyright-lsp@claude-plugins-official", "rust-analyzer-lsp@claude-plugins-official", "gopls-lsp@claude-plugins-official"}
	inner := m.run
	m.env.Run = func(ctx context.Context, c execx.Cmd) (execx.Result, error) {
		if strings.Join(c.Argv, " ") == "/usr/bin/claude plugin list --json" {
			var rows []string
			for _, p := range m.plugins {
				row := `{"id":"` + p + `","enabled":true,"version":"1.0.0","scope":"user"`
				if p == "makeit-bundle@makeit" || p == "makeit@makeit" {
					row += `,"readFromFolder":"/x"`
				}
				rows = append(rows, row+"}")
			}
			return execx.Result{Stdout: "[" + strings.Join(rows, ",") + "]"}, nil
		}
		return inner(ctx, c)
	}
	apply(t, m)
	return m
}

func TestUpdateAfterSetupIsNoop(t *testing.T) {
	m := converged(t)
	rep, err := Update(context.Background(), m.env, plan.NeedConsent, nil, nil)
	if errors.Is(err, ErrConsent) {
		for _, o := range rep.Plan.Outcomes {
			if o.Status != plan.Already {
				t.Logf("%s %s", o.ID, o.Status)
			}
		}
		t.Fatalf("update right after setup needs consent: %s", rep.Plan.Summary())
	}
	if rep, _ := Update(context.Background(), m.env, plan.Apply, nil, nil); rep.Generation != 0 {
		t.Errorf("a no-op update made generation %d", rep.Generation)
	}
}

// An adopted plugin whose version cannot show its pin is updated once; the pin is recorded, and
// the next update leaves it alone until the pin moves.
func TestAdoptedUpdateRecordsPin(t *testing.T) {
	e, f := pluginEnv(t, `["superpowers"]`, `[`+bundleEntry+`,`+spEntry+`]`,
		`[`+bundleRow+`,{"id":"superpowers@makeit","version":"7.0.1","enabled":true}]`)
	e.st.Plugins = map[string]*state.Plugin{"makeit-bundle@makeit": {InstalledBy: "user"}}
	f.answers["claude plugin update superpowers@makeit --scope user --json"] = text(upToDate)
	if _, err := step(t, e.pluginSteps(), "plugins.adopted").Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r := e.st.Plugins["superpowers@makeit"]; r == nil || r.InstalledBy != "user" || r.Observed.GitCommitSha != "bbb" {
		t.Fatalf("record %+v, want the adopted plugin at pin bbb", r)
	}
	e.inputs = nil
	if done, err := step(t, e.pluginSteps(), "plugins.adopted").Already(context.Background()); !done || err != nil {
		t.Errorf("already = %v, %v; the pin did not move", done, err)
	}
}

// Final review I4: an owned file the user linked into dotfiles (stow, per file) is theirs; update
// does not rewrite the link's target.
func TestUpdateKeepsLinkedFile(t *testing.T) {
	e := updateEnv(t)
	target := filepath.Join(t.TempDir(), "dotfiles", "go.md")
	writeFile(t, target, "old rules\n")
	link := filepath.Join(e.Roots.Claude, "rules", "go.md")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	e.st.Files = map[string]*state.File{"claude:rules/go.md": owned("old rules\n")}
	if done, err := step(t, e.updateGlobalSteps(), "global.rules/go.md").Already(context.Background()); !done || err != nil {
		t.Fatalf("already = %v, %v; a linked file is the user's", done, err)
	}
	if read(t, target) != "old rules\n" || keptReason(e, "claude:rules/go.md") != "a link, so it is yours" {
		t.Errorf("target %q, kept %q", read(t, target), keptReason(e, "claude:rules/go.md"))
	}
}

// The delegate's ruling: the restart hint only when a plugin version or pin actually moved.
func TestReloadOnlyWhenSomethingMoved(t *testing.T) {
	for outcome, want := range map[string]bool{upToDate: false, updated: true} {
		e, f := pluginEnv(t, `["superpowers"]`, `[`+bundleEntry+`,`+spEntry+`]`,
			`[`+bundleRow+`,{"id":"superpowers@makeit","version":"7.0.1","enabled":true}]`)
		e.st.Plugins = map[string]*state.Plugin{"makeit-bundle@makeit": {InstalledBy: "user"}}
		f.answers["claude plugin update superpowers@makeit --scope user --json"] = text(outcome)
		if _, err := step(t, e.pluginSteps(), "plugins.adopted").Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if e.reload != want {
			t.Errorf("%s: reload = %v, want %v", outcome, e.reload, want)
		}
	}
	e, f := pluginEnv(t, `["superpowers"]`, `[`+bundleEntry+`,`+spEntry+`]`,
		`[`+bundleRow+`,{"id":"superpowers@makeit","version":"7.0.1","enabled":true}]`)
	e.st.Plugins = map[string]*state.Plugin{"makeit-bundle@makeit": ownedAt(""), "superpowers@makeit": ownedAt("aaa")}
	f.answers["claude plugin update superpowers@makeit --scope user --json"] = text(updated)
	if _, err := step(t, e.pluginSteps(), "plugin.superpowers@makeit").Run(context.Background()); err != nil || !e.reload {
		t.Errorf("owned plugin updated: %v, reload = %v", err, e.reload)
	}
}

// Review Focus 2 on a machine with nothing else to do: the same files from a git source still
// record the switch, so the next plain update follows it.
func TestIdenticalSwitchOnConvergedMachine(t *testing.T) {
	m := converged(t)
	tr, err := catalog.EmbeddedTree()
	if err != nil {
		t.Fatal(err)
	}
	req := "latest"
	m.env.CatalogTree = tr
	m.env.CatalogRecord = state.Catalog{Kind: "git", Location: "https://h/x.git", Requested: &req, Resolved: strings.Repeat("a", 40), RefKind: "latest", Tag: "v1.0.0"}
	if _, err := Update(context.Background(), m.env, plan.Apply, nil, nil); err != nil {
		t.Fatal(err)
	}
	st, _, err := state.LoadState(filepath.Join(m.env.Roots.Stack, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Catalog.Kind != "git" || st.Catalog.Tag != "v1.0.0" {
		t.Errorf("the switch was not recorded: %+v", st.Catalog)
	}
}
