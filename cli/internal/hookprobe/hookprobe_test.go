//go:build !windows

package hookprobe

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func write(t *testing.T, path, data string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), mode); err != nil {
		t.Fatal(err)
	}
}

// plugin is a fake plugin folder with the given hooks/hooks.json ("" for none).
func plugin(t *testing.T, hooks string) string {
	t.Helper()
	dir := t.TempDir()
	if hooks != "" {
		write(t, filepath.Join(dir, "hooks", "hooks.json"), hooks, 0o644)
	}
	return dir
}

// probe runs the plugin with this process's temp folder set to an empty one, and checks that
// nothing is left in it.
func probe(t *testing.T, dir string, env ...string) Writes {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	w, err := Run(context.Background(), dir, append(os.Environ(), env...))
	if err != nil {
		t.Fatal(err)
	}
	if left, _ := os.ReadDir(tmp); len(left) > 0 {
		t.Errorf("left on disk: %v", left)
	}
	return w
}

func one(event, hook string) string {
	return `{"hooks":{"` + event + `":[{"hooks":[` + hook + `]}]}}`
}

func sh(cmd string) string { return `{"type":"command","command":` + strconv.Quote(cmd) + `}` }

func eq(t *testing.T, got Writes, project, home []string) {
	t.Helper()
	if !slices.Equal(got.Project, project) || !slices.Equal(got.Home, home) {
		t.Errorf("writes = %+v, want project %v home %v", got, project, home)
	}
}

func TestNoHooks(t *testing.T) {
	eq(t, probe(t, plugin(t, "")), nil, nil)
	eq(t, probe(t, plugin(t, "not json")), nil, nil)
	eq(t, probe(t, plugin(t, `{"hooks":{"SessionStart":"nonsense","Stop":[{"hooks":[{"type":"prompt","prompt":"x"}]}]}}`)), nil, nil)
}

func TestWritesInProjectAndHome(t *testing.T) {
	dir := plugin(t, one("SessionStart", sh(`echo x >"$PWD/new.txt"; mkdir -p "$HOME/.tool" sub; echo y >"$HOME/.tool/state"; ln -s new.txt sub/link; echo z >.git/inside`)))
	eq(t, probe(t, dir), []string{"new.txt", "sub/link"}, []string{".tool/state"})
}

func TestPluginRootAndProjectDir(t *testing.T) {
	dir := plugin(t, one("Stop", sh(`${CLAUDE_PLUGIN_ROOT}/run.sh "${CLAUDE_PROJECT_DIR}"`)))
	write(t, filepath.Join(dir, "run.sh"), "#!/bin/sh\n"+
		`[ "$1" = "$PWD" ] && [ "$CLAUDE_PROJECT_DIR" = "$PWD" ] && [ -x "$CLAUDE_PLUGIN_ROOT/run.sh" ] && [ "$(git log --oneline | wc -l)" -eq 1 ] && touch ok`+"\n", 0o755)
	eq(t, probe(t, dir), []string{"ok"}, nil)
}

func TestMatcherAndIf(t *testing.T) {
	group := func(matcher, file string) string {
		return `{"matcher":"` + matcher + `","hooks":[` + sh("touch "+file) + `]}`
	}
	dir := plugin(t, `{"hooks":{
		"SessionStart":[`+group("", "s-empty")+`,`+group("*", "s-star")+`,`+group("startup|resume", "s-re")+`,`+group("resume", "s-no")+`,`+group("(", "s-bad")+`],
		"PreToolUse":[`+group("Bash", "pre-bash")+`,`+group("Edit|Write", "pre-no")+`,{"hooks":[{"type":"command","command":"touch pre-if","if":"Bash(git push*)"}]}],
		"PostToolUse":[`+group("B.*", "post-re")+`,`+group("Bas", "post-no")+`],
		"UserPromptSubmit":[{"hooks":[`+sh("touch prompt")+`]}],
		"PreCompact":[`+group("manual|auto", "compact")+`],
		"SessionEnd":[{"hooks":[`+sh("touch end")+`]}],
		"Notification":[{"hooks":[`+sh("touch notification")+`]}]}}`)
	eq(t, probe(t, dir), []string{"compact", "end", "post-re", "pre-bash", "prompt", "s-empty", "s-re", "s-star"}, nil)
}

