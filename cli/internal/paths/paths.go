// Package paths resolves where Claude Code and makeit keep their files, and stores paths
// in state with portable roots (claude:, stack:, home:) so state stays valid when HOME,
// CLAUDE_CONFIG_DIR or MAKEIT_DIR change.
package paths

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Home is the user's home directory.
func Home() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil || h == "" {
		return "", errors.New("cannot find the home directory: set HOME (USERPROFILE on Windows)")
	}
	return h, nil
}

// ClaudeDir is ~/.claude unless CLAUDE_CONFIG_DIR is set: the rule Claude Code applies.
func ClaudeDir() (string, error) {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Abs(d)
	}
	h, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".claude"), nil
}

// StackDir is where makeit keeps its catalog copy, state and generations.
func StackDir() (string, error) {
	if d := os.Getenv("MAKEIT_DIR"); d != "" {
		return filepath.Abs(d)
	}
	h, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".makeit"), nil
}

// Roots maps the portable root names to directories.
type Roots struct{ Claude, Stack, Home string }

// Current resolves the roots from the environment.
func Current() (Roots, error) {
	var r Roots
	var err error
	if r.Home, err = Home(); err != nil {
		return r, err
	}
	if r.Claude, err = ClaudeDir(); err != nil {
		return r, err
	}
	r.Stack, err = StackDir()
	return r, err
}

// Abs turns "claude:settings.json" into an absolute path. Paths without a root pass through.
func (r Roots) Abs(p string) string {
	for prefix, dir := range map[string]string{"claude:": r.Claude, "stack:": r.Stack, "home:": r.Home} {
		if rest, ok := strings.CutPrefix(p, prefix); ok {
			return filepath.Join(dir, filepath.FromSlash(rest))
		}
	}
	return p
}

// Portable turns an absolute path under a root into its rooted form with forward slashes.
// The most specific root wins (claude: and stack: before home:).
func (r Roots) Portable(abs string) string {
	for _, c := range []struct{ prefix, dir string }{{"claude:", r.Claude}, {"stack:", r.Stack}, {"home:", r.Home}} {
		if rel, err := filepath.Rel(c.dir, abs); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			if rel == "." {
				rel = ""
			}
			return c.prefix + filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(abs)
}
