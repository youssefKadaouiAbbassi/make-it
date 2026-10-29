package claudecli

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"makeit/internal/execx"
)

// fake answers each argv (joined by spaces, without the binary) with a canned result.
type fake struct {
	answers map[string]execx.Result
	calls   []execx.Cmd
}

func (f *fake) run(_ context.Context, c execx.Cmd) (execx.Result, error) {
	f.calls = append(f.calls, c)
	key := strings.Join(c.Argv[1:], " ")
	if r, ok := f.answers[key]; ok {
		return r, nil
	}
	return execx.Result{Code: 1, Stderr: "unexpected " + key}, nil
}

func newCLI(answers map[string]execx.Result) (*CLI, *fake) {
	f := &fake{answers: answers}
	return &CLI{Path: "claude", Run: f.run}, f
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestVersionAndFloor(t *testing.T) {
	c, _ := newCLI(map[string]execx.Result{"--version": {Stdout: "2.1.311 (Claude Code)\n"}})
	v, err := c.Version(context.Background())
	if err != nil || v != "2.1.311" {
		t.Fatalf("%q %v", v, err)
	}
	cases := []struct {
		have string
		ok   bool
	}{{"2.1.287", true}, {"2.1.311", true}, {"2.2.0", true}, {"3.0", true}, {"2.1.286", false}, {"2.1.9", false}, {"1.99.999", false}}
	for _, c := range cases {
		err := CheckFloor(c.have, Floor)
		if (err == nil) != c.ok || (err != nil && !errors.Is(err, ErrBelowFloor)) {
			t.Errorf("%s: %v", c.have, err)
		}
	}
	if err := CheckFloor("2.x", Floor); err == nil {
		t.Error("accepted a non-version")
	}
	bad, _ := newCLI(map[string]execx.Result{"--version": {Stdout: "command not found\n"}})
	if _, err := bad.Version(context.Background()); err == nil {
		t.Error("accepted garbage version output")
	}
}

func TestPluginsParsesWholeStdout(t *testing.T) {
	c, _ := newCLI(map[string]execx.Result{"plugin list --json": {Stdout: fixture(t, "plugin-list.json")}})
	ps, err := c.Plugins(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 3 || !ps[1].HasUserConfig || ps[2].Enabled || len(ps[2].Errors) != 1 {
		t.Fatalf("%+v", ps)
	}
	c, _ = newCLI(map[string]execx.Result{"plugin list --json": {Stdout: "Loading...\n"}})
	if _, err := c.Plugins(context.Background()); err == nil {
		t.Fatal("accepted non-JSON")
	}
}

func TestMarketplaces(t *testing.T) {
	c, _ := newCLI(map[string]execx.Result{"plugin marketplace list --json": {Stdout: `[
  {"name": "claude-plugins-official", "source": "github", "repo": "anthropics/claude-plugins-official", "ref": "v1", "installLocation": "/x"},
  {"name": "makeit", "source": "directory", "path": "/home/user/.makeit/catalog", "installLocation": "/home/user/.makeit/catalog"}
]
`}})
	ms, err := c.Marketplaces(context.Background())
	if err != nil || len(ms) != 2 || ms[1].Path == "" || ms[0].Repo == "" || ms[0].Ref != "v1" {
		t.Fatalf("%+v %v", ms, err)
	}
}

func TestActionsNeverPassYes(t *testing.T) {
	shown := `{"command":"install","outcome":"failed","message":"The command was only displayed","pluginId":"x@m","shownCommand":{"command":"npm i -g x","plugin":"x@m","sha256":"abc123"}}`
	c, f := newCLI(map[string]execx.Result{
		"plugin install x@m --scope user --json":                         {Code: 1, Stdout: "Run: npm i -g x\n" + shown + "\n"},
		"plugin install x@m --scope user --accept-command abc123 --json": {Stdout: `{"command":"install","outcome":"ok","message":"Successfully installed plugin: x@m (scope: user)"}` + "\n"},
	})
	r, err := c.Install(context.Background(), "x@m", "")
	if err != nil || r.OK() || r.ShownCommand == nil || r.ShownCommand.Sha256 != "abc123" || r.Output != "Run: npm i -g x" {
		t.Fatalf("%+v %v", r, err)
	}
	if !strings.Contains(string(r.ShownCommand.Raw), "npm i -g x") {
		t.Fatalf("raw: %s", r.ShownCommand.Raw)
	}
	r, err = c.Install(context.Background(), "x@m", r.ShownCommand.Sha256)
	if err != nil || !r.OK() {
		t.Fatalf("%+v %v", r, err)
	}
	for _, call := range f.calls {
		if slices.Contains(call.Argv, "-y") || slices.Contains(call.Argv, "--yes") {
			t.Fatalf("passed -y: %v", call.Argv)
		}
	}
}

func TestActionResultLine(t *testing.T) {
	cases := []struct {
		name string
		res  execx.Result
		ok   bool
		err  bool
	}{
		{"ok", execx.Result{Stdout: `{"command":"marketplace-remove","outcome":"ok","marketplace":"m","message":"Successfully removed marketplace: m"}`}, true, false},
		{"already enabled", execx.Result{Code: 1, Stdout: `{"command":"enable","outcome":"failed","failureCode":"already_in_goal_state","alreadyInGoalState":true,"message":"x"}` + "\r\n"}, true, false},
		{"usage error, no result line", execx.Result{Code: 1, Stderr: "error: invalid scope"}, false, true},
		{"result not on last line", execx.Result{Stdout: `{"command":"install","outcome":"ok","message":"m"}` + "\nextra\n"}, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := parseAction("claude plugin x", c.res)
			if (err != nil) != c.err || (err == nil && r.OK() != c.ok) {
				t.Fatalf("%+v %v", r, err)
			}
		})
	}
}

func TestParseDoctor(t *testing.T) {
	ps := ParseDoctor(fixture(t, "doctor-invalid-2.1.311.txt"))
	if len(ps) != 3 {
		t.Fatalf("%+v", ps)
	}
	want := Problem{File: "/CFG/settings.json", Key: "permissions.allow", Reason: "Expected array, but received string"}
	if ps[0] != want || ps[2].Key != "model" {
		t.Fatalf("%+v", ps)
	}
	if got := ParseDoctor("Claude Code doctor\n\nRemote Control\n- Not signed in\n"); len(got) != 0 {
		t.Fatalf("read problems outside the block: %+v", got)
	}
}

func TestValidateSettingsUsesThrowawayConfig(t *testing.T) {
	var seen string
	c := &CLI{Path: "claude", Run: func(_ context.Context, cmd execx.Cmd) (execx.Result, error) {
		for _, e := range cmd.Env {
			if v, ok := strings.CutPrefix(e, "CLAUDE_CONFIG_DIR="); ok {
				seen = v
			}
		}
		b, err := os.ReadFile(seen + "/settings.json")
		if err != nil || string(b) != `{"model":5}` {
			t.Fatalf("candidate not written: %v", err)
		}
		return execx.Result{Stdout: "Invalid settings\n- " + seen + "/settings.json › model: Expected string, but received number\n- /etc/claude-code/managed-settings.json › x: other file\n\n"}, nil
	}}
	ps, err := c.ValidateSettings(context.Background(), []byte(`{"model":5}`))
	if err != nil || len(ps) != 1 || ps[0].Key != "model" {
		t.Fatalf("%+v %v", ps, err)
	}
	if _, err := os.Stat(seen); !os.IsNotExist(err) {
		t.Fatalf("throwaway dir left behind: %v", err)
	}
}

func TestUnneeded(t *testing.T) {
	out := "2 auto-installed plugins no longer needed at user scope:\n  local2@m3 (1.0.0)\n  same@m4 (1.0.0)\n(dry run — nothing removed)\n"
	c, f := newCLI(map[string]execx.Result{"plugin prune --dry-run --scope user": {Stdout: out}})
	ids, err := c.Unneeded(context.Background())
	if err != nil || !slices.Equal(ids, []string{"local2@m3", "same@m4"}) {
		t.Errorf("Unneeded = %v, %v", ids, err)
	}
	if args := f.calls[0].Argv; slices.Contains(args, "-y") || slices.Contains(args, "--yes") {
		t.Errorf("prune ran without --dry-run's guard: %v", args)
	}
	c, _ = newCLI(map[string]execx.Result{"plugin prune --dry-run --scope user": {Stdout: "Nothing to prune at user scope.\n"}})
	if ids, err := c.Unneeded(context.Background()); err != nil || len(ids) != 0 {
		t.Errorf("nothing to prune = %v, %v", ids, err)
	}
	c, _ = newCLI(map[string]execx.Result{"plugin prune --dry-run --scope user": {Code: 1, Stderr: "boom"}})
	if _, err := c.Unneeded(context.Background()); err == nil {
		t.Error("a failed prune dry run gave no error")
	}
}

func TestUpdateOutcomeParsed(t *testing.T) {
	c, _ := newCLI(map[string]execx.Result{"plugin update same@m2 --scope user --json": {Stdout: `{"command":"update","outcome":"ok","updateOutcome":"up_to_date","oldVersion":"1.0.0","newVersion":"1.0.0"}` + "\n"}})
	r, err := c.Update(context.Background(), "same@m2", "")
	if err != nil || r.UpdateOutcome != "up_to_date" {
		t.Errorf("Update = %+v, %v", r, err)
	}
}
