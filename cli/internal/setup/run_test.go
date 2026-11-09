package setup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"makeit/internal/catalog"
	"makeit/internal/claudecli"
	"makeit/internal/execx"
	"makeit/internal/fsx"
	"makeit/internal/paths"
	"makeit/internal/plan"
	"makeit/internal/platform"
	"makeit/internal/pluginrule"
	"makeit/internal/state"
)

// fakeMachine is a linux machine with every prerequisite, Claude Code 2.1.312 at /usr/bin/claude
// and a claude that remembers the marketplaces and plugins it was asked to add.
type fakeMachine struct {
	t           *testing.T
	env         Env
	markets     []string
	plugins     []string
	failInstall bool
	// calls is appended from concurrent listings, so it has a lock.
	mu    sync.Mutex
	calls []string
	// deps arrive with the bundle's install; needed are ids another plugin needs, so prune's dry
	// run leaves them out; failUninstall makes that id's uninstall fail; onRemove runs when the
	// marketplace remove arrives.
	deps          []string
	needed        map[string]bool
	failUninstall string
	onRemove      func()
}

func newMachine(t *testing.T) *fakeMachine {
	t.Helper()
	home := t.TempDir()
	m := &fakeMachine{t: t}
	m.env = Env{
		Roots:    paths.Roots{Home: home, Claude: filepath.Join(home, ".claude"), Stack: filepath.Join(home, ".makeit")},
		Platform: platform.Platform{OS: "linux", Arch: "amd64", PM: platform.APT},
		Present:  func(string) bool { return true },
		Run:      m.run,
		Catalog:  catalog.Embedded,
		Locate: func(context.Context) []claudecli.Install {
			return []claudecli.Install{{Path: "/usr/bin/claude", Source: "path", OnPath: true}}
		},
		Yes: true,
	}
	// Installing the bundle brings in what it declares, as Claude Code does.
	req, err := catalog.LoadRequires(catalog.Embedded)
	if err != nil {
		t.Fatal(err)
	}
	if m.deps, err = pluginrule.BundleIDs(catalog.Embedded, req); err != nil {
		t.Fatal(err)
	}
	return m
}

