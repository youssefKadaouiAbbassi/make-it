package settings

import (
	"testing"

	"makeit/internal/jsonx"
)

func TestShow(t *testing.T) {
	if got := Show("/env/API_KEY", "s3cret"); got != "(secret)" {
		t.Errorf("secret shown as %q", got)
	}
	if got := Show("/model", "claude-opus-5-5"); got != `"claude-opus-5-5"` {
		t.Errorf("model shown as %q", got)
	}
}

func TestDesiredDropsOwnMarketplace(t *testing.T) {
	b, err := jsonx.DecodeObject([]byte(`{"extraKnownMarketplaces":{"makeit":{"autoUpdate":true},"claude-plugins-official":{"autoUpdate":true}}}`))
	if err != nil {
		t.Fatal(err)
	}
	m, _ := Desired(b, "makeit").Get("extraKnownMarketplaces")
	if keys := m.(*jsonx.Object).Keys(); len(keys) != 1 || keys[0] != "claude-plugins-official" {
		t.Errorf("marketplaces %v, want only claude-plugins-official", keys)
	}
}
