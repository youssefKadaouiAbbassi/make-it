package doctor

import "os"

// nodePath warns when makeit installed node from nodejs.org but the node on PATH is another one
// (/usr/bin before ~/.local/bin), so the hooks and the browser MCP would run that one.
func (r *report) nodePath(m Machine) {
	if !m.NodeDist || sameNode(m.NodePath, m.NodeLink) {
		return
	}
	found := "no node is on PATH"
	if m.NodePath != "" {
		found = "PATH finds " + m.NodePath + " first"
	}
	r.add("node.path", "tools", Warn, "makeit installed node at "+m.NodeLink+", but "+found,
		"move $HOME/.local/bin before /usr/bin in your shell profile (PATH), then open a new terminal")
}

// sameNode: a and b are the same file, however each path is spelled (a symlinked home, a link).
func sameNode(a, b string) bool {
	if a == b {
		return true
	}
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}
