// Package claudecli drives the `claude` CLI for plugin and marketplace work. It relies on this
// behaviour of Claude Code 2.1.290-2.1.311:
//
//   - `plugin list --json` and `marketplace list --json` print a multi-line JSON array, so the
//     whole stdout is parsed; action verbs print their result object on the last stdout line.
//   - `-y` is never passed: it would accept a marketplace-declared command the plan never showed.
//     A run that displays a command fails with shownCommand; the caller shows it and re-runs
//     with --accept-command <sha256> only under --yes.
//   - Exit 0 does not prove a plugin loads: an unresolved dependency also exits 0, so callers
//     check Plugin.Errors and Enabled in the list output.
//   - `claude doctor` reports invalid settings as text and exits 0; ValidateSettings parses it.
package claudecli

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"makeit/internal/execx"
)

// Floor is the lowest Claude Code that modifying commands accept: --json on the marketplace
// verbs needs it (plugins cli-reference, marketplace add/remove/update).
const Floor = "2.1.287"

// CLI runs one claude binary.
type CLI struct {
	Path string
	Run  execx.Runner
	// Env is added to every child's environment.
	Env []string
	// listings keeps each listing's stdout by its arguments for the process: the two listings
	// cost 0.6 s each and doctor, setup and update ask for them several times. Action clears it.
	mu       sync.Mutex
	listings map[string]string
	// gen moves with every forget, so a listing that was out while an action ran is not kept.
	gen uint64
}

// forget drops the cached listings: something may have changed.
func (c *CLI) forget() {
	c.mu.Lock()
	c.listings = nil
	c.gen++
	c.mu.Unlock()
}

// Timeouts: list and version are local reads; actions may clone a git repository.
const (
	readTimeout   = time.Minute
	actionTimeout = 10 * time.Minute
)

