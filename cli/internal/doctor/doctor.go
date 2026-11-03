// Package doctor checks this machine against what the embedded catalog expects. It only reads:
// no write, no prompt, no lock, no network request (READ-ONLY-COMMANDS.md).
package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"makeit/internal/catalog"
	"makeit/internal/claudecli"
	"makeit/internal/execx"
	"makeit/internal/fsx"
	"makeit/internal/platform"
	"makeit/internal/selfupdate"
	"makeit/internal/state"
)

// Level is how a check came out.
type Level string

// Levels. Skipped means the check could not apply; its message says why.
const (
	OK      Level = "ok"
	Info    Level = "info"
	Warn    Level = "warn"
	Error   Level = "error"
	Skipped Level = "skipped"
)

// Check is one line of the report.
type Check struct {
	ID      string `json:"id"`
	Group   string `json:"group"`
	Level   Level  `json:"level"`
	Message string `json:"message"`
	Fix     string `json:"fix,omitempty"`
}

// Machine is what the checks read. Gather fills it from this machine; tests fill it by hand.
type Machine struct {
	Version, Target, CatalogDigest string
	Platform                       platform.Platform
	Prereqs                        []platform.State
	Routes                         []platform.Route
	Installs                       []claudecli.Install
	// BrewCask is claude-code, claude-code@latest, or "" when unknown.
	BrewCask string
	// Claude runs Installs[0].Path; nil when Installs is empty.
	Claude                  *claudecli.CLI
	Catalog                 catalog.Source
	SettingsPath, StatePath string
	// ClaudeDir is Claude Code's configuration directory; Globals are the catalog paths of the
	// global files setup writes into it (templates/global/...).
	ClaudeDir string
	Globals   []string
	// Getenv reads the environment (os.Getenv in production); nil is an empty environment.
	Getenv func(string) string
	// Exe is this makeit, symlinks resolved; Self is what its receipt says; Dev is a development
	// build, which never updates itself.
	Exe  string
	Self selfupdate.Status
	Dev  bool
	// CatalogRecord is the catalog source state.json records, and CatalogDir its synced copy.
	CatalogRecord state.Catalog
	CatalogDir    string
}

type report struct{ checks []Check }

func (r *report) add(id, group string, level Level, msg, fix string) {
	r.checks = append(r.checks, Check{ID: id, Group: group, Level: level, Message: msg, Fix: fix})
}

// Run evaluates every check, in report order.
func Run(ctx context.Context, m Machine) []Check {
	r := &report{}
	req, reqErr := catalog.LoadRequires(m.Catalog)
	r.build(m)
	r.self(m)
	r.catalogSource(m)
	r.platform(m)
	r.prereqs(m)
	unusable := r.claude(ctx, m, req, reqErr)
	r.settings(ctx, m, req.Marketplace, unusable)
	st, info, stErr := state.LoadState(m.StatePath)
	r.plugins(ctx, m, req, reqErr, unusable, st)
	r.global(m)
	r.record(st, info, stErr)
	return r.checks
}

// Count counts the error and warn checks.
func Count(checks []Check) (errors, warnings int) {
	for _, c := range checks {
		switch c.Level {
		case Error:
			errors++
		case Warn:
			warnings++
		}
	}
	return errors, warnings
}

func (r *report) build(m Machine) {
	digest := strings.TrimPrefix(m.CatalogDigest, "sha256:")
	if len(digest) > 12 {
		digest = digest[:12]
	}
	r.add("makeit.build", "makeit", Info, fmt.Sprintf("makeit %s (%s), plugin set sha256:%s", m.Version, m.Target, digest), "")
}

// self reports how this makeit updates itself, and a rollback's skip (cli/SELF-UPDATE.md).
func (r *report) self(m Machine) {
	s := m.Self
	switch {
	case s.Err != nil:
		r.add("makeit.receipt", "makeit", Warn, "receipt.json: "+s.Err.Error(), "")
	case m.Dev:
		r.add("makeit.receipt", "makeit", Info, "development build: it does not update itself", "")
	case !s.Receipt:
		r.add("makeit.receipt", "makeit", Info, "no receipt: self update is off for this copy", m.Exe+" self install")
	case s.Ours:
		r.add("makeit.receipt", "makeit", Info, "installed by its script at "+s.BinPath, "")
	default:
		r.add("makeit.receipt", "makeit", Info, fmt.Sprintf("the receipt names %s; this makeit is %s", s.BinPath, m.Exe), "")
	}
	if s.Ours && s.Skipped != "" {
		r.add("makeit.skip", "makeit", Info, fmt.Sprintf("makeit %s is skipped: you rolled back from it", s.Skipped),
			"makeit self update --to "+s.Skipped)
	}
}

