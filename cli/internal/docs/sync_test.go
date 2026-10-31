package docs

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"makeit/internal/execx"
)

var now = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

// server is a fake docs-mcp-server: list answers with listJSON, scrape and refresh are recorded,
// and a scrape of a library named in block waits until the context ends.
type server struct {
	mu       sync.Mutex
	calls    [][]string
	listJSON string
	listErr  error
	block    map[string]bool
}

func (s *server) run(ctx context.Context, c execx.Cmd) (execx.Result, error) {
	s.mu.Lock()
	s.calls = append(s.calls, c.Argv)
	s.mu.Unlock()
	for _, want := range []string{"DOCS_MCP_TELEMETRY=false", "PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1"} {
		if !slices.Contains(c.Env, want) {
			return execx.Result{}, errors.New("missing env " + want)
		}
	}
	args := c.Argv[3:]
	switch args[0] {
	case "list":
		if s.listErr != nil {
			return execx.Result{}, s.listErr
		}
		return execx.Result{Stdout: s.listJSON}, nil
	case "scrape":
		if s.block[args[1]] {
			<-ctx.Done()
			return execx.Result{}, ctx.Err()
		}
	}
	return execx.Result{Stdout: "Successfully done"}, nil
}

func (s *server) did(verb string) []string {
	var out []string
	for _, c := range s.calls {
		if c[3] == verb {
			out = append(out, c[4])
		}
	}
	return out
}

func opts(s *server) Options {
	return Options{Run: s.run, Server: []string{"npx", "-y", "@arabold/docs-mcp-server@3.4.1"}, PerLib: 50 * time.Millisecond,
		Now: func() time.Time { return now }, Resolver: Resolver{}}
}

const listed = `[
 {"name": "golang.org/x/term", "versions": [{"version": "v0.47.0", "indexedAt": "2026-09-21T00:00:00.000Z"}]},
 {"name": "serde", "versions": [{"version": "1.0.200", "indexedAt": "2026-10-21T00:00:00.000Z"}, {"version": "1.0.100", "indexedAt": "2026-01-01T00:00:00.000Z"}]}
]`

func TestSyncPlan(t *testing.T) {
	s := &server{listJSON: listed}
	libs := []Lib{{"golang.org/x/term", "v0.47.0", "go"}, {"serde", "1.0.200", "cargo"}, {"anyhow", "1.0.100", "cargo"}}
	rep := Sync(context.Background(), libs, opts(s))
	if !slices.Equal(rep.Scraped, []Lib{libs[2]}) || !slices.Equal(rep.Refreshed, []Lib{libs[0]}) || !slices.Equal(rep.Current, []Lib{libs[1]}) || len(rep.Skipped) != 0 {
		t.Fatalf("report %+v", rep)
	}
	want := []string{"npx", "-y", "@arabold/docs-mcp-server@3.4.1", "scrape", "anyhow", "https://docs.rs/anyhow/1.0.100/", "--version", "1.0.100"}
	if !slices.ContainsFunc(s.calls, func(c []string) bool { return slices.Equal(c, want) }) {
		t.Errorf("scrape argv missing; calls %v", s.calls)
	}
	wantRefresh := []string{"npx", "-y", "@arabold/docs-mcp-server@3.4.1", "refresh", "golang.org/x/term", "--version", "v0.47.0"}
	if !slices.ContainsFunc(s.calls, func(c []string) bool { return slices.Equal(c, wantRefresh) }) {
		t.Errorf("refresh argv missing; calls %v", s.calls)
	}
	if lines := rep.Lines(); len(lines) == 0 || !strings.Contains(strings.Join(lines, "\n"), "anyhow 1.0.100") {
		t.Errorf("lines %q", lines)
	}
}

// Review Focus 1: one library that hangs or cannot be resolved never stops the others.
func TestSyncTimeoutAndUnreachable(t *testing.T) {
	s := &server{listJSON: `[]`, block: map[string]bool{"slow": true}}
	libs := []Lib{{"slow", "1.0.0", "cargo"}, {"nowhere", "1.0.0", "maven"}, {"serde", "1.0.200", "cargo"}}
	start := time.Now()
	rep := Sync(context.Background(), libs, opts(s))
	if time.Since(start) > 2*time.Second {
		t.Errorf("took %s", time.Since(start))
	}
	if !slices.Equal(rep.Scraped, []Lib{libs[2]}) || len(rep.Skipped) != 2 {
		t.Fatalf("report %+v", rep)
	}
	if rep.Skipped[0].Lib != libs[0] || rep.Skipped[0].Reason != "timed out after 50ms" {
		t.Errorf("slow: %+v", rep.Skipped[0])
	}
	if rep.Skipped[1].Lib != libs[1] || !strings.Contains(rep.Skipped[1].Reason, "no versioned docs URL") {
		t.Errorf("unresolvable: %+v", rep.Skipped[1])
	}
}

func TestSyncListFails(t *testing.T) {
	s := &server{listErr: errors.New("npx: not found")}
	libs := []Lib{{"serde", "1.0.200", "cargo"}, {"anyhow", "1.0.100", "cargo"}}
	rep := Sync(context.Background(), libs, opts(s))
	if len(rep.Skipped) != 2 || !strings.Contains(rep.Skipped[0].Reason, "npx: not found") || len(s.did("scrape")) != 0 {
		t.Errorf("report %+v, calls %v", rep, s.calls)
	}
}

// A sync is retried unless every library it left out had no docs URL to find.
func TestReportFailed(t *testing.T) {
	for _, c := range []struct {
		name string
		s    *server
		libs []Lib
		want bool
	}{
		{"clean", &server{listJSON: `[]`}, []Lib{{"serde", "1.0.200", "cargo"}}, false},
		{"unresolvable only", &server{listJSON: `[]`}, []Lib{{"nowhere", "1.0.0", "maven"}}, false},
		{"list failed", &server{listErr: errors.New("npx: not found")}, []Lib{{"serde", "1.0.200", "cargo"}}, true},
		{"timed out", &server{listJSON: `[]`, block: map[string]bool{"slow": true}}, []Lib{{"slow", "1.0.0", "cargo"}, {"nowhere", "1.0.0", "maven"}}, true},
	} {
		if got := Sync(context.Background(), c.libs, opts(c.s)).Failed(); got != c.want {
			t.Errorf("%s: Failed() = %v, want %v", c.name, got, c.want)
		}
	}
}
