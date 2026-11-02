package ui

import (
	"bytes"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func liveAsker(input string) (*UI, *bytes.Buffer) {
	var out bytes.Buffer
	u := NewFromReader(strings.NewReader(input), &out, true)
	u.Mode = Live
	return u, &out
}

func TestConfirmLiveEnterTakesDefault(t *testing.T) {
	u, out := liveAsker("\r")
	ok, err := u.Confirm("Apply 4 changes?", true)
	if err != nil || !ok {
		t.Fatalf("confirm = %v, %v; want yes", ok, err)
	}
	if n := strings.Count(out.String(), "Apply 4 changes?"); n == 0 {
		t.Errorf("question not shown: %q", out.String())
	}
}

func TestConfirmLiveNoKey(t *testing.T) {
	u, _ := liveAsker("n")
	if ok, err := u.Confirm("Apply?", true); err != nil || ok {
		t.Fatalf("confirm = %v, %v; want no", ok, err)
	}
}

// A lone Esc byte in a pipe waits for the rest of an escape sequence; a terminal times it out. The
// test uses Ctrl-C, the same abort path; the recording checks the Esc key itself.
func TestConfirmLiveAbortDeclines(t *testing.T) {
	u, _ := liveAsker("\x03")
	if ok, err := u.Confirm("Apply?", true); err != nil || ok {
		t.Fatalf("confirm = %v, %v; want a plain no", ok, err)
	}
}

// A closed input (the terminal went away, a pipe ended) must end the question, not hang it.
func TestConfirmLiveInputClosed(t *testing.T) {
	done := make(chan bool, 1)
	go func() {
		u, _ := liveAsker("")
		ok, _ := u.Confirm("Apply?", true)
		done <- ok
	}()
	select {
	case ok := <-done:
		if ok {
			t.Error("a closed input answered yes")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Confirm still waiting 5s after its input closed")
	}
}

func TestSelectLiveArrowDown(t *testing.T) {
	u, _ := liveAsker("\x1b[B\r")
	i, err := u.Select("What next?", []string{"Update now", "Doctor", "Nothing"}, 0)
	if err != nil || i != 1 {
		t.Fatalf("select = %d, %v; want 1", i, err)
	}
}

// A pipe is not a terminal: it keeps the wrapper that ends the question when the input closes.
func TestFormInputPipeWrapped(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if _, ok := New(r, &bytes.Buffer{}, true, false).formInput().(*abortOnEOF); !ok {
		t.Error("a pipe went to Huh unwrapped")
	}
	if _, ok := NewFromReader(strings.NewReader(""), &bytes.Buffer{}, true).formInput().(*abortOnEOF); !ok {
		t.Error("a reader went to Huh unwrapped")
	}
}

func TestEscLeavesAQuestion(t *testing.T) {
	if !slices.Contains(keymap().Quit.Keys(), "esc") {
		t.Error("Esc does not leave a question")
	}
}
