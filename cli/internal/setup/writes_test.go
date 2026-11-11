//go:build !windows

package setup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"makeit/internal/claudecli"
	"makeit/internal/execx"
	"makeit/internal/plan"
	"makeit/internal/state"
)

// fakePlugin is a plugin folder whose SessionStart hook runs script.
func fakePlugin(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "hooks", "hooks.json"), `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"sh ${CLAUDE_PLUGIN_ROOT}/hook.sh"}]}]}}`)
	writeFile(t, filepath.Join(dir, "hook.sh"), script+"\n")
	return dir
}

func pluginRows(dirs map[string]string) string {
	var rows []string
	for id, dir := range dirs {
		rows = append(rows, `{"id":"`+id+`","version":"1.0.0","scope":"user","enabled":true,"installPath":"`+dir+`"}`)
	}
	slices.Sort(rows)
	return "[" + strings.Join(rows, ",") + "]"
}

// writesEnv is an update in its apply pass with the plugins of dirs (id to folder) installed.
func writesEnv(t *testing.T, dirs map[string]string) *env {
	t.Helper()
	e, _ := pluginEnv(t, `[]`, `[]`, pluginRows(dirs))
	return e
}

func runWrites(t *testing.T, e *env) (string, error) {
	t.Helper()
	s := step(t, e.pluginSteps(), "writes.check")
	if done, err := s.Already(context.Background()); done || err != nil {
		t.Fatalf("already = %v, %v; the check must run", done, err)
	}
	return s.Run(context.Background())
}

func TestWritesCheckIsLast(t *testing.T) {
	e := writesEnv(t, nil)
	steps := e.pluginSteps()
	if last := steps[len(steps)-1]; last.ID != "writes.check" || last.Title != "Check what the plugins' session hooks write" {
		t.Errorf("last plugin step = %s %q", last.ID, last.Title)
	}
}

func TestWritesFirstRunRecords(t *testing.T) {
	e := writesEnv(t, map[string]string{"sp@makeit": fakePlugin(t, `touch in-project; mkdir -p ~/.sp && touch ~/.sp/state`)})
	if _, err := runWrites(t, e); err != nil {
		t.Fatal(err)
	}
	got := e.st.PluginWrites["sp@makeit"]
	if !slices.Equal(got.Project, []string{"in-project"}) || !slices.Equal(got.Home, []string{".sp/state"}) || len(e.notes) != 0 {
		t.Errorf("record %+v, notes %v; a first check records and reports nothing", got, e.notes)
	}
	// Recorded and nothing moved: the next run costs no probe.
	if done, err := step(t, e.pluginSteps(), "writes.check").Already(context.Background()); !done || err != nil {
		t.Errorf("already = %v, %v", done, err)
	}
}

func TestWritesNewProjectFileFails(t *testing.T) {
	e := writesEnv(t, map[string]string{
		"sp@makeit":  fakePlugin(t, `echo past >SESSIONS.md; mkdir -p .sp && touch .sp/log`),
		"dv@makeit":  fakePlugin(t, `touch dv.txt`),
		"old@makeit": fakePlugin(t, `touch known`),
	})
	e.st.PluginWrites = map[string]state.Writes{"sp@makeit": {}, "dv@makeit": {}, "old@makeit": {Project: []string{"known"}}, "gone@makeit": {}}
	e.reload = true
	_, err := runWrites(t, e)
	want := "dv's session hooks now create dv.txt in the project you work in; undo this update with makeit rollback; " +
		"sp's session hooks now create .sp/log, SESSIONS.md in the project you work in; undo this update with makeit rollback"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v\nwant  %s", err, want)
	}
	if got := e.st.PluginWrites["sp@makeit"].Project; !slices.Equal(got, []string{".sp/log", "SESSIONS.md"}) {
		t.Errorf("record %v; the record takes the new result so the finding is reported once", got)
	}
	if _, kept := e.st.PluginWrites["gone@makeit"]; kept {
		t.Error("the record of a plugin that is gone stays")
	}
	// The same hooks again: nothing new.
	if _, err := runWrites(t, e); err != nil || len(e.notes) != 0 {
		t.Errorf("second check: %v, notes %v", err, e.notes)
	}
}

