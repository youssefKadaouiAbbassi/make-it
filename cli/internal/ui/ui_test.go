package ui

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestPrompts(t *testing.T) {
	var out bytes.Buffer
	u := NewFromReader(strings.NewReader("maybe\ny\n\n9\n2\n1 3\n\nhello\n"), &out, true)
	if ok, err := u.Confirm("go?", false); err != nil || !ok {
		t.Fatalf("confirm: %v %v", ok, err)
	}
	if ok, err := u.Confirm("go?", true); err != nil || !ok {
		t.Fatalf("confirm default: %v %v", ok, err)
	}
	if i, err := u.Select("which?", []string{"a", "b"}, 0); err != nil || i != 1 {
		t.Fatalf("select: %v %v", i, err)
	}
	got, err := u.Multi("types?", []string{"a", "b", "c"}, []bool{false, true, false})
	if err != nil || !got[0] || got[1] || !got[2] {
		t.Fatalf("multi: %v %v", got, err)
	}
	got, err = u.Multi("types?", []string{"a", "b"}, []bool{false, true})
	if err != nil || got[0] || !got[1] {
		t.Fatalf("multi keep: %v %v", got, err)
	}
	if s, err := u.Text("name", "def"); err != nil || s != "hello" {
		t.Fatalf("text: %q %v", s, err)
	}
	if _, err := u.Confirm("again?", true); !errors.Is(err, ErrCancelled) {
		t.Fatalf("EOF: %v", err)
	}
	if !strings.Contains(out.String(), "answer y or n") || !strings.Contains(out.String(), "pick 1 to 2") {
		t.Fatalf("no retry hints:\n%s", out.String())
	}
}

func TestNoTerminalRefusesToAsk(t *testing.T) {
	u := NewFromReader(strings.NewReader("y\n"), &bytes.Buffer{}, false)
	if _, err := u.Confirm("go?", true); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("got %v", err)
	}
}

func TestQuietAndColor(t *testing.T) {
	var out bytes.Buffer
	u := NewFromReader(strings.NewReader(""), &out, false)
	u.Quiet = true
	u.Info("hidden")
	u.Ok("hidden")
	u.Warn("shown")
	if s := out.String(); strings.Contains(s, "hidden") || !strings.Contains(s, "shown") {
		t.Fatalf("%q", s)
	}
	t.Setenv("NO_COLOR", "1")
	if ColorWanted("auto", true) || !ColorWanted("always", false) {
		t.Fatal("NO_COLOR or --color=always ignored")
	}
}