func (m *fakeMachine) run(_ context.Context, c execx.Cmd) (execx.Result, error) {
	cmd := strings.Join(c.Argv, " ")
	m.mu.Lock()
	m.calls = append(m.calls, cmd)
	m.mu.Unlock()
	list := func(names []string, field string) string {
		var rows []string
		for _, n := range names {
			rows = append(rows, `{"`+field+`":"`+n+`","enabled":true,"version":"1.0.0"}`)
		}
		return "[" + strings.Join(rows, ",") + "]"
	}
	switch {
	case strings.HasPrefix(cmd, "npm install --prefix ") && len(c.Argv) == 7:
		// The local chrome-devtools-mcp install: leaves the package.json npm would.
		pkg, ver, _ := strings.Cut(c.Argv[6], "@")
		path := filepath.Join(c.Argv[3], "node_modules", pkg, "package.json")
		// Not t.Fatal: this runs off the test goroutine.
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			m.t.Error(err)
			return execx.Result{Code: 1}, nil
		}
		if err := os.WriteFile(path, []byte(`{"version":"`+ver+`"}`), 0o644); err != nil {
			m.t.Error(err)
			return execx.Result{Code: 1}, nil
		}
		return execx.Result{}, nil
	case cmd == "/usr/bin/claude --version":
		return execx.Result{Stdout: "2.1.319 (Claude Code)\n"}, nil
	case cmd == "/usr/bin/claude doctor":
		return execx.Result{}, nil
	case cmd == "/usr/bin/claude plugin marketplace list --json":
		var rows []string
		for _, n := range m.markets {
			row := `{"name":"` + n + `","source":"github"}`
			if n == "makeit" {
				row = `{"name":"makeit","source":"directory","path":"` + filepath.Join(m.env.Roots.Stack, "catalog") + `"}`
			}
			rows = append(rows, row)
		}
		return execx.Result{Stdout: "[" + strings.Join(rows, ",") + "]"}, nil
	case cmd == "/usr/bin/claude plugin list --json":
		return execx.Result{Stdout: list(m.plugins, "id")}, nil
	case strings.HasPrefix(cmd, "/usr/bin/claude plugin marketplace add "):
		src := c.Argv[4]
		name := "makeit"
		if strings.Contains(src, "claude-plugins-official") {
			name = "claude-plugins-official"
		}
		m.markets = append(m.markets, name)
		return execx.Result{Stdout: `{"command":"marketplace add","outcome":"ok"}`}, nil
	case strings.HasPrefix(cmd, "/usr/bin/claude plugin install "):
		if m.failInstall {
			return execx.Result{Stdout: `{"command":"install","outcome":"failed","message":"network down"}`}, nil
		}
		for _, id := range append([]string{c.Argv[3]}, m.deps...) {
			if !slices.Contains(m.plugins, id) && (id == c.Argv[3] || c.Argv[3] == "makeit-bundle@makeit") {
				m.plugins = append(m.plugins, id)
			}
		}
		return execx.Result{Stdout: `{"command":"install","outcome":"ok"}`}, nil
	case strings.HasPrefix(cmd, "/usr/bin/claude plugin uninstall "):
		id := c.Argv[3]
		if id == m.failUninstall {
			return execx.Result{Code: 1, Stdout: `{"command":"uninstall","outcome":"failed","message":"busy"}`}, nil
		}
		m.plugins = slices.DeleteFunc(m.plugins, func(p string) bool { return p == id })
		return execx.Result{Stdout: `{"command":"uninstall","outcome":"ok","keptData":true}`}, nil
	case strings.HasPrefix(cmd, "/usr/bin/claude plugin marketplace remove "):
		if m.onRemove != nil {
			m.onRemove()
		}
		name := c.Argv[4]
		m.plugins = slices.DeleteFunc(m.plugins, func(p string) bool { return strings.HasSuffix(p, "@"+name) })
		// Measured: from the home directory (or a project), ~/.claude/settings.json also counts as
		// project settings, so the marketplace stays registered after a user-scope remove.
		if c.Dir == "" || strings.HasPrefix(c.Dir, m.env.Roots.Home) {
			return execx.Result{Stdout: `{"command":"marketplace-remove","outcome":"ok"}`}, nil
		}
		m.markets = slices.DeleteFunc(m.markets, func(n string) bool { return n == name })
		return execx.Result{Stdout: `{"command":"marketplace-remove","outcome":"ok"}`}, nil
	case cmd == "/usr/bin/claude plugin prune --dry-run --scope user":
		var out strings.Builder
		if !slices.Contains(m.plugins, "makeit-bundle@makeit") {
			for _, p := range m.plugins {
				if slices.Contains(m.deps, p) && !m.needed[p] {
					out.WriteString("  " + p + " (1.0.0)\n")
				}
			}
		}
		return execx.Result{Stdout: out.String()}, nil
	}
	return execx.Result{Code: 1, Stderr: "unexpected " + cmd}, nil
}

func generations(t *testing.T, stack string) int {
	t.Helper()
	es, err := os.ReadDir(state.GenerationsDir(stack))
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(es)
}

func apply(t *testing.T, m *fakeMachine) Report {
	t.Helper()
	rep, err := Run(context.Background(), m.env, plan.Apply, nil, []string{"setup", "--yes"})
	if err != nil {
		t.Fatalf("run: %v (%s)", err, rep.Plan.Summary())
	}
	return rep
}

func TestRunNeedsConsent(t *testing.T) {
	m := newMachine(t)
	rep, err := Run(context.Background(), m.env, plan.NeedConsent, nil, nil)
	if !errors.Is(err, ErrConsent) || len(rep.Plan.Outcomes) == 0 {
		t.Fatalf("err %v, plan %d outcomes", err, len(rep.Plan.Outcomes))
	}
	if _, err := os.Stat(m.env.Roots.Stack); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("consent needed, but %s was created", m.env.Roots.Stack)
	}
	if _, err := os.Stat(m.env.Roots.Claude); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("consent needed, but %s was created", m.env.Roots.Claude)
	}
}

