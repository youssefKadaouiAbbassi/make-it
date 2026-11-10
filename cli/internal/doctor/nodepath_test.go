package doctor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func nodePathChecks(t *testing.T, dist bool, onPath string) []Check {
	t.Helper()
	m, _ := healthy(t)
	m.NodeDist, m.NodeLink, m.NodePath = dist, "/home/u/.local/bin/node", onPath
	return Run(context.Background(), m)
}

// makeit installed node from nodejs.org but another node comes first on PATH: warn with the fix.
func TestNodePathWarnsWhenAnotherNodeWins(t *testing.T) {
	for _, onPath := range []string{"/usr/bin/node", ""} {
		got := byID(t, nodePathChecks(t, true, onPath), "node.path")
		if got.Level != Warn || !strings.Contains(got.Message, "/home/u/.local/bin/node") || !strings.Contains(got.Fix, "move $HOME/.local/bin before /usr/bin in your shell profile") {
			t.Errorf("on PATH %q: check %+v", onPath, got)
		}
	}
}

func TestNodePathQuietOtherwise(t *testing.T) {
	for name, checks := range map[string][]Check{
		"makeit's node first": nodePathChecks(t, true, "/home/u/.local/bin/node"),
		"not makeit's":        nodePathChecks(t, false, "/usr/bin/node"),
	} {
		for _, c := range checks {
			if c.ID == "node.path" {
				t.Errorf("%s: unexpected %+v", name, c)
			}
		}
	}
}

// The same file reached through a symlinked home (or any other spelling) is the same node.
func TestNodePathComparesFiles(t *testing.T) {
	real := t.TempDir()
	if err := os.WriteFile(filepath.Join(real, "node"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	m, _ := healthy(t)
	m.NodeDist, m.NodeLink, m.NodePath = true, filepath.Join(real, "node"), filepath.Join(alias, "node")
	for _, c := range Run(context.Background(), m) {
		if c.ID == "node.path" {
			t.Errorf("unexpected %+v", c)
		}
	}
}
