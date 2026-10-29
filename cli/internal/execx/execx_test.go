package execx

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCheckBatchArgs(t *testing.T) {
	if err := CheckBatchArgs(`C:\npm\claude.cmd`, []string{"plugin", "install", "x@y"}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"a&b", "100%", "x|y", `"q"`, "(x)", "a^b", "!v!"} {
		if err := CheckBatchArgs(`C:\npm\claude.CMD`, []string{bad}); err == nil {
			t.Errorf("%q accepted for a .cmd target", bad)
		}
	}
	if err := CheckBatchArgs(`C:\bin\claude.exe`, []string{"a&b"}); err != nil {
		t.Fatalf(".exe refused: %v", err)
	}
}

func TestQuote(t *testing.T) {
	got := Quote([]string{"echo", "a b", "it's", "", "plain"})
	if got != `echo 'a b' 'it'\''s' '' plain` {
		t.Fatalf("%s", got)
	}
}

func TestRunCodesOutputAndTimeout(t *testing.T) {
	ctx := context.Background()
	r, err := Run(ctx, Cmd{Argv: []string{"sh", "-c", "echo out; echo err >&2; exit 3"}})
	if err != nil || r.Code != 3 || strings.TrimSpace(r.Stdout) != "out" || strings.TrimSpace(r.Stderr) != "err" {
		t.Fatalf("%+v %v", r, err)
	}
	r, err = Run(ctx, Cmd{Argv: []string{"sh", "-c", "printf %s \"$X\""}, Env: []string{"X=set"}})
	if err != nil || r.Stdout != "set" {
		t.Fatalf("env: %+v %v", r, err)
	}
	if _, err := Run(ctx, Cmd{Argv: []string{"sh", "-c", "sleep 5"}, Timeout: 100 * time.Millisecond}); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("timeout: %v", err)
	}
	if r, err := Run(ctx, Cmd{Argv: []string{"no-such-program-xyz"}}); err == nil || r.Code != 127 {
		t.Fatalf("missing: %+v %v", r, err)
	}
	if _, err := Run(ctx, Cmd{}); err == nil {
		t.Fatal("empty argv accepted")
	}
}

// A run the caller cancels, such as on Ctrl-C, is not a timeout.
func TestRunCancelledIsNotATimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	if _, err := Run(ctx, Cmd{Argv: []string{"sh", "-c", "sleep 5"}}); err == nil || strings.Contains(err.Error(), "timed out") || !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled run: %v, want context.Canceled and no timeout", err)
	}
}