func TestRunNothingToDo(t *testing.T) {
	m := newMachine(t)
	first := apply(t, m)
	if first.Plan.Failed() || first.Generation != 1 {
		t.Fatalf("first run: %s, generation %d", first.Plan.Summary(), first.Generation)
	}
	second := apply(t, m)
	for _, o := range second.Plan.Outcomes {
		if o.Status != plan.Already {
			t.Errorf("second run: %s is %s, want already", o.ID, o.Status)
		}
	}
	if second.Generation != 0 || generations(t, m.env.Roots.Stack) != 1 {
		t.Errorf("second run made a generation (%d, %d on disk)", second.Generation, generations(t, m.env.Roots.Stack))
	}
	if _, err := os.Stat(filepath.Join(m.env.Roots.Stack, "lock")); !errors.Is(err, os.ErrNotExist) {
		t.Error("lock left behind")
	}
}

func TestSecondRunResumes(t *testing.T) {
	m := newMachine(t)
	m.failInstall = true
	rep, err := Run(context.Background(), m.env, plan.Apply, nil, nil)
	if err != nil || !rep.Plan.Failed() {
		t.Fatalf("first run should fail at the bundle: %v %s", err, rep.Plan.Summary())
	}
	m.failInstall = false
	m.calls = nil
	second := apply(t, m)
	for _, o := range second.Plan.Outcomes {
		want := plan.Already
		// bundle failed on the first run; the local MCP install, settings and the global files come after it.
		if o.ID == "bundle" || o.ID == "mcp.chrome-devtools" || o.ID == "settings" || strings.HasPrefix(o.ID, "global.") || strings.HasPrefix(o.ID, "path.") {
			want = plan.Done
		}
		if o.Status != want {
			t.Errorf("%s is %s, want %s", o.ID, o.Status, want)
		}
	}
}

