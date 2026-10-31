// Package docs keeps the docs-mcp-server index in step with a project's dependencies
// (cli/ZERO-CONFIG.md section 2): it resolves each library to a versioned docs URL, scrapes
// what is missing and refreshes what is stale. The agent is not relied on to do it.
package docs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"makeit/internal/buildinfo"
)

// Lib is one library at one version. Ecosystem is go, npm, pypi or cargo.
type Lib struct{ Name, Version, Ecosystem string }

// Target is where a library's docs are scraped from, with the scrape options that suit the site.
type Target struct {
	Lib  Lib
	URL  string
	Args []string
}

// Resolver turns libraries into Targets. NPM and PyPI are the registry bases
// (https://registry.npmjs.org, https://pypi.org/pypi); Tags lists a git repository's tags
// (tag -> commit), from git ls-remote in production.
type Resolver struct {
	HTTP *http.Client
	NPM  string
	PyPI string
	Tags func(ctx context.Context, repo string) (map[string]string, error)
}

// ErrUnresolvable means no versioned docs URL was found; the library is listed, not failed.
var ErrUnresolvable = errors.New("no versioned docs URL")

var (
	goArgs = []string{"--max-depth", "0", "--scrape-mode", "fetch"}
	mdArgs = []string{"--include-pattern", "**/*.md"}
	ghRepo = regexp.MustCompile(`github\.com[/:]([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+?)(?:\.git)?(?:[/#?].*)?$`)
)

// Resolve finds l's docs URL without an agent and without guessing.
func (r Resolver) Resolve(ctx context.Context, l Lib) (Target, error) {
	t := Target{Lib: l}
	switch l.Ecosystem {
	case "go":
		t.URL, t.Args = "https://pkg.go.dev/"+l.Name+"@"+l.Version, goArgs
		if l.Name == "go" {
			t.URL = "https://pkg.go.dev/std@go" + strings.TrimPrefix(l.Version, "go")
		}
		return t, nil
	case "cargo":
		t.URL = "https://docs.rs/" + l.Name + "/" + l.Version + "/"
		return t, nil
	case "npm":
		var meta struct {
			Repository json.RawMessage `json:"repository"`
			Homepage   string          `json:"homepage"`
		}
		if err := r.get(ctx, r.NPM+"/"+l.Name+"/"+l.Version, &meta); err != nil {
			return t, err
		}
		var repo string
		if json.Unmarshal(meta.Repository, &repo) != nil {
			var obj struct{ URL string }
			_ = json.Unmarshal(meta.Repository, &obj)
			repo = obj.URL
		}
		if strings.HasPrefix(repo, "github:") {
			repo = "https://github.com/" + strings.TrimPrefix(repo, "github:")
		}
		return r.github(ctx, t, repo, meta.Homepage)
	case "pypi":
		var meta struct {
			Info struct {
				ProjectURLs map[string]string `json:"project_urls"`
				HomePage    string            `json:"home_page"`
			} `json:"info"`
		}
		if err := r.get(ctx, r.PyPI+"/"+l.Name+"/"+l.Version+"/json", &meta); err != nil {
			return t, err
		}
		for k, v := range meta.Info.ProjectURLs {
			if strings.EqualFold(k, "documentation") && v != "" {
				t.URL = v
				return t, nil
			}
		}
		repo := ""
		for _, v := range meta.Info.ProjectURLs {
			if ghRepo.MatchString(v) {
				repo = v
				break
			}
		}
		return r.github(ctx, t, repo, meta.Info.HomePage)
	}
	return t, fmt.Errorf("%s %s: %w", l.Ecosystem, l.Name, ErrUnresolvable)
}

// github points t at the repository's tree at the version's tag (v<version> or <version>),
// else at homepage.
func (r Resolver) github(ctx context.Context, t Target, repo, homepage string) (Target, error) {
	if m := ghRepo.FindStringSubmatch(repo); m != nil && r.Tags != nil {
		base := "https://github.com/" + m[1] + "/" + m[2]
		if tags, err := r.Tags(ctx, base+".git"); err == nil {
			for _, tag := range []string{"v" + t.Lib.Version, t.Lib.Version} {
				if _, ok := tags[tag]; ok {
					t.URL, t.Args = base+"/tree/"+tag, mdArgs
					return t, nil
				}
			}
		}
	}
	if homepage != "" {
		t.URL = homepage
		return t, nil
	}
	return t, fmt.Errorf("%s %s: %w", t.Lib.Ecosystem, t.Lib.Name, ErrUnresolvable)
}

// get fetches one registry page as JSON. A missing page is ErrUnresolvable.
func (r Resolver) get(ctx context.Context, url string, v any) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "makeit/"+buildinfo.Version)
	req.Header.Set("Accept", "application/json")
	c := r.HTTP
	if c == nil {
		c = http.DefaultClient
	}
	res, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", url, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d: %w", url, res.StatusCode, ErrUnresolvable)
	}
	return json.NewDecoder(res.Body).Decode(v)
}
