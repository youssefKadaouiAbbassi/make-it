package doctor

// nodePath warns when makeit installed node from nodejs.org but the node on PATH is another one
// (/usr/bin before ~/.local/bin), so the hooks and the browser MCP would run that one.
func (r *report) nodePath(m Machine) {
	if !m.NodeDist || m.NodePath == m.NodeLink {
		return
	}
	found := "no node is on PATH"
	if m.NodePath != "" {
		found = "PATH finds " + m.NodePath + " first"
	}
	r.add("node.path", "tools", Warn, "makeit installed node at "+m.NodeLink+", but "+found,
		"put ~/.local/bin first in PATH (makeit setup writes it to your shell profile; open a new terminal)")
}