// run runs claude in a private, empty directory made for the command and removed after it. From
// the user's home or a project, Claude Code also reads that directory's .claude/settings.json as
// project settings, so makeit would see project declarations and a user-scope marketplace remove
// would leave the marketplace registered (measured with cli/scripts/measure-plugin-cli.sh); a shared
// directory such as /tmp would let another local user plant those settings. makeit manages user
// scope only.
func (c *CLI) run(ctx context.Context, timeout time.Duration, env []string, args ...string) (execx.Result, error) {
	dir, err := os.MkdirTemp("", "makeit-claude-")
	if err != nil {
		return execx.Result{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	return c.Run(ctx, execx.Cmd{Argv: append([]string{c.Path}, args...), Dir: dir,
		Env: append(append([]string{}, c.Env...), env...), Timeout: timeout})
}

// Version returns the version `claude --version` prints, such as 2.1.311.
func (c *CLI) Version(ctx context.Context) (string, error) {
	r, err := c.run(ctx, readTimeout, nil, "--version")
	if err != nil {
		return "", fmt.Errorf("claude --version: %w", err)
	}
	if r.Code != 0 {
		return "", fmt.Errorf("claude --version exited %d: %s", r.Code, r.Text())
	}
	v, _, _ := strings.Cut(strings.TrimSpace(r.Stdout), " ")
	if _, err := parseVersion(v); err != nil {
		return "", fmt.Errorf("claude --version printed %q: %w", strings.TrimSpace(r.Stdout), err)
	}
	return v, nil
}

// ErrBelowFloor is wrapped by CheckFloor.
var ErrBelowFloor = errors.New("claude code is too old")

// CheckFloor fails when have is below floor.
func CheckFloor(have, floor string) error {
	c, err := Compare(have, floor)
	if err != nil {
		return err
	}
	if c < 0 {
		return fmt.Errorf("%w: %s is installed, %s or later is needed: run claude update", ErrBelowFloor, have, floor)
	}
	return nil
}

// Compare orders two dotted numeric versions; a pre-release suffix after "-" is ignored.
func Compare(a, b string) (int, error) {
	x, err := parseVersion(a)
	if err != nil {
		return 0, err
	}
	y, err := parseVersion(b)
	if err != nil {
		return 0, err
	}
	for i := range max(len(x), len(y)) {
		var p, q int
		if i < len(x) {
			p = x[i]
		}
		if i < len(y) {
			q = y[i]
		}
		if p != q {
			if p < q {
				return -1, nil
			}
			return 1, nil
		}
	}
	return 0, nil
}

func parseVersion(v string) ([]int, error) {
	core, _, _ := strings.Cut(strings.TrimPrefix(v, "v"), "-")
	parts := strings.Split(core, ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("not a version: %q", v)
		}
		out[i] = n
	}
	return out, nil
}

// Plugin is one entry of `claude plugin list --json`.
type Plugin struct {
	ID            string `json:"id"`
	Version       string `json:"version"`
	Scope         string `json:"scope"`
	Enabled       bool   `json:"enabled"`
	InstallPath   string `json:"installPath"`
	HasUserConfig bool   `json:"hasUserConfig"`
	// ReadFromFolder is the source directory of a plugin that sessions load in place from its
	// marketplace's folder (Claude Code 2.1.289 or later); empty for a copied plugin.
	ReadFromFolder string `json:"readFromFolder"`
	// FolderVersion is the version in that folder's plugin.json; it moves before Version does.
	FolderVersion string   `json:"folderVersion"`
	Errors        []string `json:"errors"`
	Notes         []string `json:"notes"`
}

// Plugins lists installed plugins.
func (c *CLI) Plugins(ctx context.Context) ([]Plugin, error) {
	var out []Plugin
	return out, c.list(ctx, &out, "plugin", "list", "--json")
}

// Marketplace is one entry of `claude plugin marketplace list --json`.
type Marketplace struct {
	Name            string `json:"name"`
	Source          string `json:"source"` // github, git, url, directory, file
	Repo            string `json:"repo"`
	URL             string `json:"url"`
	Path            string `json:"path"`
	Ref             string `json:"ref"` // pinned branch or tag, github and git only
	InstallLocation string `json:"installLocation"`
}

// Marketplaces lists known marketplaces.
func (c *CLI) Marketplaces(ctx context.Context) ([]Marketplace, error) {
	var out []Marketplace
	return out, c.list(ctx, &out, "plugin", "marketplace", "list", "--json")
}

func (c *CLI) list(ctx context.Context, out any, args ...string) error {
	key := strings.Join(args, " ")
	c.mu.Lock()
	stdout, cached := c.listings[key]
	gen := c.gen
	c.mu.Unlock()
	if !cached {
		r, err := c.run(ctx, readTimeout, nil, args...)
		if err != nil {
			return fmt.Errorf("claude %s: %w", key, err)
		}
		if r.Code != 0 {
			return fmt.Errorf("claude %s exited %d: %s", key, r.Code, r.Text())
		}
		stdout = r.Stdout
	}
	if err := json.Unmarshal([]byte(stdout), out); err != nil {
		return fmt.Errorf("claude %s printed no JSON array: %w", key, err)
	}
	c.mu.Lock()
	if c.gen == gen {
		if c.listings == nil {
			c.listings = map[string]string{}
		}
		c.listings[key] = stdout
	}
	c.mu.Unlock()
	return nil
}

// Result is the object an action verb prints on the last stdout line under --json.
type Result struct {
	Command            string `json:"command"`
	Outcome            string `json:"outcome"` // ok or failed
	Message            string `json:"message"`
	PluginID           string `json:"pluginId"`
	Marketplace        string `json:"marketplace"`
	FailureCode        string `json:"failureCode"`
	AlreadyInGoalState bool   `json:"alreadyInGoalState"`
	// UpdateOutcome is plugin update's "updated" or "up_to_date". up_to_date also covers a pin that
	// moved while the plugin's version did not.
	UpdateOutcome string        `json:"updateOutcome"`
	ShownCommand  *ShownCommand `json:"shownCommand"`
	// Output is everything stdout printed before the result line, such as a displayed command.
	Output string `json:"-"`
}

// OK is true for success, and for enable or disable of a plugin already in that state.
func (r Result) OK() bool { return r.Outcome == "ok" || r.AlreadyInGoalState }

// ShownCommand is a marketplace-declared command a run displayed and did not run.
type ShownCommand struct {
	Sha256               string `json:"sha256"`
	AcceptCommandMatched *bool  `json:"acceptCommandMatched"`
	// Raw is the whole object, shown to the user as is: its other fields are not documented.
	Raw jsontext.Value `json:"-"`
}

// UnmarshalJSON keeps the raw object next to the fields makeit reads.
func (s *ShownCommand) UnmarshalJSON(b []byte) error {
	type plain ShownCommand
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*s = ShownCommand(p)
	s.Raw = append(jsontext.Value{}, b...)
	return nil
}

// Action runs `claude plugin <args...> --json` and parses its result line. A missing result
// line is an error; a failed outcome is returned as a Result for the caller to judge.
func (c *CLI) Action(ctx context.Context, args ...string) (Result, error) {
	args = append(append([]string{"plugin"}, args...), "--json")
	name := "claude " + strings.Join(args, " ")
	r, err := c.run(ctx, actionTimeout, nil, args...)
	c.forget()
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", name, err)
	}
	return parseAction(name, r)
}

func parseAction(name string, r execx.Result) (Result, error) {
	out := strings.TrimRight(r.Stdout, "\r\n \t")
	before, last := "", out
	if i := strings.LastIndexByte(out, '\n'); i >= 0 {
		before, last = out[:i], out[i+1:]
	}
	var res Result
	if err := json.Unmarshal([]byte(strings.TrimSpace(last)), &res); err != nil || res.Outcome == "" {
		return Result{}, fmt.Errorf("%s exited %d without a result line: %s", name, r.Code, r.Text())
	}
	res.Output = strings.TrimSpace(before)
	return res, nil
}