// catalogSource reports a directory or git catalog: where it comes from, git when it needs it,
// and whether its synced copy still matches what makeit wrote (cli/CATALOG-GIT.md). doctor uses
// no network, so a newer tag is update's notice.
func (r *report) catalogSource(m Machine) {
	c := m.CatalogRecord
	if c.Kind == "" || c.Kind == "embedded" {
		return
	}
	at := c.Tag
	if at == "" {
		at = strings.TrimPrefix(c.Resolved, "sha256:")
		at = at[:min(12, len(at))]
	}
	msg := fmt.Sprintf("plugin set %s %s at %s", c.Kind, c.Location, at)
	if c.Requested != nil && *c.Requested != "" {
		msg += " (" + *c.Requested + ")"
	}
	if c.Kind == "git" && !prereqPresent(m, "git") {
		r.add("catalog.source", "catalog", Error, fmt.Sprintf("the plugin set %s needs git: install git", c.Location), "")
		return
	}
	if mk := catalog.ReadMarker(m.CatalogDir); mk.Digest != "" {
		if t, err := catalog.DirTree(m.CatalogDir); err != nil || catalog.Digest(t.Manifest) != mk.Digest {
			r.add("catalog.source", "catalog", Warn, msg+": "+m.CatalogDir+" changed since makeit wrote it", "makeit update")
			return
		}
	}
	r.add("catalog.source", "catalog", Info, msg, "")
}

func prereqPresent(m Machine, id string) bool {
	for _, s := range m.Prereqs {
		if s.Prereq.ID == id {
			return s.Present
		}
	}
	return false
}

func (r *report) platform(m Machine) {
	wsl := ""
	if m.Platform.WSL {
		wsl = " (WSL)"
	}
	r.add("platform", "platform", Info, fmt.Sprintf("%s/%s%s, package manager %s", m.Platform.OS, m.Platform.Arch, wsl, m.Platform.PM), "")
}

func (r *report) prereqs(m Machine) {
	for _, s := range m.Prereqs {
		id := "prereq." + s.Prereq.ID
		if s.Present {
			r.add(id, "prereqs", OK, s.Prereq.ID+" found", "")
			continue
		}
		level := Warn
		if s.Prereq.Required {
			level = Error
		}
		if dir := offPath(s.Prereq, m.Getenv); dir != "" {
			r.add(id, "prereqs", level, fmt.Sprintf("%s is installed in %s, which is not on PATH: %s", s.Prereq.ID, dir, s.Prereq.Why),
				`export PATH="`+dir+`:$PATH"  (in your shell profile)`)
			continue
		}
		fix := s.Prereq.Manual
		// The fix is for a person at a terminal, so sudo may prompt (no -n).
		if s.Via != nil {
			fix = execx.Quote(s.Via)
		} else if cmd, ok := platform.InstallCommand(s.Prereq, m.Platform.PM, true, false); ok {
			fix = execx.Quote(cmd.Argv)
		}
		r.add(id, "prereqs", level, fmt.Sprintf("%s is missing: %s", s.Prereq.ID, s.Prereq.Why), fix)
	}
}

// offPath returns the folder where one of p's toolchain routes put its program, when the program
// is there but not on PATH; "" otherwise.
func offPath(p platform.Prereq, getenv func(string) string) string {
	if getenv == nil {
		return ""
	}
	for _, v := range p.Via {
		dir := v.Into(getenv)
		if dir == "" {
			continue
		}
		for _, b := range p.Bins {
			for _, name := range []string{b, b + ".exe"} {
				if fi, err := os.Stat(filepath.Join(dir, name)); err == nil && fi.Mode().IsRegular() {
					return dir
				}
			}
		}
	}
	return ""
}

