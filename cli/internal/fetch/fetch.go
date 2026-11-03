// Package fetch downloads over HTTPS with the limits every makeit download keeps.
package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// Get copies url's body into w: https only, redirects only to https and at most ten, status 200,
// at most limit bytes, and the User-Agent is all it sends.
func Get(ctx context.Context, c *http.Client, userAgent, url string, limit int64, w io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if req.URL.Scheme != "https" {
		return fmt.Errorf("refusing %s: not https", url)
	}
	req.Header.Set("User-Agent", userAgent)
	cl := *c
	cl.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if r.URL.Scheme != "https" {
			return fmt.Errorf("refusing a redirect to %s: not https", r.URL)
		}
		if len(via) >= 10 {
			return errors.New("too many redirects")
		}
		return nil
	}
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %s", url, resp.Status)
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return err
	}
	if n > limit {
		return fmt.Errorf("%s is larger than %d bytes", url, limit)
	}
	return nil
}