// Install installs a plugin at user scope. accept is the sha256 of a displayed command the
// user consented to (--yes); empty accepts nothing.
func (c *CLI) Install(ctx context.Context, id, accept string) (Result, error) {
	args := []string{"install", id, "--scope", "user"}
	if accept != "" {
		args = append(args, "--accept-command", accept)
	}
	return c.Action(ctx, args...)
}

// Update updates a plugin at user scope, with the same accept rule as Install.
func (c *CLI) Update(ctx context.Context, id, accept string) (Result, error) {
	args := []string{"update", id, "--scope", "user"}
	if accept != "" {
		args = append(args, "--accept-command", accept)
	}
	return c.Action(ctx, args...)
}

// Uninstall removes a plugin from user scope and keeps its data directory.
func (c *CLI) Uninstall(ctx context.Context, id string) (Result, error) {
	return c.Action(ctx, "uninstall", id, "--scope", "user", "--keep-data")
}

// Unneeded lists the auto-installed user-scope plugins no installed plugin needs any more, from
// `claude plugin prune --dry-run`, which removes nothing. The output is text with one
// "  <id> (<version>)" line per plugin (measured with cli/scripts/measure-plugin-cli.sh).
func (c *CLI) Unneeded(ctx context.Context) ([]string, error) {
	r, err := c.run(ctx, readTimeout, nil, "plugin", "prune", "--dry-run", "--scope", "user")
	if err != nil {
		return nil, fmt.Errorf("claude plugin prune --dry-run: %w", err)
	}
	if r.Code != 0 {
		return nil, fmt.Errorf("claude plugin prune --dry-run exited %d: %s", r.Code, r.Text())
	}
	var ids []string
	for _, line := range strings.Split(r.Stdout, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.HasPrefix(line, " ") && strings.Contains(f[0], "@") && strings.HasPrefix(f[1], "(") {
			ids = append(ids, f[0])
		}
	}
	return ids, nil
}

// AddMarketplace adds a marketplace at user scope from a path, owner/repo[#ref] or git URL.
func (c *CLI) AddMarketplace(ctx context.Context, source string) (Result, error) {
	return c.Action(ctx, "marketplace", "add", source, "--scope", "user")
}

// RemoveMarketplace removes a marketplace. Removal deletes the options, secrets and data of
// its plugins (cli-reference), so the caller backs up plugins/data/<id>/ first.
func (c *CLI) RemoveMarketplace(ctx context.Context, name string) (Result, error) {
	return c.Action(ctx, "marketplace", "remove", name, "--scope", "user")
}

// UpdateMarketplace refreshes a marketplace.
func (c *CLI) UpdateMarketplace(ctx context.Context, name string) (Result, error) {
	return c.Action(ctx, "marketplace", "update", name)
}

// Problem is one invalid setting `claude doctor` reported.
type Problem struct {
	File, Key, Reason string
}

func (p Problem) String() string { return p.Key + ": " + p.Reason }

// ValidateSettings runs `claude doctor` against candidate in a throwaway CLAUDE_CONFIG_DIR and
// returns what it found invalid in that file. Nothing in the real configuration is read.
func (c *CLI) ValidateSettings(ctx context.Context, candidate []byte) ([]Problem, error) {
	dir, err := os.MkdirTemp("", "makeit-validate-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	// doctor prints the resolved path; on macOS the temp dir sits behind a symlink.
	if d, err := filepath.EvalSymlinks(dir); err == nil {
		dir = d
	}
	file := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(file, candidate, 0o600); err != nil {
		return nil, err
	}
	r, err := c.run(ctx, readTimeout, []string{"CLAUDE_CONFIG_DIR=" + dir}, "doctor")
	if err != nil {
		return nil, fmt.Errorf("claude doctor: %w", err)
	}
	if r.Code != 0 {
		return nil, fmt.Errorf("claude doctor exited %d: %s", r.Code, r.Text())
	}
	var mine []Problem
	for _, p := range ParseDoctor(r.Stdout + "\n" + r.Stderr) {
		if p.File == file {
			mine = append(mine, p)
		}
	}
	return mine, nil
}

// ParseDoctor reads the "Invalid settings" block of `claude doctor` output:
//
//	Invalid settings
//	- <file> › <key>: <reason>
//	  Suggested fix: ...
//
// The format is not documented; testdata holds the 2.1.311 output as a contract test.
func ParseDoctor(out string) []Problem {
	var ps []Problem
	in := false
	for line := range strings.Lines(out) {
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "Invalid settings":
			in = true
		case !in:
		case line == "":
			in = false
		case strings.HasPrefix(line, "- "):
			file, rest, ok := strings.Cut(line[2:], " › ")
			if !ok {
				continue
			}
			key, reason, _ := strings.Cut(rest, ": ")
			ps = append(ps, Problem{File: file, Key: key, Reason: reason})
		}
	}
	return ps
}
