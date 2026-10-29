package paths

import (
	"path/filepath"
	"testing"
)

func TestRootsRoundTrip(t *testing.T) {
	home := t.TempDir()
	r := Roots{Home: home, Claude: filepath.Join(home, ".claude"), Stack: filepath.Join(home, ".makeit")}
	cases := map[string]string{
		filepath.Join(home, ".claude", "rules", "x.md"): "claude:rules/x.md",
		filepath.Join(home, ".makeit", "catalog"):       "stack:catalog",
		filepath.Join(home, ".local", "bin", "cs"):      "home:.local/bin/cs",
		filepath.Join(home, ".claude"):                  "claude:",
	}
	for abs, want := range cases {
		if got := r.Portable(abs); got != want {
			t.Errorf("Portable(%s) = %s, want %s", abs, got, want)
		}
		if got := r.Abs(want); got != abs {
			t.Errorf("Abs(%s) = %s, want %s", want, got, abs)
		}
	}
	outside := filepath.Join(filepath.Dir(home), "elsewhere")
	if got := r.Portable(outside); got != filepath.ToSlash(outside) {
		t.Errorf("outside: %s", got)
	}
	if got := r.Abs("/abs/path"); got != "/abs/path" {
		t.Errorf("unrooted: %s", got)
	}
}

func TestEnvironmentOverrides(t *testing.T) {
	d := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(d, "cc"))
	t.Setenv("MAKEIT_DIR", filepath.Join(d, "cs"))
	r, err := Current()
	if err != nil {
		t.Fatal(err)
	}
	if r.Claude != filepath.Join(d, "cc") || r.Stack != filepath.Join(d, "cs") {
		t.Fatalf("%+v", r)
	}
}
