package setup

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"makeit/internal/state"
)

// The newest run's record cannot be read: rollback stops and says so, instead of undoing the run
// before it as if it were the last.
func TestRollbackStopsAtAnUnreadableNewerRecord(t *testing.T) {
	e, _ := newEnv(t)
	g := genWithWrite(t, e, filepath.Join(e.Roots.Claude, "settings.json"))
	n, err := state.NewGeneration(e.Roots.Stack, g.Manifest.Generation, "update", nil)
	if err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(state.GenerationsDir(e.Roots.Stack), strconv.Itoa(n.Manifest.Generation), "manifest.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := lastUndoable(e.Roots.Stack)
	if err == nil || !strings.Contains(err.Error(), strconv.Itoa(n.Manifest.Generation)) || !strings.Contains(err.Error(), "cannot be read") {
		t.Errorf("got %v, err %v; want a refusal naming generation %d", got, err, n.Manifest.Generation)
	}
}
