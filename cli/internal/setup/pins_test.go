package setup

import (
	"testing"

	"makeit/internal/state"
)

// A pin move whose plugin did not update (its step failed, or never ran) is not recorded: the next
// session would announce it and rollback would plan to undo it.
func TestPinsKeptOnlyWhenUpdated(t *testing.T) {
	pins := []state.PinMove{{ID: "superpowers@makeit", ToTag: "v7.1.0"}, {ID: "ponytail@makeit", ToTag: "v5.3.3"}}
	got := updatedPins(pins, []string{"ponytail@makeit"})
	if len(got) != 1 || got[0].ID != "ponytail@makeit" {
		t.Errorf("pins %+v, want only ponytail", got)
	}
	if got := updatedPins(pins, nil); len(got) != 0 {
		t.Errorf("pins %+v, want none when nothing updated", got)
	}
}
