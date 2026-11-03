package fetch

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func server(t *testing.T, body string) (*httptest.Server, *http.Client) {
	t.Helper()
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s, s.Client()
}

func TestGet(t *testing.T) {
	s, c := server(t, "hello")
	var b bytes.Buffer
	if err := Get(context.Background(), c, "makeit/test", s.URL+"/x", 100, &b); err != nil || b.String() != "hello" {
		t.Errorf("%q %v", b.String(), err)
	}
}

func TestGetRefusesHTTP(t *testing.T) {
	_, c := server(t, "hello")
	if err := Get(context.Background(), c, "makeit/test", "http://example.invalid/x", 100, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "not https") {
		t.Errorf("err %v", err)
	}
}

func TestGetLimit(t *testing.T) {
	s, c := server(t, strings.Repeat("x", 50))
	if err := Get(context.Background(), c, "makeit/test", s.URL+"/x", 10, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "larger than 10 bytes") {
		t.Errorf("err %v", err)
	}
}

func TestGetStatus(t *testing.T) {
	s, c := server(t, "")
	if err := Get(context.Background(), c, "makeit/test", s.URL+"/missing", 10, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("err %v", err)
	}
}
