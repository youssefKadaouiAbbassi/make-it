// Package hookprobe runs a plugin's session hooks in a scratch project and a scratch home and
// reports the files they create there, so an update can tell when a plugin starts writing
// somewhere new.
package hookprobe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Writes are the files a plugin's hooks created: sorted slash paths relative to the scratch
// project and the scratch home, regular files and symlinks only.
type Writes struct{ Project, Home []string }

// ErrUnsupported is returned by Run on Windows, where the probe does not run.
var ErrUnsupported = errors.New("the check of what plugin hooks write does not run on Windows")

// maxLimit is the longest one hook may run; tests shorten it.
var maxLimit = 10 * time.Second

// hook is one entry of hooks.<Event>[].hooks[].
type hook struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Timeout float64  `json:"timeout"`
	If      string   `json:"if"`
}

type group struct {
	Matcher string `json:"matcher"`
	Hooks   []hook `json:"hooks"`
}

// events are the simulated session, in order. match is what a group's matcher is tested against
// ("" when the probe runs every group), extra the event's own input fields.
var events = []struct {
	name, match string
	extra       map[string]any
}{
	{"SessionStart", "startup", map[string]any{"source": "startup"}},
	{"UserPromptSubmit", "", map[string]any{"prompt": "hello"}},
	{"PreToolUse", "Bash", map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": "true"}}},
	{"PostToolUse", "Bash", map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": "true"}, "tool_response": map[string]any{}}},
	{"Stop", "", map[string]any{"stop_hook_active": false}},
	{"PreCompact", "", map[string]any{"trigger": "manual"}},
	{"SessionEnd", "", map[string]any{"reason": "other"}},
}

// Run runs the command hooks of the plugin at pluginDir for one simulated session (start, a
// prompt, a Bash call, stop, compact, end) and returns the files they created. Each hook runs in
// a scratch git project with HOME, the XDG folders and CLAUDE_CONFIG_DIR in a scratch home, env
// underneath those, and is killed with its process group at its time limit. A hook that fails or
// hangs is not an error. Both scratch folders are removed before Run returns.
func Run(ctx context.Context, pluginDir string, env []string) (Writes, error) {
	if !supported {
		return Writes{}, ErrUnsupported
	}
	sources := hookSources(pluginDir)
	if len(sources) == 0 {
		return Writes{}, nil
	}
	root, err := os.MkdirTemp("", "makeit-hookprobe-")
	if err != nil {
		return Writes{}, fmt.Errorf("scratch folder: %w", err)
	}
	defer remove(root)
	// A hook compares paths with $PWD, which the kernel reports without links (macOS /var).
	if root, err = filepath.EvalSymlinks(root); err != nil {
		return Writes{}, fmt.Errorf("scratch folder: %w", err)
	}
	project, home := filepath.Join(root, "project"), filepath.Join(root, "home")
	config := filepath.Join(home, ".claude")
	transcript := filepath.Join(config, "projects", "project", "probe.jsonl")
	if err := errors.Join(os.MkdirAll(project, 0o700), os.MkdirAll(filepath.Dir(transcript), 0o700), os.WriteFile(transcript, nil, 0o600),
		os.WriteFile(filepath.Join(project, "README.md"), []byte("scratch project\n"), 0o600)); err != nil {
		return Writes{}, fmt.Errorf("scratch folder: %w", err)
	}
	env = append(slices.Clone(env),
		"HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"), "XDG_STATE_HOME="+filepath.Join(home, ".local", "state"),
		"XDG_DATA_HOME="+filepath.Join(home, ".local", "share"), "XDG_CACHE_HOME="+filepath.Join(home, ".cache"),
		"CLAUDE_CONFIG_DIR="+config, "CLAUDE_PLUGIN_ROOT="+pluginDir, "CLAUDE_PROJECT_DIR="+project, "PWD="+project,
		// makeit's daily run starts from a session hook and inherits that session's variables:
		// these three would lead a probed hook to the real session's files or the user's git config.
		"CLAUDE_PLUGIN_DATA="+filepath.Join(config, "plugins", "data", "probe"), "CLAUDE_ENV_FILE="+filepath.Join(config, "session-env"),
		"GIT_CONFIG_GLOBAL="+filepath.Join(home, ".gitconfig"))
	for _, args := range [][]string{{"init", "-q"}, {"add", "README.md"}, {"commit", "-q", "-m", "scratch"}} {
		git := exec.CommandContext(ctx, "git", append([]string{"-c", "user.name=makeit", "-c", "user.email=makeit@example.invalid",
			"-c", "init.defaultBranch=main", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"}, args...)...)
		// The identity comes from -c alone: no system file, and the global one is the scratch home's.
		git.Dir, git.Env = project, append(slices.Clone(env), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
		if out, err := git.CombinedOutput(); err != nil {
			return Writes{}, fmt.Errorf("scratch project: git %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
		}
	}
	before := [2]map[string]bool{list(project), list(home)}
	replace := strings.NewReplacer("${CLAUDE_PLUGIN_ROOT}", pluginDir, "${CLAUDE_PROJECT_DIR}", project)
	stdin := filepath.Join(root, "stdin.json")
	for _, ev := range events {
		input := map[string]any{"session_id": "makeit-probe", "transcript_path": transcript, "cwd": project, "hook_event_name": ev.name}
		for k, v := range ev.extra {
			input[k] = v
		}
		data, _ := json.Marshal(input)
		if err := os.WriteFile(stdin, data, 0o600); err != nil {
			return Writes{}, fmt.Errorf("scratch folder: %w", err)
		}
		for _, src := range sources {
			var groups []group
			if json.Unmarshal(src[ev.name], &groups) != nil {
				continue
			}
			for _, g := range groups {
				if ev.match != "" && !matches(g.Matcher, ev.match) {
					continue
				}
				for _, h := range g.Hooks {
					// A hook with an `if` would not fire for the simulated call.
					if h.Type != "command" || h.Command == "" || h.If != "" {
						continue
					}
					cmd := exec.Command("sh", "-c", replace.Replace(h.Command))
					if h.Args != nil {
						args := make([]string, len(h.Args))
						for i, a := range h.Args {
							args[i] = replace.Replace(a)
						}
						cmd = exec.Command(replace.Replace(h.Command), args...)
					}
					cmd.Dir, cmd.Env = project, env
					runHook(ctx, cmd, stdin, limitOf(h))
					if err := ctx.Err(); err != nil {
						return Writes{}, fmt.Errorf("the hook check was interrupted: %w", err)
					}
				}
			}
		}
	}
	return Writes{Project: added(list(project), before[0]), Home: added(list(home), before[1])}, nil
}

// limitOf is the hook's time limit: its own timeout when that is shorter than the probe's.
func limitOf(h hook) time.Duration {
	if own := time.Duration(h.Timeout * float64(time.Second)); own > 0 && own < maxLimit {
		return own
	}
	return maxLimit
}

// runHook runs one hook to its end or its limit, then kills its whole process group, so neither
// a hanging hook nor a child it left behind outlives the call. Its output goes nowhere and its
// input is a file, so no pipe a child holds open can keep the call waiting.
func runHook(ctx context.Context, cmd *exec.Cmd, stdin string, limit time.Duration) {
	in, err := os.Open(stdin)
	if err != nil {
		return
	}
	defer in.Close()
	cmd.Stdin = in
	ownGroup(cmd)
	if cmd.Start() != nil {
		return
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	case <-ctx.Done():
	}
	// shortcut: after a normal exit the group id could in theory belong to a new process group
	// (the pid counter would have to wrap in between); use a pidfd-held leader if that ever matters.
	killGroup(cmd.Process.Pid)
	<-done
}

// matches reports whether a group's matcher selects value: absent, empty, "*", or a regular
// expression that matches the whole value.
func matches(matcher, value string) bool {
	if matcher == "" || matcher == "*" {
		return true
	}
	re, err := regexp.Compile("^(?:" + matcher + ")$")
	return err == nil && re.MatchString(value)
}

// hookSources are the plugin's hook tables (event name to its groups): hooks/hooks.json, and the
// `hooks` key of .claude-plugin/plugin.json, an object or a path relative to the plugin. A file
// that is missing or not hooks is skipped, as Claude Code would not run it either.
func hookSources(pluginDir string) []map[string]json.RawMessage {
	var out []map[string]json.RawMessage
	table := func(raw []byte) {
		var m map[string]json.RawMessage
		if json.Unmarshal(raw, &m) != nil {
			return
		}
		// A hooks file wraps the table in {"hooks": ...}; the manifest's key holds it directly.
		var inner map[string]json.RawMessage
		if json.Unmarshal(m["hooks"], &inner) == nil && inner != nil {
			m = inner
		}
		out = append(out, m)
	}
	file := func(path string) {
		if raw, err := os.ReadFile(path); err == nil {
			table(raw)
		}
	}
	file(filepath.Join(pluginDir, "hooks", "hooks.json"))
	var manifest struct {
		Hooks json.RawMessage `json:"hooks"`
	}
	if raw, err := os.ReadFile(filepath.Join(pluginDir, ".claude-plugin", "plugin.json")); err == nil && json.Unmarshal(raw, &manifest) == nil {
		var rel string
		switch {
		case json.Unmarshal(manifest.Hooks, &rel) != nil:
			table(manifest.Hooks)
		// The default file is already read; a path that leaves the plugin is not followed.
		case filepath.IsLocal(rel) && filepath.Clean(rel) != filepath.Join("hooks", "hooks.json"):
			file(filepath.Join(pluginDir, rel))
		}
	}
	return out
}

// list is the regular files and symlinks under dir as slash paths, without the project's .git.
func list(dir string) map[string]bool {
	out := map[string]bool{}
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		if d.IsDir() && rel == ".git" {
			return fs.SkipDir
		}
		if d.Type().IsRegular() || d.Type()&fs.ModeSymlink != 0 {
			out[filepath.ToSlash(rel)] = true
		}
		return nil
	})
	return out
}

// added are the paths of after that before lacks, sorted.
func added(after, before map[string]bool) []string {
	var out []string
	for p := range after {
		if !before[p] {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

// remove deletes the scratch folder, also when a hook left folders it cannot write to.
func remove(root string) {
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(path, 0o700)
		}
		return nil
	})
	_ = os.RemoveAll(root)
}