func TestWritesNewHomeFileIsANote(t *testing.T) {
	e := writesEnv(t, map[string]string{"sp@makeit": fakePlugin(t, `touch ~/.a ~/.b; exit 7`)})
	e.st.PluginWrites = map[string]state.Writes{"sp@makeit": {Home: []string{".a"}}}
	e.reload = true
	if _, err := runWrites(t, e); err != nil {
		t.Fatalf("a new home file or a failing hook must not fail the step: %v", err)
	}
	if want := []string{"sp's session hooks now write in your home folder: .b"}; !slices.Equal(e.notes, want) {
		t.Errorf("notes = %q, want %q", e.notes, want)
	}
	e.notes = nil
	if _, err := runWrites(t, e); err != nil || len(e.notes) != 0 {
		t.Errorf("second check: %v, notes %v; a finding is reported once", err, e.notes)
	}
}

func TestWritesAlready(t *testing.T) {
	dir := fakePlugin(t, `touch x`)
	already := func(e *env) bool {
		t.Helper()
		done, err := step(t, e.pluginSteps(), "writes.check").Already(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return done
	}
	recorded := func() *env {
		e := writesEnv(t, map[string]string{"sp@makeit": dir})
		e.st.PluginWrites = map[string]state.Writes{"sp@makeit": {}}
		return e
	}
	if e := recorded(); !already(e) {
		t.Error("nothing moved and every plugin has a record: no probe")
	}
	e := recorded()
	e.reload = true
	if already(e) {
		t.Error("a plugin moved: the check runs")
	}
	e = recorded()
	e.changes = []state.Change{{Kind: "tool", Item: "rg"}}
	if already(e) {
		t.Error("a tool moved: the check runs")
	}
	e = recorded()
	e.changes = []state.Change{{Kind: "remove", Item: "docs index"}}
	if !already(e) {
		t.Error("a removed folder moves no plugin and no tool")
	}
	// The dry pass: nothing has moved yet, so a step with work to do stands for it.
	e = recorded()
	e.gen = nil
	if !already(e) {
		t.Error("dry pass with nothing to do: no probe")
	}
	e.pending = true
	if already(e) {
		t.Error("dry pass with a step to apply: the check is planned")
	}
	e = recorded()
	e.reload, e.claude = true, nil
	if !already(e) {
		t.Error("without Claude Code there is nothing to check")
	}
	e = recorded()
	e.reload, e.Platform.OS = true, "windows"
	if !already(e) {
		t.Error("the check does not run on Windows")
	}
	// Not probed: another marketplace, another scope, a folder that is not there.
	e, _ = pluginEnv(t, `[]`, `[]`, `[{"id":"a@other","scope":"user","installPath":"`+dir+`"},{"id":"b@makeit","scope":"project","installPath":"`+dir+`"},{"id":"c@makeit","scope":"user","installPath":"/no/such/folder"},{"id":"d@makeit","scope":"user"}]`)
	e.reload = true
	if !already(e) {
		t.Error("no plugin to probe: nothing to do")
	}
}

func TestWritesNeverProbesMakeit(t *testing.T) {
	makeit := fakePlugin(t, `touch "$CLAUDE_PLUGIN_ROOT/ran"`)
	inPlace := fakePlugin(t, `touch "$CLAUDE_PLUGIN_ROOT/ran"`)
	e, _ := pluginEnv(t, `[]`, `[]`, `[{"id":"makeit@makeit","scope":"user","readFromFolder":"`+makeit+`"},{"id":"dv@makeit","scope":"user","readFromFolder":"`+inPlace+`","installPath":"/no/such"}]`)
	if _, err := runWrites(t, e); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(makeit, "ran")); !errors.Is(err, os.ErrNotExist) {
		t.Error("the makeit plugin's hooks ran; they send notifications")
	}
	if _, err := os.Stat(filepath.Join(inPlace, "ran")); err != nil {
		t.Errorf("a plugin loaded in place is probed in its folder: %v", err)
	}
	if _, rec := e.st.PluginWrites["makeit@makeit"]; rec || len(e.st.PluginWrites) != 1 {
		t.Errorf("records %v", e.st.PluginWrites)
	}
}

