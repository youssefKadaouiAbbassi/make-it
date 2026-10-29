// Package doctor checks this machine against what the embedded catalog expects. It only reads:
// no write, no prompt, no lock, no network request (READ-ONLY-COMMANDS.md).
package doctor

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"makeit/internal/catalog"
	"makeit/internal/claudecli"
	"makeit/internal/execx"
	"makeit/internal/platform"
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
	r.platform(m)
	r.prereqs(m)
	unusable := r.claude(ctx, m, req, reqErr)
	r.settings(ctx, m, unusable)
	r.plugins(ctx, m, req, reqErr, unusable)
	r.record(m)
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
	r.add("makeit.build", "makeit", Info, fmt.Sprintf("makeit %s (%s), catalog sha256:%s", m.Version, m.Target, digest), "")
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
		fix := s.Prereq.Manual
		// The fix is for a person at a terminal, so sudo may prompt (no -n).
		if cmd, ok := platform.InstallCommand(s.Prereq, m.Platform.PM, true, false); ok {
			fix = execx.Quote(cmd.Argv)
		}
		r.add(id, "prereqs", level, fmt.Sprintf("%s is missing: %s", s.Prereq.ID, s.Prereq.Why), fix)
	}
}

// claude checks the Claude Code install and returns why it cannot be asked anything, or "" when
// it can: installed, --version works, and at least the catalog's floor.
func (r *report) claude(ctx context.Context, m Machine, req catalog.Requires, reqErr error) (unusable string) {
	const g = "claude"
	if len(m.Installs) == 0 {
		fix := ""
		if len(m.Routes) > 0 {
			fix = execx.Quote(m.Routes[0].Argv)
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
	const badCatalog = "the catalog's version floor cannot be read"
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
	fix := upgradeFix(m.Installs[0].Source, m.Platform.PM, m.BrewCask)
	belowHard, err := claudecli.Compare(v, hard)
	if err != nil {
		r.add(id, g, Error, fmt.Sprintf("the catalog's floor %q: %v", hard, err), "")
		return badCatalog
	}
	belowSoft, err := claudecli.Compare(v, soft)
	if err != nil {
		r.add(id, g, Error, fmt.Sprintf("the catalog's tested version %q: %v", soft, err), "")
		return badCatalog
	}
	switch {
	case belowHard < 0:
		r.add(id, g, Error, fmt.Sprintf("Claude Code %s is below %s, the lowest makeit works with", v, hard), fix)
		return fmt.Sprintf("Claude Code %s is below %s", v, hard)
	case belowSoft < 0:
		r.add(id, g, Warn, fmt.Sprintf("Claude Code %s is older than %s, the version this catalog was tested with", v, soft), fix)
	default:
		r.add(id, g, OK, "Claude Code "+v, "")
	}
	return ""
}

// upgradeFix is the upgrade command for an install (setup.md 78-82, 92, 291, 409, 435, 457, 471).
// A claude on PATH at no route's location is taken to come from the package manager.
func upgradeFix(source string, pm platform.PM, cask string) string {
	switch source {
	case "native":
		return "claude update"
	case "brew":
		if cask == "" {
			cask = "claude-code"
		}
		return "brew upgrade " + cask
	case "npm":
		return "npm install -g @anthropic-ai/claude-code@latest"
	}
	switch pm {
	case platform.APT:
		return "sudo apt update && sudo apt upgrade claude-code"
	case platform.DNF:
		return "sudo dnf upgrade claude-code"
	case platform.APK:
		return "apk update && apk upgrade claude-code"
	case platform.WinGet:
		return "winget upgrade Anthropic.ClaudeCode"
	}
	return "claude update"
}

// caskOf names the Homebrew cask a resolved bin/claude link points into: casks are staged at
// $(brew --caskroom)/<token>/<version> (docs.brew.sh Cask-Cookbook). "" when neither token is there.
func caskOf(resolved string) string {
	segs := strings.Split(filepath.ToSlash(resolved), "/")
	for _, cask := range []string{"claude-code@latest", "claude-code"} {
		if slices.Contains(segs, cask) {
			return cask
		}
	}
	return ""
}
