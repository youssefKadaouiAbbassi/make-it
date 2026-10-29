// Package release checks for a newer makeit with one HEAD request to the repository's
// releases/latest page. GitHub answers it with a redirect to the latest tag, so there is no API
// call, no rate limit and no token (DESIGN.md section 3).
package release

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"makeit/internal/claudecli"
)

// timeout bounds the check; an offline machine gets one line, not a wait.
const timeout = 5 * time.Second

// Latest returns the version of repo's ("owner/name") latest release at base
// (https://github.com), from the tag its releases/latest redirect names. The redirect is not followed.
func Latest(ctx context.Context, c *http.Client, base, repo string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client := *c
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, strings.TrimRight(base, "/")+"/"+repo+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 300 || resp.StatusCode > 399 {
		return "", fmt.Errorf("releases/latest answered %s, not a redirect", resp.Status)
	}
	loc := resp.Header.Get("Location")
	_, tag, ok := strings.Cut(loc, "/releases/tag/")
	v, vok := strings.CutPrefix(tag, "v")
	if !ok || !vok || v == "" || strings.Contains(v, "/") {
		return "", fmt.Errorf("releases/latest redirected to %q, not a release tag", loc)
	}
	return v, nil
}

// Notice is the line update prints about a newer makeit. It is empty when repo is empty (a dev
// build: no request is made), when current is the latest or newer, or when current is not a
// release version; a failed check is one line saying so.
func Notice(ctx context.Context, c *http.Client, base, repo, current string) string {
	if repo == "" {
		return ""
	}
	latest, err := Latest(ctx, c, base, repo)
	if err != nil {
		return "could not check for a newer makeit: " + err.Error()
	}
	if n, err := claudecli.Compare(latest, current); err != nil || n <= 0 {
		return ""
	}
	return fmt.Sprintf("makeit %s is available (this is %s)", latest, current)
}
