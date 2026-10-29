package selfupdate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"makeit/internal/execx"
	"makeit/internal/minisign"
)

// The download caps: a checksums list or signature, and a binary (about 7 MB today).
var (
	maxList   int64 = 1 << 20
	maxBinary int64 = 64 << 20
)

// asset is the release file of version v for target.
func asset(v, target string) string { return "makeit-" + v + "-" + target + exeSuffix(target) }

func exeSuffix(target string) string {
	if strings.HasPrefix(target, "windows-") {
		return ".exe"
	}
	return ""
}

// url is the address of release v's file name.
func (e Env) url(v, name string) string {
	return e.Base + "/" + e.Repo + "/releases/download/v" + v + "/" + name
}

// get copies url's body into w: https only, redirects only to https, status 200, at most limit
// bytes, and the User-Agent makeit/<version> is all it sends.
func (e Env) get(ctx context.Context, url string, limit int64, w io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if req.URL.Scheme != "https" {
		return fmt.Errorf("refusing %s: not https", url)
	}
	req.Header.Set("User-Agent", "makeit/"+e.Version)
	c := *e.Client
	c.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if r.URL.Scheme != "https" {
			return fmt.Errorf("refusing a redirect to %s: not https", r.URL)
		}
		if len(via) >= 10 {
			return errors.New("too many redirects")
		}
		return nil
	}
	resp, err := c.Do(req)
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

// signedSha fetches release v's checksums.txt and its signature, verifies them with the keys this
// binary carries, and returns the sha256 listed for this target's asset.
func (e Env) signedSha(ctx context.Context, v string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var list, sig bytes.Buffer
	if err := e.get(ctx, e.url(v, "checksums.txt"), maxList, &list); err != nil {
		return "", err
	}
	if err := e.get(ctx, e.url(v, "checksums.txt.minisig"), maxList, &sig); err != nil {
		return "", err
	}
	comment, err := minisign.Verify(e.Keys, sig.String(), list.Bytes())
	if err != nil {
		return "", err
	}
	if want := "makeit " + v + " checksums.txt"; comment != want {
		return "", fmt.Errorf("signature: trusted comment %q, want %q", comment, want)
	}
	name := asset(v, e.Target)
	for line := range strings.SplitSeq(list.String(), "\n") {
		if f := strings.Fields(line); len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("checksums.txt of %s has no line for %s", v, name)
}

// download fetches release v's asset for this target into dir as .makeit.new-<pid>[.exe], mode
// 0755, and checks its sha256. Any failure removes the file.
func (e Env) download(ctx context.Context, v, sha, dir string) (path string, err error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	tmp := filepath.Join(dir, fmt.Sprintf(".makeit.new-%d%s", os.Getpid(), exeSuffix(e.Target)))
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	h := sha256.New()
	err = e.get(ctx, e.url(v, asset(v, e.Target)), maxBinary, io.MultiWriter(f, h))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != sha {
		return "", fmt.Errorf("%s: sha256 %s, but checksums.txt says %s", asset(v, e.Target), got, sha)
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return "", err
	}
	return tmp, nil
}

// check runs the new binary's `version --json` and requires release v for this target, built with
// a repository and release keys, so that it can update itself later.
func (e Env) check(ctx context.Context, path, v string) error {
	res, err := e.Run(ctx, execx.Cmd{Argv: []string{path, "version", "--json"}, Timeout: 10 * time.Second})
	if err != nil {
		return fmt.Errorf("the new makeit did not run: %w", err)
	}
	if res.Code != 0 {
		return fmt.Errorf("the new makeit exited %d: %s", res.Code, res.Text())
	}
	var got struct {
		Version, Target, Repo string
		Keys                  []string
	}
	if err := json.Unmarshal([]byte(res.Stdout), &got); err != nil {
		return fmt.Errorf("the new makeit's version --json: %w", err)
	}
	switch {
	case got.Version != v:
		return fmt.Errorf("the new makeit reports version %q, want %q", got.Version, v)
	case got.Target != e.Target:
		return fmt.Errorf("the new makeit reports target %q, want %q", got.Target, e.Target)
	case got.Repo == "":
		return errors.New("the new makeit reports no repo: it could not update itself")
	case len(got.Keys) == 0:
		return errors.New("the new makeit has no release keys: it could not update itself")
	}
	return nil
}
