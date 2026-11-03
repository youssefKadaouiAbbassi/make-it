package plan

import (
	"context"
	"errors"
	"testing"
)

func steps(log *[]string) []Step {
	return []Step{
		{ID: "a", Title: "A", Detail: "does a", Run: func(context.Context) (string, error) { *log = append(*log, "a"); return "", nil }},
		{ID: "b", Title: "B", Already: func(context.Context) (bool, error) { return true, nil }, Run: func(context.Context) (string, error) { *log = append(*log, "b"); return "", nil }},
		{ID: "c", Title: "C", Already: func(context.Context) (bool, error) { return false, ErrNotYet }, Run: func(context.Context) (string, error) { return "", errors.New("boom") }},
		{ID: "d", Title: "D", Run: func(context.Context) (string, error) { *log = append(*log, "d"); return "", nil }},
	}
}

func statuses(r Report) []Status {
	out := make([]Status, len(r.Outcomes))
	for i, o := range r.Outcomes {
		out[i] = o.Status
	}
	return out
}

func eq(t *testing.T, got, want []Status) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestDryRunExecutesNothing(t *testing.T) {
	var log []string
	r := Execute(context.Background(), steps(&log), Options{DryRun: true})
	if len(log) != 0 {
		t.Fatalf("ran %v", log)
	}
	eq(t, statuses(r), []Status{WouldChange, Already, WouldChange, WouldChange})
	if r.Outcomes[2].Detail != ErrNotYet.Error() {
		t.Fatalf("detail %q", r.Outcomes[2].Detail)
	}
}

func TestStopsAtFirstFailure(t *testing.T) {
	var log []string
	var seen []string
	r := Execute(context.Background(), steps(&log), Options{After: func(o Outcome) { seen = append(seen, o.ID) }})
	if len(log) != 1 || log[0] != "a" {
		t.Fatalf("ran %v", log)
	}
	eq(t, statuses(r), []Status{Done, Already, Failed, Skipped})
	if !r.Failed() || r.Summary() != "1 done, 1 already, 1 skipped, 1 failed" || len(seen) != 4 {
		t.Fatalf("summary %q seen %v", r.Summary(), seen)
	}
}

func TestKeepGoing(t *testing.T) {
	var log []string
	r := Execute(context.Background(), steps(&log), Options{KeepGoing: true})
	eq(t, statuses(r), []Status{Done, Already, Failed, Done})
}

func TestInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var log []string
	s := steps(&log)
	s[0].Run = func(context.Context) (string, error) { cancel(); return "", context.Canceled }
	r := Execute(ctx, s, Options{})
	eq(t, statuses(r), []Status{Interrupted, Skipped, Skipped, Skipped})
	cancel()
	r = Execute(ctx, steps(&log), Options{})
	eq(t, statuses(r), []Status{Interrupted, Interrupted, Interrupted, Interrupted})
}

func TestSkipAndCheckError(t *testing.T) {
	s := []Step{
		{ID: "x", Run: func(context.Context) (string, error) { return "declined", ErrSkip }},
		{ID: "y", Already: func(context.Context) (bool, error) { return false, errors.New("unreadable") }, Run: nil},
	}
	r := Execute(context.Background(), s, Options{})
	eq(t, statuses(r), []Status{Skipped, Failed})
	if Execute(context.Background(), nil, Options{}).Summary() != "nothing to do" {
		t.Fatal("empty summary")
	}
}

func TestConsentMatrix(t *testing.T) {
	cases := []struct {
		interactive, yes, dry bool
		want                  Mode
	}{
		{false, false, false, NeedConsent},
		{true, false, false, Prompt},
		{false, true, false, Apply},
		{true, true, false, Apply},
		{false, false, true, DryRunOnly},
		{true, true, true, DryRunOnly},
	}
	for _, c := range cases {
		if got := Decide(c.interactive, c.yes, c.dry); got != c.want {
			t.Errorf("interactive=%v yes=%v dry=%v: got %v want %v", c.interactive, c.yes, c.dry, got, c.want)
		}
	}
}

func TestBeforeCalledPerStep(t *testing.T) {
	var log, seen []string
	Execute(context.Background(), steps(&log), Options{DryRun: true, Before: func(s Step) { seen = append(seen, s.ID) }})
	if len(seen) != 4 || seen[0] != "a" || seen[3] != "d" {
		t.Errorf("Before saw %v, want a b c d", seen)
	}
}
