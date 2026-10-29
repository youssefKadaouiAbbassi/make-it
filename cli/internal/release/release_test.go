package release

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// server answers HEAD /o/makeit/releases/latest with status and location; every other request
// (a followed redirect) is counted in hits.
func server(t *testing.T, status int, location string, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/o/makeit/releases/latest" || r.Method != http.MethodHead {
			hits.Add(1)
			return
		}
		if location != "" {
			w.Header().Set("Location", location)
		}
		w.WriteHeader(status)
	}))
}

func TestLatestReadsLocation(t *testing.T) {
	var hits atomic.Int32
	srv := server(t, http.StatusFound, "https://github.com/o/makeit/releases/tag/v1.4.2", &hits)
	v, err := Latest(context.Background(), srv.Client(), srv.URL, "o/makeit")
	if err != nil || v != "1.4.2" {
		t.Errorf("Latest = %q, %v; want 1.4.2", v, err)
	}
}

func TestLatestDoesNotFollow(t *testing.T) {
	var hits atomic.Int32
	srv := server(t, http.StatusFound, "/o/makeit/releases/tag/v1.4.2", &hits)
	if _, err := Latest(context.Background(), srv.Client(), srv.URL, "o/makeit"); err != nil {
		t.Fatal(err)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("the redirect was followed (%d requests to its target)", n)
	}
}

func TestNoticeNewer(t *testing.T) {
	var hits atomic.Int32
	srv := server(t, http.StatusFound, "/o/makeit/releases/tag/v1.4.2", &hits)
	if got := Notice(context.Background(), srv.Client(), srv.URL, "o/makeit", "1.2.0"); got != "makeit 1.4.2 is available (this is 1.2.0)" {
		t.Errorf("Notice = %q", got)
	}
}

func TestNoticeEqualOrOlder(t *testing.T) {
	var hits atomic.Int32
	srv := server(t, http.StatusFound, "/o/makeit/releases/tag/v1.4.2", &hits)
	for _, current := range []string{"1.4.2", "2.0.0", "dev"} {
		if got := Notice(context.Background(), srv.Client(), srv.URL, "o/makeit", current); got != "" {
			t.Errorf("current %s: Notice = %q, want nothing", current, got)
		}
	}
}

func TestNoticeTimeout(t *testing.T) {
	srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	got := Notice(ctx, srv.Client(), srv.URL, "o/makeit", "1.0.0")
	if !strings.HasPrefix(got, "could not check for a newer makeit: ") {
		t.Errorf("Notice = %q, want could not check", got)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("took %s; the deadline is not honoured", d)
	}
}

func TestNoticeMissingLocation(t *testing.T) {
	var hits atomic.Int32
	for _, c := range []struct {
		status   int
		location string
	}{{http.StatusFound, ""}, {http.StatusOK, ""}, {http.StatusFound, "/o/makeit/releases"}} {
		srv := server(t, c.status, c.location, &hits)
		if got := Notice(context.Background(), srv.Client(), srv.URL, "o/makeit", "1.0.0"); !strings.HasPrefix(got, "could not check for a newer makeit: ") {
			t.Errorf("%d %q: Notice = %q", c.status, c.location, got)
		}
	}
}

func TestNoticeNoRepoNoRequest(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewTestServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	if got := Notice(context.Background(), srv.Client(), srv.URL, "", "1.0.0"); got != "" || hits.Load() != 0 {
		t.Errorf("Notice = %q with %d requests; a dev build checks nothing", got, hits.Load())
	}
}