func TestEventOrder(t *testing.T) {
	var groups []string
	for _, ev := range []string{"SessionEnd", "PreCompact", "Stop", "PostToolUse", "PreToolUse", "UserPromptSubmit", "SessionStart"} {
		groups = append(groups, `"`+ev+`":[{"hooks":[`+sh("echo "+ev+" >>$HOME/order")+`]}]`)
	}
	dir := plugin(t, `{"hooks":{`+strings.Join(groups, ",")+`}}`)
	out := filepath.Join(dir, "order")
	// The scratch home is gone after the call, so the last hook copies the file out.
	write(t, filepath.Join(dir, ".claude-plugin", "plugin.json"), `{"name":"p","hooks":`+one("SessionEnd", sh("cp $HOME/order "+out))[len(`{"hooks":`):], 0o644)
	probe(t, dir)
	got, _ := os.ReadFile(out)
	if want := "SessionStart\nUserPromptSubmit\nPreToolUse\nPostToolUse\nStop\nPreCompact\nSessionEnd\n"; string(got) != want {
		t.Errorf("order = %q, want %q", got, want)
	}
}

func TestHooksInManifest(t *testing.T) {
	obj := plugin(t, "")
	write(t, filepath.Join(obj, ".claude-plugin", "plugin.json"), `{"name":"p","hooks":{"SessionStart":[{"hooks":[`+sh("touch from-object")+`]}]}}`, 0o644)
	eq(t, probe(t, obj), []string{"from-object"}, nil)

	path := plugin(t, "")
	write(t, filepath.Join(path, ".claude-plugin", "plugin.json"), `{"name":"p","hooks":"./config/h.json"}`, 0o644)
	write(t, filepath.Join(path, "config", "h.json"), one("SessionStart", sh("touch from-path")), 0o644)
	eq(t, probe(t, path), []string{"from-path"}, nil)
}

func TestExecForm(t *testing.T) {
	dir := plugin(t, one("SessionStart", `{"type":"command","command":"${CLAUDE_PLUGIN_ROOT}/run.sh","args":["a b","${CLAUDE_PROJECT_DIR}/made"]}`))
	// Exec form passes "a b" as one argument, with no shell in between.
	write(t, filepath.Join(dir, "run.sh"), "#!/bin/sh\n"+`[ "$1" = "a b" ] && touch "$2"`+"\n", 0o755)
	eq(t, probe(t, dir), []string{"made"}, nil)
}

func TestStdinIsTheEventInput(t *testing.T) {
	dir := plugin(t, `{"hooks":{
		"SessionStart":[{"hooks":[`+sh(`cat >"$HOME/start.json"; cp "$HOME/start.json" "$CLAUDE_PLUGIN_ROOT/start.json"`)+`]}],
		"PreToolUse":[{"hooks":[`+sh(`cat >"$CLAUDE_PLUGIN_ROOT/pre.json"`)+`]}]}}`)
	eq(t, probe(t, dir), nil, []string{"start.json"})
	start, _ := os.ReadFile(filepath.Join(dir, "start.json"))
	for _, want := range []string{`"hook_event_name":"SessionStart"`, `"source":"startup"`, `"session_id":"`, `"transcript_path":"`, `"cwd":"`} {
		if !strings.Contains(string(start), want) {
			t.Errorf("SessionStart input %s lacks %s", start, want)
		}
	}
	pre, _ := os.ReadFile(filepath.Join(dir, "pre.json"))
	for _, want := range []string{`"tool_name":"Bash"`, `"tool_input":{"command":"true"}`} {
		if !strings.Contains(string(pre), want) {
			t.Errorf("PreToolUse input %s lacks %s", pre, want)
		}
	}
}