func TestWritesSettingsEnvReachesHooks(t *testing.T) {
	dir := fakePlugin(t, `echo "$PROBE_FROM_SETTINGS $PROBE_FROM_PROCESS" >"$CLAUDE_PLUGIN_ROOT/seen"`)
	e := writesEnv(t, map[string]string{"sp@makeit": dir})
	t.Setenv("PROBE_FROM_PROCESS", "process")
	t.Setenv("PROBE_FROM_SETTINGS", "overridden")
	writeFile(t, filepath.Join(e.Roots.Claude, "settings.json"), `{"env":{"PROBE_FROM_SETTINGS":"settings","N":1}}`)
	if _, err := runWrites(t, e); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, "seen")); got != "settings process\n" {
		t.Errorf("the hook saw %q", got)
	}
}

// A whole update: the plan shows the check when something is to apply, the failure is the step's
// outcome, and the new record is on disk although the step failed.
func TestWritesFailureThroughExecute(t *testing.T) {
	m := newMachine(t)
	dir := fakePlugin(t, `touch SESSIONS.md`)
	inner := m.env.Run
	m.env.Run = func(ctx context.Context, c execx.Cmd) (execx.Result, error) {
		if strings.HasSuffix(strings.Join(c.Argv, " "), "claude plugin list --json") {
			return execx.Result{Stdout: pluginRows(map[string]string{"sp@makeit": dir})}, nil
		}
		return inner(ctx, c)
	}
	writeFile(t, filepath.Join(m.env.Roots.Stack, "state.json"), `{"schemaVersion":1,"revision":1,"pluginWrites":{"sp@makeit":{"project":[],"home":[]}}}`)
	moves := true
	build := func(en *env) []phase {
		en.claude = &claudecli.CLI{Path: "/usr/bin/claude", Run: m.env.Run}
		move := plan.Step{ID: "plugin.sp@makeit", Title: "Update sp",
			Already: func(context.Context) (bool, error) { return !moves, nil },
			Run:     func(context.Context) (string, error) { en.reload = true; return "", nil }}
		return []phase{{steps: []plan.Step{move, en.writesStep()}, keepGoing: true}}
	}
	rep, _, err := execute(context.Background(), m.env, "update", plan.Apply, nil, nil, build)
	if err != nil {
		t.Fatal(err)
	}
	last := rep.Plan.Outcomes[len(rep.Plan.Outcomes)-1]
	want := "sp's session hooks now create SESSIONS.md in the project you work in; undo this update with makeit rollback"
	if last.ID != "writes.check" || last.Status != plan.Failed || last.Detail != want {
		t.Fatalf("outcome %+v", last)
	}
	st, _, err := state.LoadState(filepath.Join(m.env.Roots.Stack, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := st.PluginWrites["sp@makeit"].Project; !slices.Equal(got, []string{"SESSIONS.md"}) {
		t.Errorf("saved record %v; the state is saved even when the step fails", got)
	}
	// The next day nothing moves: nothing to do, no probe, no generation.
	moves = false
	rep, _, err = execute(context.Background(), m.env, "update", plan.Apply, nil, nil, build)
	if err != nil || rep.Generation != 0 || rep.Plan.Failed() {
		t.Errorf("second run: %v, generation %d, %s", err, rep.Generation, rep.Plan.Summary())
	}
}