func TestRunJournal(t *testing.T) {
	m := newMachine(t)
	m.failInstall = true
	if _, err := Run(context.Background(), m.env, plan.Apply, nil, nil); err != nil {
		t.Fatal(err)
	}
	st, _, err := state.LoadState(filepath.Join(m.env.Roots.Stack, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if r := st.Steps["marketplace"]; r == nil || r.Status != string(plan.Done) {
		t.Errorf("marketplace record %+v", r)
	}
	if r := st.Steps["bundle"]; r == nil || r.Status != string(plan.Failed) || !strings.Contains(r.Error, "network down") {
		t.Errorf("bundle record %+v", r)
	}
	if st.Plugins["makeit-bundle@makeit"] != nil {
		t.Error("a failed bundle install was recorded as installed")
	}
}

func TestRunPromptDeclined(t *testing.T) {
	m := newMachine(t)
	no := func([]plan.Outcome) (bool, error) { return false, nil }
	if _, err := Run(context.Background(), m.env, plan.Prompt, no, nil); !errors.Is(err, ErrDeclined) {
		t.Fatalf("err %v, want ErrDeclined", err)
	}
	if generations(t, m.env.Roots.Stack) != 0 {
		t.Error("declined, but a generation was made")
	}
}

// A run that would apply takes the lock before it reads state or asks claude anything, so a
// second setup cannot plan on stale state while another applies.
func TestRunLockedPlansNothing(t *testing.T) {
	m := newMachine(t)
	lock, err := fsx.Acquire(filepath.Join(m.env.Roots.Stack, "lock"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	if _, err := Run(context.Background(), m.env, plan.Apply, nil, nil); err == nil || !strings.Contains(err.Error(), "another makeit run") {
		t.Fatalf("err %v, want the lock holder named", err)
	}
	if len(m.calls) != 0 {
		t.Errorf("planned while another run held the lock: %v", m.calls)
	}
}

// What setup cannot install here is reported after the apply pass too, not only in the plan.
func TestNeedsYouSurvivesTheApplyPass(t *testing.T) {
	m := newMachine(t)
	m.env.Platform.PM = platform.APK
	m.env.Present = func(bin string) bool {
		return !slices.Contains([]string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome", "Google Chrome"}, bin)
	}
	rep := apply(t, m)
	if !slices.ContainsFunc(rep.NeedsYou, func(n NeedsYou) bool { return n.ID == "chrome" }) {
		t.Errorf("needs you %+v, want chrome", rep.NeedsYou)
	}
}

// setup's two listings run together from the start: a runner that releases each listing only
// once both are in flight; listings one after the other wait out the deadline and a step fails.
func TestSetupListsConcurrently(t *testing.T) {
	m := newMachine(t)
	var arrived atomic.Int32
	open := make(chan struct{})
	var once sync.Once
	inner := m.env.Run
	m.env.Run = func(ctx context.Context, c execx.Cmd) (execx.Result, error) {
		cmd := strings.Join(c.Argv, " ")
		if strings.HasSuffix(cmd, "plugin list --json") || strings.HasSuffix(cmd, "plugin marketplace list --json") {
			if arrived.Add(1) == 2 {
				once.Do(func() { close(open) })
			}
			select {
			case <-open:
			case <-time.After(2 * time.Second):
				return execx.Result{Code: 1, Stderr: "deadline: the listings ran one after the other"}, nil
			}
		}
		return inner(ctx, c)
	}
	if rep := apply(t, m); rep.Plan.Failed() {
		t.Errorf("a step failed: %s", rep.Plan.Summary())
	}
}

// The report lists Claude Code, then plugins, then MCP servers, then tools; a plugin move counts
// only when the plugin really updated, an MCP move only when the plugin set was written.
func TestReportChanges(t *testing.T) {
	got := reportChanges(
		[]state.Change{{Kind: "tool", Item: "grype", From: "0.121.1", To: "0.122.0"}, {Kind: "claude-code", Item: "Claude Code", From: "2.1.317", To: "2.1.320"}},
		[]state.PinMove{{ID: "superpowers@makeit", FromTag: "v7.0.1", ToTag: "v7.0.2"}, {ID: "chrome-devtools-mcp@makeit", FromTag: "chrome-devtools-mcp-v1.12.0", ToTag: "chrome-devtools-mcp-v1.13.0"}},
		[]state.Change{{Kind: "mcp", Item: "chrome-devtools-mcp", From: "1.12.0", To: "1.13.0"}}, true)
	want := []state.Change{
		{Kind: "claude-code", Item: "Claude Code", From: "2.1.317", To: "2.1.320"},
		{Kind: "plugin", Item: "superpowers", From: "7.0.1", To: "7.0.2"},
		{Kind: "plugin", Item: "chrome-devtools-mcp", From: "1.12.0", To: "1.13.0"},
		{Kind: "mcp", Item: "chrome-devtools-mcp", From: "1.12.0", To: "1.13.0"},
		{Kind: "tool", Item: "grype", From: "0.121.1", To: "0.122.0"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("changes\n%+v\nwant\n%+v", got, want)
	}
	if got := reportChanges(nil, nil, []state.Change{{Kind: "mcp", Item: "x", From: "1", To: "2"}}, false); len(got) != 0 {
		t.Errorf("an MCP move without the plugin set written: %+v", got)
	}
}

// A plugin on a branch moves by commit: the change shows the short commits.
func TestReportChangesBranch(t *testing.T) {
	got := reportChanges(nil, []state.PinMove{{ID: "security-audit@makeit", FromTag: "main", FromSHA: "c1c8a8c1471069fb", ToTag: "main", ToSHA: "9f2e4d0aa1b2c3d4"}}, nil, false)
	want := []state.Change{{Kind: "plugin", Item: "security-audit", From: "c1c8a8c", To: "9f2e4d0"}}
	if !slices.Equal(got, want) {
		t.Errorf("changes %+v, want %+v", got, want)
	}
}