// claude checks the Claude Code install and returns why it cannot be asked anything, or "" when
// it can: installed, --version works, and at least the catalog's floor.
func (r *report) claude(ctx context.Context, m Machine, req catalog.Requires, reqErr error) (unusable string) {
	const g = "claude"
	if len(m.Installs) == 0 {
		fix := ""
		if len(m.Routes) > 0 {
			fix = m.Routes[0].Text()
		}
		const why = "Claude Code is not installed"
		r.add("claude.installed", g, Error, why, fix)
		for _, id := range []string{"claude.on-path", "claude.other-installs", "claude.version"} {
			r.add(id, g, Skipped, why, "")
		}
		return why
	}
	first := m.Installs[0]
	r.add("claude.installed", g, OK, fmt.Sprintf("Claude Code at %s (%s)", first.Path, first.Source), "")
	if first.OnPath {
		r.add("claude.on-path", g, OK, "claude on PATH is "+first.Path, "")
	} else {
		r.add("claude.on-path", g, Warn, first.Path+" is not on PATH", "add "+filepath.Dir(first.Path)+" to PATH")
	}
	if len(m.Installs) > 1 {
		var all []string
		for _, in := range m.Installs {
			all = append(all, fmt.Sprintf("%s (%s)", in.Path, in.Source))
		}
		r.add("claude.other-installs", g, Warn, "more than one Claude Code: "+strings.Join(all, ", "),
			"keep one: https://code.claude.com/docs/en/troubleshoot-install.md#check-for-conflicting-installations")
	} else {
		r.add("claude.other-installs", g, OK, "one Claude Code installation", "")
	}
	return r.version(ctx, m, req, reqErr)
}

func (r *report) version(ctx context.Context, m Machine, req catalog.Requires, reqErr error) (unusable string) {
	const id, g = "claude.version", "claude"
	const badCatalog = "the plugin set's minimum Claude Code version cannot be read"
	if reqErr != nil {
		r.add(id, g, Error, badCatalog+": "+reqErr.Error(), "")
		return badCatalog
	}
	v, err := m.Claude.Version(ctx)
	if err != nil {
		r.add(id, g, Error, err.Error(), "")
		return "claude --version failed"
	}
	hard, soft := req.Requires.ClaudeCode.Hard, req.Requires.ClaudeCode.Soft
	fix := claudecli.UpgradeCommand(m.Installs[0].Source, m.Platform.PM, m.BrewCask)
	belowHard, err := claudecli.Compare(v, hard)
	if err != nil {
		r.add(id, g, Error, fmt.Sprintf("the plugin set's minimum Claude Code version %q: %v", hard, err), "")
		return badCatalog
	}
	belowSoft, err := claudecli.Compare(v, soft)
	if err != nil {
		r.add(id, g, Error, fmt.Sprintf("the plugin set's tested version %q: %v", soft, err), "")
		return badCatalog
	}
	switch {
	case belowHard < 0:
		r.add(id, g, Error, fmt.Sprintf("Claude Code %s is below %s, the lowest makeit works with", v, hard), fix)
		return fmt.Sprintf("Claude Code %s is below %s", v, hard)
	case belowSoft < 0:
		r.add(id, g, Warn, fmt.Sprintf("Claude Code %s is older than %s, the version this plugin set was tested with", v, soft), fix)
	default:
		r.add(id, g, OK, "Claude Code "+v, "")
	}
	return ""
}

// global checks the global files setup writes: ok when equal to the template, info when the
// user's own file is there instead (adopted), warn when missing.
func (r *report) global(m Machine) {
	const g = "global"
	for _, src := range m.Globals {
		rel := strings.TrimPrefix(src, "templates/global/")
		id := "global." + rel
		want, err := m.Catalog.ReadFile(src)
		if err != nil {
			r.add(id, g, Error, "the plugin set's "+src+": "+err.Error(), "")
			continue
		}
		path := filepath.Join(m.ClaudeDir, filepath.FromSlash(rel))
		have, found, err := fsx.ReadOptional(path)
		switch {
		case err != nil:
			r.add(id, g, Error, path+" cannot be read: "+err.Error(), "")
		case !found:
			r.add(id, g, Warn, path+" is missing", "makeit setup")
		case string(have) == string(want):
			r.add(id, g, OK, path+" is makeit's", "")
		default:
			r.add(id, g, Info, path+" is yours (adopted): it differs from makeit's and setup leaves it alone", "")
		}
	}
}