func TestEnvUnderneathTheOverrides(t *testing.T) {
	real := t.TempDir()
	dir := plugin(t, one("SessionStart", sh(`echo "$PROBE_VAR" >~/tilde; echo x >>"$CLAUDE_ENV_FILE"; git config --global probe.x 1; echo "$XDG_CONFIG_HOME $XDG_STATE_HOME $XDG_DATA_HOME $XDG_CACHE_HOME $CLAUDE_CONFIG_DIR" >"$CLAUDE_PLUGIN_ROOT/dirs"; cp ~/tilde "$CLAUDE_PLUGIN_ROOT/var"`)))
	w := probe(t, dir, "PROBE_VAR=from-env", "HOME="+real, "XDG_CONFIG_HOME="+real, "CLAUDE_CONFIG_DIR="+real,
		"CLAUDE_ENV_FILE="+filepath.Join(real, "env"), "GIT_CONFIG_GLOBAL="+filepath.Join(real, "gitconfig"))
	if !slices.Contains(w.Home, "tilde") {
		t.Errorf("home writes %v: ~ must be the scratch home", w.Home)
	}
	if left, _ := os.ReadDir(real); len(left) > 0 {
		t.Errorf("the hook wrote into the real home: %v", left)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "var")); string(got) != "from-env\n" {
		t.Errorf("PROBE_VAR reached the hook as %q", got)
	}
	dirs, _ := os.ReadFile(filepath.Join(dir, "dirs"))
	for _, d := range strings.Fields(string(dirs)) {
		if strings.HasPrefix(d, real) || !strings.Contains(d, "makeit-hookprobe-") {
			t.Errorf("a config folder is outside the scratch home: %s", d)
		}
	}
}

func TestFailingHooksAreNotErrors(t *testing.T) {
	dir := plugin(t, `{"hooks":{"SessionStart":[{"hooks":[`+sh("exit 3")+`,`+sh("/no/such/program")+`,{"type":"command","command":"/no/such","args":[]},`+
		sh(`head -c 200000 /dev/urandom; head -c 200000 /dev/urandom >&2`)+`,`+sh("touch after")+`]}]}}`)
	eq(t, probe(t, dir), []string{"after"}, nil)
}

// alive reports whether the process whose pid is in file still runs (a zombie does not count).
func alive(t *testing.T, file string) bool {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("the hook did not record its pid: %v", err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	out, _ := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	return !strings.HasPrefix(strings.TrimSpace(string(out)), "Z") && len(out) > 0
}

func TestHangingHookIsCutWithItsChildren(t *testing.T) {
	old := maxLimit
	maxLimit = 500 * time.Millisecond
	defer func() { maxLimit = old }()
	dir := plugin(t, `{"hooks":{"SessionStart":[{"hooks":[`+
		// Ignores SIGTERM, keeps a child, and sleeps past any limit.
		sh(`trap "" TERM; sleep 60 & echo $! >"$CLAUDE_PLUGIN_ROOT/child"; touch before; sleep 60; touch late`)+`]}],
		"Stop":[{"hooks":[`+
		// Exits at once and leaves a child behind; then one that waits on stdin's end and more.
		sh(`(trap "" TERM HUP; sleep 60) & echo $! >"$CLAUDE_PLUGIN_ROOT/orphan"`)+`,`+sh(`cat; cat /dev/zero >/dev/null`)+`]}]}}`)
	start := time.Now()
	w := probe(t, dir)
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("the call took %s; each hanging hook must be cut at the limit", took)
	}
	eq(t, w, []string{"before"}, nil)
	deadline := time.Now().Add(2 * time.Second)
	for _, f := range []string{"child", "orphan"} {
		for alive(t, filepath.Join(dir, f)) {
			if time.Now().After(deadline) {
				t.Fatalf("the %s process outlived the call", f)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

func TestHookTimeoutIsUsedWhenShorter(t *testing.T) {
	dir := plugin(t, one("SessionStart", `{"type":"command","command":"sleep 30","timeout":0.3}`))
	start := time.Now()
	probe(t, dir)
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("the call took %s; the hook's own 0.3 s limit applies", took)
	}
	if got := limitOf(hook{Timeout: 600}); got != maxLimit {
		t.Errorf("a 600 s hook timeout gives %s, want %s", got, maxLimit)
	}
}

func TestCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dir := plugin(t, one("SessionStart", sh("sleep 30")))
	time.AfterFunc(200*time.Millisecond, cancel)
	start := time.Now()
	if _, err := Run(ctx, dir, os.Environ()); err == nil {
		t.Error("a cancelled run must return the context's error, not a partial result")
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("the call took %s after a cancel", took)
	}
}

func TestReadOnlyFoldersAreRemoved(t *testing.T) {
	dir := plugin(t, one("SessionStart", sh(`mkdir -p ro/deep && touch ro/deep/f && chmod -R a-w ro`)))
	eq(t, probe(t, dir), []string{"ro/deep/f"}, nil)
}
