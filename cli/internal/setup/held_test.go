package setup

import (
	"testing"

	"makeit/internal/settings"
)

// A setting the user holds reads as the setting's name and what happens to it, not a JSON pointer.
func TestKeepHeldWording(t *testing.T) {
	e := &env{}
	e.keepHeld([]settings.Change{{Path: "/model", Action: settings.Held}, {Path: "/permissions/defaultMode", Action: settings.KeptTheirs}})
	want := []Kept{
		{Item: "settings.json: model", Reason: "your value stays; makeit's is not applied"},
		{Item: "settings.json: permissions.defaultMode", Reason: "your value stays; makeit's is not applied"},
	}
	if len(e.kept) != len(want) {
		t.Fatalf("kept %+v", e.kept)
	}
	for i := range want {
		if e.kept[i] != want[i] {
			t.Errorf("kept[%d] = %+v, want %+v", i, e.kept[i], want[i])
		}
	}
}
