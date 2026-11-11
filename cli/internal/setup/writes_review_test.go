//go:build !windows

package setup

import (
	"context"
	"errors"
	"slices"
	"testing"

	"makeit/internal/hookprobe"
	"makeit/internal/state"
)

// makeit rollback is the undo the finding names: it puts the write records back as they were
// before the run, so the next release that creates the same file is reported again.
func TestWritesRollbackRestoresRecord(t *testing.T) {
	e := writesEnv(t, map[string]string{"sp@makeit": fakePlugin(t, `touch SESSIONS.md`), "new@makeit": fakePlugin(t, `true`)})
	if e.gen == nil {
		t.Fatal("writesEnv has no generation")
	}
	e.st.PluginWrites = map[string]state.Writes{"sp@makeit": {Home: []string{".sp/state"}}}
	e.reload = true
	if _, err := runWrites(t, e); err == nil {
		t.Fatal("a new project file did not fail the check")
	}
	if got := e.st.PluginWrites["sp@makeit"].Project; !slices.Equal(got, []string{"SESSIONS.md"}) {
		t.Fatalf("record after the finding = %v", got)
	}
	before := e.gen.Manifest.Writes
	if old := before["sp@makeit"]; old == nil || len(old.Project) != 0 || !slices.Equal(old.Home, []string{".sp/state"}) {
		t.Fatalf("the generation does not keep sp's earlier record: %+v", before)
	}
	if old, kept := before["new@makeit"]; !kept || old != nil {
		t.Errorf("a plugin recorded for the first time must be kept as having none: %+v", before)
	}
	s := step(t, e.rollbackSteps(e.gen), "writes.rollback")
	if _, err := s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := e.st.PluginWrites["sp@makeit"]; len(got.Project) != 0 || !slices.Equal(got.Home, []string{".sp/state"}) {
		t.Errorf("record after rollback = %+v, want the earlier one", got)
	}
	if _, kept := e.st.PluginWrites["new@makeit"]; kept {
		t.Error("the record the undone run made stays")
	}
	// The next release creates the same file: reported again.
	e.reload = true
	if _, err := runWrites(t, e); err == nil {
		t.Error("the same file after a rollback was not reported again")
	}
}

// A probe that cannot run is remembered, so the check does not come back every day, and the
// first check that works after it only records.
func TestWritesProbeErrorIsRemembered(t *testing.T) {
	e := writesEnv(t, map[string]string{"sp@makeit": fakePlugin(t, `touch x`)})
	real := probeRun
	t.Cleanup(func() { probeRun = real })
	probeRun = func(context.Context, string, []string) (hookprobe.Writes, error) {
		return hookprobe.Writes{}, errors.New("no scratch folder")
	}
	if _, err := runWrites(t, e); err != nil {
		t.Fatalf("a probe that cannot run failed the step: %v", err)
	}
	if !e.st.PluginWrites["sp@makeit"].Unchecked {
		t.Fatalf("record = %+v, want it marked unchecked", e.st.PluginWrites["sp@makeit"])
	}
	s := step(t, e.pluginSteps(), "writes.check")
	if done, _ := s.Already(context.Background()); !done {
		t.Error("nothing moved: an unchecked plugin must not bring the check back")
	}
	probeRun = real
	e.reload = true
	if _, err := runWrites(t, e); err != nil {
		t.Errorf("the first check that works after a failed one must only record: %v", err)
	}
	if got := e.st.PluginWrites["sp@makeit"]; got.Unchecked || !slices.Equal(got.Project, []string{"x"}) {
		t.Errorf("record = %+v", got)
	}
}
