package docs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"makeit/internal/execx"
)

// Options for Sync. Server is the argv that starts docs-mcp-server
// (npx -y @arabold/docs-mcp-server@<pin>, the pin of makeit's .mcp.json); PerLib caps the time
// one library may take to resolve and scrape (default 2 minutes).
type Options struct {
	Run      execx.Runner
	Server   []string
	PerLib   time.Duration
	Now      func() time.Time
	Resolver Resolver
}

// Skip is a library left out of this sync, and why.
type Skip struct {
	Lib    Lib
	Reason string
	// retry is false when the library has no docs URL to find (ErrUnresolvable): trying again
	// cannot help.
	retry bool
}

// Report is what one sync did.
type Report struct {
	Scraped, Refreshed, Current []Lib
	Skipped                     []Skip
}

// Failed is true when a library was left out for a reason other than ErrUnresolvable (the list
// failed, a scrape failed or timed out): the sync should run again, so its record is not saved.
func (r Report) Failed() bool {
	return slices.ContainsFunc(r.Skipped, func(s Skip) bool { return s.retry })
}

// stale is the age after which an indexed version is refreshed (ZERO-CONFIG.md section 2).
const stale = 30 * 24 * time.Hour

// serverEnv keeps docs-mcp-server quiet and light.
var serverEnv = []string{"DOCS_MCP_TELEMETRY=false", "PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1"}

// Sync lists the index, scrapes the libraries missing at their version and refreshes those
// indexed more than 30 days ago, one at a time. A library that cannot be resolved, fails or takes
// longer than PerLib is listed in Skipped; Sync itself never fails.
func Sync(ctx context.Context, libs []Lib, o Options) Report {
	var rep Report
	if o.PerLib == 0 {
		o.PerLib = 2 * time.Minute
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	indexed, err := o.list(ctx)
	if err != nil {
		for _, l := range libs {
			rep.Skipped = append(rep.Skipped, Skip{l, "could not list the docs index: " + err.Error(), true})
		}
		return rep
	}
	for _, l := range libs {
		if strings.HasPrefix(l.Name, "-") || strings.HasPrefix(l.Version, "-") {
			// It would reach docs-mcp-server's argv as an option.
			rep.Skipped = append(rep.Skipped, Skip{l, "a name or version starting with - is refused", false})
			continue
		}
		at, ok := indexed[l.Name+"@"+l.Version]
		switch {
		case ok && o.Now().Sub(at) <= stale:
			rep.Current = append(rep.Current, l)
		case ok:
			if err := o.timed(ctx, func(ctx context.Context) error {
				return o.server(ctx, "refresh", l.Name, "--version", l.Version)
			}); err != nil {
				rep.Skipped = append(rep.Skipped, Skip{l, o.reason(err), !errors.Is(err, ErrUnresolvable)})
			} else {
				rep.Refreshed = append(rep.Refreshed, l)
			}
		default:
			if err := o.timed(ctx, func(ctx context.Context) error {
				t, err := o.Resolver.Resolve(ctx, l)
				if err != nil {
					return err
				}
				args := append([]string{"scrape", l.Name, t.URL, "--version", l.Version}, t.Args...)
				return o.server(ctx, args...)
			}); err != nil {
				rep.Skipped = append(rep.Skipped, Skip{l, o.reason(err), !errors.Is(err, ErrUnresolvable)})
			} else {
				rep.Scraped = append(rep.Scraped, l)
			}
		}
	}
	return rep
}

// Lines is the report for people: one line per library that changed or was skipped.
func (r Report) Lines() []string {
	var out []string
	for _, l := range r.Scraped {
		out = append(out, "indexed "+l.Name+" "+l.Version)
	}
	for _, l := range r.Refreshed {
		out = append(out, "refreshed "+l.Name+" "+l.Version)
	}
	for _, s := range r.Skipped {
		out = append(out, "not indexed "+s.Lib.Name+" "+s.Lib.Version+": "+s.Reason)
	}
	if len(r.Current) > 0 {
		out = append(out, fmt.Sprintf("%d already indexed and current", len(r.Current)))
	}
	return out
}

var errTimedOut = errors.New("timed out")

// timed runs f with the per-library deadline.
func (o Options) timed(ctx context.Context, f func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, o.PerLib)
	defer cancel()
	err := f(ctx)
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return errTimedOut
	}
	return err
}

func (o Options) reason(err error) string {
	if errors.Is(err, errTimedOut) {
		return "timed out after " + o.PerLib.String()
	}
	return err.Error()
}

func (o Options) server(ctx context.Context, args ...string) error {
	res, err := o.Run(ctx, execx.Cmd{Argv: append(append([]string{}, o.Server...), args...), Env: serverEnv})
	if err != nil {
		return err
	}
	if res.Code != 0 {
		return fmt.Errorf("docs-mcp-server %s: exit %d: %s", args[0], res.Code, res.Text())
	}
	return nil
}

// list returns name@version -> indexedAt for every indexed version.
func (o Options) list(ctx context.Context) (map[string]time.Time, error) {
	res, err := o.Run(ctx, execx.Cmd{Argv: append(append([]string{}, o.Server...), "list", "--output", "json"), Env: serverEnv})
	if err != nil {
		return nil, err
	}
	if res.Code != 0 {
		return nil, fmt.Errorf("exit %d: %s", res.Code, res.Text())
	}
	var libs []struct {
		Name     string `json:"name"`
		Versions []struct {
			Version   string    `json:"version"`
			IndexedAt time.Time `json:"indexedAt"`
		} `json:"versions"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &libs); err != nil {
		return nil, fmt.Errorf("docs-mcp-server list: %w", err)
	}
	out := map[string]time.Time{}
	for _, l := range libs {
		for _, v := range l.Versions {
			out[l.Name+"@"+v.Version] = v.IndexedAt
		}
	}
	return out, nil
}
