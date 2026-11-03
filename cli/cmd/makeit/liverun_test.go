package main

import (
	"context"
	"reflect"
	"testing"

	"makeit/internal/execx"
)

type fakeHold struct{ held, released int }

func (h *fakeHold) Hold() func() { h.held++; return func() { h.released++ } }

type call1 struct {
	argv    []string
	inherit bool
}

func record(calls *[]call1) execx.Runner {
	return func(_ context.Context, c execx.Cmd) (execx.Result, error) {
		*calls = append(*calls, call1{c.Argv, c.Inherit})
		return execx.Result{}, nil
	}
}

// In the live view, a package manager's output is captured instead of flooding the screen; sudo's
// password prompt still gets the terminal, through a held `sudo -v` first.
func TestLiveRunCapturesOutput(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cmd   execx.Cmd
		want  []call1
		holds int
	}{
		{"sudo install", execx.Cmd{Argv: []string{"sudo", "env", "DEBIAN_FRONTEND=noninteractive", "apt-get", "install", "-y", "jq"}, Inherit: true},
			[]call1{{[]string{"sudo", "-v"}, true}, {[]string{"sudo", "env", "DEBIAN_FRONTEND=noninteractive", "apt-get", "install", "-y", "jq"}, false}}, 1},
		{"sudo -v itself", execx.Cmd{Argv: []string{"sudo", "-v"}, Inherit: true},
			[]call1{{[]string{"sudo", "-v"}, true}}, 1},
		{"script with sudo", execx.Cmd{Argv: []string{"sh", "-c", "sudo apt-get update"}, Inherit: true},
			[]call1{{[]string{"sudo", "-v"}, true}, {[]string{"sh", "-c", "sudo apt-get update"}, false}}, 1},
		{"no sudo", execx.Cmd{Argv: []string{"brew", "install", "jq"}, Inherit: true},
			[]call1{{[]string{"brew", "install", "jq"}, false}}, 0},
		{"already captured", execx.Cmd{Argv: []string{"git", "status"}},
			[]call1{{[]string{"git", "status"}, false}}, 0},
	} {
		var calls []call1
		h := &fakeHold{}
		_, _ = liveRun(record(&calls), func() holder { return h })(context.Background(), tc.cmd)
		if !reflect.DeepEqual(calls, tc.want) {
			t.Errorf("%s: calls %v, want %v", tc.name, calls, tc.want)
		}
		if h.held != tc.holds || h.released != h.held {
			t.Errorf("%s: held %d released %d, want %d", tc.name, h.held, h.released, tc.holds)
		}
	}
}

// Without the live view (plain mode), commands run exactly as asked.
func TestLiveRunPlainUntouched(t *testing.T) {
	var calls []call1
	c := execx.Cmd{Argv: []string{"sudo", "apt-get", "install", "-y", "jq"}, Inherit: true}
	_, _ = liveRun(record(&calls), func() holder { return nil })(context.Background(), c)
	if want := []call1{{c.Argv, true}}; !reflect.DeepEqual(calls, want) {
		t.Errorf("calls %v, want %v", calls, want)
	}
}
