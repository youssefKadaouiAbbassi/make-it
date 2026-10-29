package setup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"makeit/internal/catalog"
	"makeit/internal/claudecli"
	"makeit/internal/execx"
	"makeit/internal/fsx"
	"makeit/internal/paths"
	"makeit/internal/plan"
	"makeit/internal/platform"
	"makeit/internal/state"
)

// fakeMachine is a linux machine with every prerequisite, Claude Code 2.1.312 at /usr/bin/claude
// and a claude that remembers the marketplaces and plugins it was asked to add.
type fakeMachine struct {
	env         Env
	markets     []string
	plugins     []string
	failInstall bool
	calls       []string
}

func newMachine(t *testing.T) *fakeMachine {
	t.Helper()
	home := t.TempDir()
	m := &fakeMachine{}
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
	return m
}

func (m *fakeMachine) run(_ context.Context, c execx.Cmd) (execx.Result, error) {
	cmd := strings.Join(c.Argv, " ")
	m.calls = append(m.calls, cmd)
	list := func(names []string, field string) string {
		var rows []string
		for _, n := range names {
			rows = append(rows, `{"`+field+`":"`+n+`","enabled":true,"version":"1.0.0"}`)
		}
		return "[" + strings.Join(rows, ",") + "]"
	}
	switch {
	case cmd == "/usr/bin/claude --version":
		return execx.Result{Stdout: "2.1.312 (Claude Code)\n"}, nil
	case cmd == "/usr/bin/claude doctor":
		return execx.Result{}, nil
	case cmd == "/usr/bin/claude plugin marketplace list --json":
		return execx.Result{Stdout: list(m.markets, "name")}, nil
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
		m.plugins = append(m.plugins, c.Argv[3])
		return execx.Result{Stdout: `{"command":"install","outcome":"ok"}`}, nil
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
		// bundle failed on the first run; settings and the global files come after it.
		if o.ID == "bundle" || o.ID == "settings" || strings.HasPrefix(o.ID, "global.") {
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
