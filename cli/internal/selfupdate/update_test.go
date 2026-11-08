package selfupdate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"makeit/internal/execx"
	"makeit/internal/fsx"
	"makeit/internal/minisign/minisigntest"
	"makeit/internal/state"
)

// answers is a runner whose new binary reports version v for linux-x64, with keys.
func answers(v string, keys string) execx.Runner {
	return func(context.Context, execx.Cmd) (execx.Result, error) {
		return execx.Result{Stdout: `{"version":"` + v + `","target":"linux-x64","repo":"o/makeit","keys":` + keys + `}`}, nil
	}
}

// installed is machine(t) with a receipt for its binary, online against release 1.2.0 signed by k.
func installed(t *testing.T, k minisigntest.Key, files map[string][]byte) (Env, *server) {
	t.Helper()
	s := serve(t, files, "1.2.0")
	e := online(t, s, k)
	e.Run = answers("1.2.0", `["ABC"]`)
	writeReceipt(t, e, ours(e))
	return e, s
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func receiptOf(t *testing.T, e Env) *state.Receipt {
	t.Helper()
	r, _, err := state.LoadReceipt(e.ReceiptPath())
	if err != nil || r == nil {
		t.Fatalf("receipt %v, %v", r, err)
	}
	return r
}

func TestPick(t *testing.T) {
	k := minisigntest.NewKey()
	cases := []struct {
		name, latest, skip, to string
		allow                  bool
		want                   Pick
		err                    error
	}{
		{"newer", "1.2.0", "", "", false, Pick{To: "1.2.0", Latest: "1.2.0"}, nil},
		{"same", "1.1.0", "", "", false, Pick{Latest: "1.1.0"}, nil},
		{"latest below current (Review Focus 4)", "1.0.0", "", "", false, Pick{Latest: "1.0.0"}, nil},
		{"latest is the skip", "1.2.0", "1.2.0", "", false, Pick{Latest: "1.2.0", Skipped: true}, nil},
		{"newer than the skip", "1.3.0", "1.2.0", "", false, Pick{To: "1.3.0", Latest: "1.3.0"}, nil},
		{"--to older", "1.2.0", "", "1.0.0", false, Pick{}, ErrDowngrade},
		{"--to older allowed", "1.2.0", "", "1.0.0", true, Pick{To: "1.0.0"}, nil},
		{"--to the same", "1.2.0", "", "1.1.0", false, Pick{}, nil},
		{"--to the skip", "1.2.0", "1.2.0", "1.2.0", false, Pick{To: "1.2.0"}, nil},
		{"--to with the tag's v", "1.2.0", "", "v1.2.0", false, Pick{To: "1.2.0"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := serve(t, nil, c.latest)
			e := online(t, s, k)
			p, err := e.Pick(context.Background(), &state.Receipt{Version: "0.9.0", Skipped: c.skip}, c.to, c.allow)
			if !errors.Is(err, c.err) || p != c.want {
				t.Fatalf("%+v %v, want %+v %v", p, err, c.want, c.err)
			}
		})
	}
}

func TestUpdate(t *testing.T) {
	k := minisigntest.NewKey()
	e, _ := installed(t, k, releaseFiles(k, "1.2.0", "", []byte("new 1.2.0")))
	res, err := e.Update(context.Background(), "1.2.0")
	if err != nil || res != (Result{From: "1.1.0", To: "1.2.0", BinPath: e.Exe}) {
		t.Fatalf("%+v %v", res, err)
	}
	if got := read(t, e.Exe); got != "new 1.2.0" {
		t.Errorf("binary %q", got)
	}
	if got := read(t, filepath.Join(e.Roots.Stack, "self", "makeit-1.1.0")); got != "old" {
		t.Errorf("kept %q", got)
	}
	if r := receiptOf(t, e); r.Version != "1.2.0" || r.Previous == nil || *r.Previous != "1.1.0" {
		t.Errorf("receipt %+v", r)
	}
	if left := newFiles(t, filepath.Dir(e.Exe)); len(left) > 0 {
		t.Errorf("left %v", left)
	}
}

// unchanged checks that a failed Update left the binary, self/, the receipt and the bin dir as
// they were.
func unchanged(t *testing.T, e Env, receipt string) {
	t.Helper()
	if got := read(t, e.Exe); got != "old" {
		t.Errorf("binary %q", got)
	}
	if _, err := os.Stat(filepath.Join(e.Roots.Stack, "self")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("self/ exists: %v", err)
	}
	if got := read(t, e.ReceiptPath()); got != receipt {
		t.Errorf("receipt changed:\n%s", got)
	}
	if left := newFiles(t, filepath.Dir(e.Exe)); len(left) > 0 {
		t.Errorf("left %v", left)
	}
}

// A release binary built without keys passes the signature and the hash, is refused at the check,
// and nothing is swapped.
func TestUpdateKeylessNothingSwapped(t *testing.T) {
	k := minisigntest.NewKey()
	e, _ := installed(t, k, releaseFiles(k, "1.2.0", "", []byte("new 1.2.0")))
	e.Run = answers("1.2.0", `[]`)
	before := read(t, e.ReceiptPath())
	if _, err := e.Update(context.Background(), "1.2.0"); err == nil || !strings.Contains(err.Error(), "keys") {
		t.Fatalf("err %v, want one about keys", err)
	}
	unchanged(t, e, before)
}

func TestUpdateFailuresChangeNothing(t *testing.T) {
	k, other := minisigntest.NewKey(), minisigntest.NewKey()
	badSha := releaseFiles(k, "1.2.0", "", []byte("new 1.2.0"))
	badSha["v1.2.0/makeit-1.2.0-linux-x64"] = []byte("tampered")
	cases := []struct {
		name  string
		files map[string][]byte
		run   execx.Runner
	}{
		{"bad signature", releaseFiles(other, "1.2.0", "", []byte("new 1.2.0")), nil},
		{"sha mismatch", badSha, nil},
		{"check fails", releaseFiles(k, "1.2.0", "", []byte("new 1.2.0")), answers("1.0.0", `["ABC"]`)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, _ := installed(t, k, c.files)
			if c.run != nil {
				e.Run = c.run
			}
			before := read(t, e.ReceiptPath())
			if _, err := e.Update(context.Background(), "1.2.0"); err == nil {
				t.Fatal("updated")
			}
			unchanged(t, e, before)
		})
	}
}

