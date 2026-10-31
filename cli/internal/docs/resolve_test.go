package docs

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func registry(t *testing.T) *httptest.Server {
	t.Helper()
	pages := map[string]string{
		"/npm/zod/4.1.12":         `{"repository": {"type": "git", "url": "git+https://github.com/colinhacks/zod.git"}, "homepage": "https://zod.dev"}`,
		"/npm/plain/1.0.0":        `{"repository": "github:o/plain", "homepage": "https://plain.dev"}`,
		"/npm/notag/2.0.0":        `{"repository": {"url": "https://github.com/o/notag"}, "homepage": "https://notag.dev"}`,
		"/npm/bare/1.0.0":         `{"name": "bare"}`,
		"/npm/@scope/pkg/3.0.0":   `{"repository": {"url": "https://github.com/o/pkg.git"}}`,
		"/pypi/httpx/0.28.1/json": `{"info": {"project_urls": {"Documentation": "https://www.python-httpx.org/", "Source": "https://github.com/encode/httpx"}}}`,
		"/pypi/rich/14.0.0/json":  `{"info": {"project_urls": {"Source": "https://github.com/Textualize/rich"}, "home_page": ""}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ua := r.Header.Get("User-Agent"); ua == "" || ua[:7] != "makeit/" {
			t.Errorf("user agent %q", ua)
		}
		b, ok := pages[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(b))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestResolve(t *testing.T) {
	srv := registry(t)
	tags := map[string]map[string]string{
		"https://github.com/colinhacks/zod.git":  {"v4.1.12": "a"},
		"https://github.com/o/plain.git":         {"1.0.0": "b"},
		"https://github.com/o/notag.git":         {"v1.0.0": "c"},
		"https://github.com/o/pkg.git":           {"v3.0.0": "d"},
		"https://github.com/Textualize/rich.git": {"v14.0.0": "e"},
	}
	r := Resolver{HTTP: srv.Client(), NPM: srv.URL + "/npm", PyPI: srv.URL + "/pypi",
		Tags: func(_ context.Context, repo string) (map[string]string, error) {
			if m, ok := tags[repo]; ok {
				return m, nil
			}
			return nil, errors.New("no such repository")
		}}
	md := []string{"--include-pattern", "**/*.md"}
	for _, c := range []struct {
		lib  Lib
		url  string
		args []string
		err  error
	}{
		{Lib{"golang.org/x/term", "v0.47.0", "go"}, "https://pkg.go.dev/golang.org/x/term@v0.47.0", []string{"--max-depth", "0", "--scrape-mode", "fetch"}, nil},
		{Lib{"go", "1.27.3", "go"}, "https://pkg.go.dev/std@go1.27.3", []string{"--max-depth", "0", "--scrape-mode", "fetch"}, nil},
		{Lib{"serde", "1.0.200", "cargo"}, "https://docs.rs/serde/1.0.200/", nil, nil},
		{Lib{"zod", "4.1.12", "npm"}, "https://github.com/colinhacks/zod/tree/v4.1.12", md, nil},
		{Lib{"plain", "1.0.0", "npm"}, "https://github.com/o/plain/tree/1.0.0", md, nil},
		{Lib{"notag", "2.0.0", "npm"}, "https://notag.dev", nil, nil},
		{Lib{"@scope/pkg", "3.0.0", "npm"}, "https://github.com/o/pkg/tree/v3.0.0", md, nil},
		{Lib{"httpx", "0.28.1", "pypi"}, "https://www.python-httpx.org/", nil, nil},
		{Lib{"rich", "14.0.0", "pypi"}, "https://github.com/Textualize/rich/tree/v14.0.0", md, nil},
		{Lib{"bare", "1.0.0", "npm"}, "", nil, ErrUnresolvable},
		{Lib{"missing", "9.9.9", "npm"}, "", nil, ErrUnresolvable},
		{Lib{"missing", "9.9.9", "pypi"}, "", nil, ErrUnresolvable},
	} {
		got, err := r.Resolve(context.Background(), c.lib)
		if !errors.Is(err, c.err) || got.URL != c.url || !slices.Equal(got.Args, c.args) {
			t.Errorf("%s %s: got %q %v, %v; want %q %v, %v", c.lib.Ecosystem, c.lib.Name, got.URL, got.Args, err, c.url, c.args, c.err)
		}
		if err == nil && got.Lib != c.lib {
			t.Errorf("%s: target lib %+v", c.lib.Name, got.Lib)
		}
	}
}
