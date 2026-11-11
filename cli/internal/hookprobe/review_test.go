//go:build !windows

package hookprobe

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A shell that exports GIT_DIR (a git hook, a bare dotfiles setup) must not lead the scratch
// commit, or the probed hooks, to that repository.
func TestInheritedGitVariablesAreDropped(t *testing.T) {
	real := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.invalid", "-C", real}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return string(out)
	}
	git("init", "-q")
	write(t, filepath.Join(real, "a.txt"), "a\n", 0o644)
	git("add", "a.txt")
	git("commit", "-q", "-m", "real")
	dir := plugin(t, one("SessionStart", sh(`git rev-parse --show-toplevel >seen-top 2>&1; true`)))
	for _, env := range [][]string{
		{"GIT_DIR=" + filepath.Join(real, ".git")},
		{"GIT_DIR=" + filepath.Join(real, ".git"), "GIT_WORK_TREE=" + real, "GIT_INDEX_FILE=" + filepath.Join(real, ".git", "index")},
	} {
		w, err := Run(t.Context(), dir, append(os.Environ(), env...))
		if err != nil {
			t.Fatalf("%v: %v", env, err)
		}
		if log := git("log", "--oneline"); strings.Count(log, "\n") != 1 {
			t.Errorf("%v: the real repository gained commits:\n%s", env, log)
		}
		if _, err := os.Stat(filepath.Join(real, "README.md")); err == nil {
			t.Errorf("%v: the scratch file landed in the real repository", env)
		}
		if !slices.Contains(w.Project, "seen-top") {
			t.Errorf("%v: the hook did not run in the scratch project: %v", env, w.Project)
		}
	}
}

// A hook that plants a git hook writes into the project: .git/hooks is listed, the rest of .git
// (objects, the index) is not.
func TestGitHooksFolderIsListed(t *testing.T) {
	dir := plugin(t, one("SessionStart", sh(`mkdir -p .git/hooks && printf '#!/bin/sh\n' >.git/hooks/pre-commit; touch seen && git add seen 2>/dev/null; true`)))
	eq(t, probe(t, dir), []string{".git/hooks/pre-commit", "seen"}, nil)
}

// The manifest may give its hooks as a list of files and tables.
func TestManifestHooksAsList(t *testing.T) {
	dir := plugin(t, "")
	write(t, filepath.Join(dir, "extra", "a.json"), one("SessionStart", sh("touch from-file")), 0o644)
	write(t, filepath.Join(dir, ".claude-plugin", "plugin.json"),
		`{"name":"x","hooks":["./extra/a.json",{"Stop":[{"hooks":[`+sh("touch from-table")+`]}]},"../outside.json"]}`, 0o644)
	eq(t, probe(t, dir), []string{"from-file", "from-table"}, nil)
}