// A crash after the swap left the receipt behind; the running version decides.
func TestUpdateReceiptBehind(t *testing.T) {
	k := minisigntest.NewKey()
	e, _ := installed(t, k, releaseFiles(k, "1.2.0", "", []byte("new 1.2.0")))
	r := ours(e)
	r.Version = "1.0.0"
	writeReceipt(t, e, r)
	p, err := e.Pick(context.Background(), &r, "", false)
	if err != nil || p.To != "1.2.0" {
		t.Fatalf("pick %+v %v", p, err)
	}
	if _, err := e.Update(context.Background(), p.To); err != nil {
		t.Fatal(err)
	}
	if got := receiptOf(t, e); got.Version != "1.2.0" || *got.Previous != "1.1.0" {
		t.Errorf("receipt %+v", got)
	}
	if _, err := os.Stat(filepath.Join(e.Roots.Stack, "self", "makeit-1.1.0")); err != nil {
		t.Errorf("kept as 1.1.0: %v", err)
	}
}

// Another makeit holds the lock.
func TestUpdateLockHeld(t *testing.T) {
	k := minisigntest.NewKey()
	e, s := installed(t, k, releaseFiles(k, "1.2.0", "", []byte("new 1.2.0")))
	lock, err := fsx.Acquire(filepath.Join(e.Roots.Stack, "lock"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	before := read(t, e.ReceiptPath())
	if _, err := e.Update(context.Background(), "1.2.0"); err == nil || !strings.Contains(err.Error(), "another makeit run holds") {
		t.Fatalf("err %v", err)
	}
	unchanged(t, e, before)
	if got := s.seen(); len(got) > 0 {
		t.Errorf("requests under someone else's lock: %v", got)
	}
}

func TestKeepTwo(t *testing.T) {
	dir, bin := filepath.Join(t.TempDir(), "self"), filepath.Join(t.TempDir(), "makeit")
	for _, v := range []string{"1.0.0", "1.1.0", "1.2.0"} {
		if err := os.WriteFile(bin, []byte(v), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := keep(dir, bin, v, false); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, en := range entries {
		names = append(names, en.Name())
	}
	if want := []string{"makeit-1.1.0", "makeit-1.2.0"}; !slices.Equal(names, want) {
		t.Errorf("kept %v, want %v", names, want)
	}
	if got := read(t, filepath.Join(dir, "makeit-1.2.0")); got != "1.2.0" {
		t.Errorf("makeit-1.2.0 holds %q", got)
	}
}

func TestSwapAside(t *testing.T) {
	dir := t.TempDir()
	bin, next := filepath.Join(dir, "makeit.exe"), filepath.Join(dir, ".makeit.new-1.exe")
	for p, body := range map[string]string{bin: "old", next: "new"} {
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := swap(next, bin, "1.1.0", true); err != nil {
		t.Fatal(err)
	}
	if read(t, bin) != "new" || read(t, bin+".old-1.1.0") != "old" {
		t.Errorf("after the swap: %q and %q", read(t, bin), read(t, bin+".old-1.1.0"))
	}
	if err := os.WriteFile(bin, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(bin + ".old-1.1.0"); err != nil {
		t.Fatal(err)
	}
	if err := swap(filepath.Join(dir, "missing"), bin, "1.1.0", true); err == nil {
		t.Fatal("swapped in a missing file")
	}
	if got := read(t, bin); got != "old" {
		t.Errorf("binary %q after a failed swap", got)
	}
	if _, err := os.Stat(bin + ".old-1.1.0"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the aside file stayed: %v", err)
	}
}

func TestClean(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{".makeit.new-1", "makeit.old-1.0.0", "other"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	clean(dir)
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "other" {
		t.Errorf("left %v", entries)
	}
}

func TestSkipCleared(t *testing.T) {
	k := minisigntest.NewKey()
	for _, c := range []struct{ v string }{{"1.3.0"}, {"1.2.0"}} {
		e, _ := installed(t, k, releaseFiles(k, c.v, "", []byte("new "+c.v)))
		e.Run = answers(c.v, `["ABC"]`)
		r := ours(e)
		r.Skipped = "1.2.0"
		writeReceipt(t, e, r)
		if _, err := e.Update(context.Background(), c.v); err != nil {
			t.Fatal(err)
		}
		if got := receiptOf(t, e); got.Skipped != "" {
			t.Errorf("%s: skip %q stayed", c.v, got.Skipped)
		}
	}
}

// `--to V` clears the skip of V only: a --to below the skip leaves it, so the release the user
// rolled back from does not come back with the next update.
func TestSkipKeptBelow(t *testing.T) {
	k := minisigntest.NewKey()
	e, _ := installed(t, k, releaseFiles(k, "1.1.5", "", []byte("new 1.1.5")))
	e.Run = answers("1.1.5", `["ABC"]`)
	r := ours(e)
	r.Skipped = "1.3.0"
	writeReceipt(t, e, r)
	if _, err := e.Update(context.Background(), "1.1.5"); err != nil {
		t.Fatal(err)
	}
	if got := receiptOf(t, e).Skipped; got != "1.3.0" {
		t.Errorf("skip %q after --to 1.1.5, want 1.3.0 kept", got)
	}
}

// An interrupt that arrives while the new binary is checked stops the update before anything is
// kept or swapped.
func TestUpdateCancelledBeforeSwap(t *testing.T) {
	k := minisigntest.NewKey()
	e, _ := installed(t, k, releaseFiles(k, "1.2.0", "", []byte("new 1.2.0")))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	check := answers("1.2.0", `["ABC"]`)
	e.Run = func(c context.Context, cmd execx.Cmd) (execx.Result, error) {
		cancel()
		return check(c, cmd)
	}
	before := read(t, e.ReceiptPath())
	if _, err := e.Update(ctx, "1.2.0"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v, want context.Canceled", err)
	}
	unchanged(t, e, before)
}

// The receipt's install time is when this makeit was swapped in, not when the first one was:
// rollback decides by it whether the last update brought the running makeit.
func TestUpdateRecordsInstalledAt(t *testing.T) {
	k := minisigntest.NewKey()
	e, _ := installed(t, k, releaseFiles(k, "1.2.0", "", []byte("new 1.2.0")))
	t1 := time.Date(2026, 11, 3, 13, 10, 0, 0, time.UTC)
	e.Now = func() time.Time { return t1 }
	r := ours(e)
	r.InstalledAt = t1.Add(-3 * time.Hour)
	writeReceipt(t, e, r)
	if _, err := e.Update(context.Background(), "1.2.0"); err != nil {
		t.Fatal(err)
	}
	if got := receiptOf(t, e).InstalledAt; !got.Equal(t1) {
		t.Errorf("InstalledAt %v, want %v (the swap)", got, t1)
	}
}
