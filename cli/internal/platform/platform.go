// Package platform detects the machine (os, arch, WSL, package manager) and holds the
// prerequisite table and the Claude Code install routes (cli/research/docs/setup.md).
package platform

import (
	"crypto/x509"
	"slices"

	"makeit/internal/execx"
)

// PM is a package manager.
type PM string

// The package managers makeit knows, in detection order per OS.
const (
	Brew   PM = "brew"
	APT    PM = "apt"
	DNF    PM = "dnf"
	APK    PM = "apk"
	Pacman PM = "pacman"
	WinGet PM = "winget"
	Scoop  PM = "scoop"
	None   PM = "none"
)

// Platform is the machine makeit runs on. OS and Arch use Go's names.
type Platform struct {
	OS, Arch string
	PM       PM
	// WSL is Linux inside Windows: the Linux routes apply, Chrome may live on the Windows side.
	WSL bool
}

// Detect reads the platform; present reports whether a program is on PATH.
func Detect(goos, goarch string, present func(string) bool, getenv func(string) string) Platform {
	p := Platform{OS: goos, Arch: goarch, PM: None}
	var order []PM
	switch goos {
	case "darwin":
		order = []PM{Brew}
	case "windows":
		order = []PM{WinGet, Scoop}
	default:
		order = []PM{APT, DNF, APK, Pacman, Brew}
		p.WSL = getenv("WSL_DISTRO_NAME") != "" || getenv("WSL_INTEROP") != ""
	}
	for _, pm := range order {
		bin := string(pm)
		if pm == APT {
			bin = "apt-get"
		}
		if present(bin) {
			p.PM = pm
			break
		}
	}
	return p
}

// Prereq is one program the stack uses.
type Prereq struct {
	ID string
	// Why is what the stack uses it for, in plain words.
	Why      string
	Required bool
	// Bins are looked up on PATH; the first found counts.
	Bins []string
	// Present replaces the PATH lookup for rows that are not a program.
	Present func() bool
	// OS limits the row to one operating system; empty means every one.
	OS string
	// Pkgs are the install arguments per manager; a missing entry means no package route.
	Pkgs map[PM][]string
	// Manual is shown when there is no package route.
	Manual string
}

// Applies reports whether the row is checked on p.
func (r Prereq) Applies(p Platform) bool { return r.OS == "" || r.OS == p.OS }

// Prereqs is the table from the TypeScript draft plus the ca-certificates row (DESIGN.md 10).
var Prereqs = []Prereq{
	{
		ID: "ca-certificates", Why: "TLS for this binary, git and Claude Code's plugin installs", Required: true, OS: "linux",
		Present: hasCABundle,
		Pkgs:    map[PM][]string{APT: {"ca-certificates"}, DNF: {"ca-certificates"}, APK: {"ca-certificates"}, Pacman: {"ca-certificates"}},
	},
	{
		ID: "git", Why: "every repository, the handoff hook, the pre-commit hook", Required: true, Bins: []string{"git"},
		Pkgs: map[PM][]string{Brew: {"git"}, APT: {"git"}, DNF: {"git"}, APK: {"git"}, Pacman: {"git"}, WinGet: {"Git.Git"}},
	},
	{
		ID: "node", Why: "the two MCP servers start with npx (chrome-devtools-mcp, docs-mcp-server)", Required: true, Bins: []string{"node"},
		Pkgs:   map[PM][]string{Brew: {"node"}, APT: {"nodejs", "npm"}, DNF: {"nodejs", "npm"}, APK: {"nodejs", "npm"}, Pacman: {"nodejs", "npm"}, WinGet: {"OpenJS.NodeJS.LTS"}},
		Manual: "https://nodejs.org/en/download (22 or later)",
	},
	{
		ID: "jq", Why: "the ntfy and handoff hooks read Claude Code's JSON with it", Required: true, Bins: []string{"jq"},
		Pkgs: map[PM][]string{Brew: {"jq"}, APT: {"jq"}, DNF: {"jq"}, APK: {"jq"}, Pacman: {"jq"}, WinGet: {"jqlang.jq"}},
	},
	{
		ID: "gh", Why: "branch rulesets and the sha-pinning setting are applied through the GitHub API", Bins: []string{"gh"},
		Pkgs:   map[PM][]string{Brew: {"gh"}, APT: {"gh"}, DNF: {"gh"}, APK: {"github-cli"}, Pacman: {"github-cli"}, WinGet: {"GitHub.cli"}},
		Manual: "https://cli.github.com",
	},
	{
		ID: "chrome", Why: "chrome-devtools-mcp drives an installed Chrome (also used by sitespeed.io)",
		Bins:   []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome", "Google Chrome"},
		Pkgs:   map[PM][]string{Brew: {"--cask", "google-chrome"}, WinGet: {"Google.Chrome"}},
		Manual: "https://www.google.com/chrome/ (on Linux, the distribution's chromium package also works)",
	},
	{
		ID: "prek", Why: "the commit hook runner (reads .pre-commit-config.yaml, no Python needed)", Bins: []string{"prek"},
		Pkgs:   map[PM][]string{Brew: {"prek"}, WinGet: {"j178.prek"}},
		Manual: "uv tool install prek, or cargo install --locked prek, or a release binary from github.com/j178/prek",
	},
	{
		ID: "deja-vu", Why: "the memory plugin's local search binary", Bins: []string{"deja"},
		Pkgs:   map[PM][]string{Brew: {"deja-vu"}},
		Manual: "go install github.com/vshulcz/deja-vu/cmd/deja@latest, or a release binary from github.com/vshulcz/deja-vu",
	},
	{
		ID: "grype", Why: "the vulnerability scan, as a local command (CI runs it too)", Bins: []string{"grype"},
		Pkgs:   map[PM][]string{Brew: {"grype"}, WinGet: {"Anchore.Grype"}},
		Manual: "a release binary from github.com/anchore/grype (verify its checksum)",
	},
}

// Find returns the row with id; an unknown id is a programming error.
func Find(id string) Prereq {
	i := slices.IndexFunc(Prereqs, func(r Prereq) bool { return r.ID == id })
	if i < 0 {
		panic("platform: no prerequisite " + id)
	}
	return Prereqs[i]
}

// hasCABundle reports whether Go found any system root. A missing bundle gives an empty pool,
// and TLS then fails with "certificate signed by unknown authority" (measured on slim images).
func hasCABundle() bool {
	pool, err := x509.SystemCertPool()
	return err == nil && !pool.Equal(x509.NewCertPool())
}

// State is one row checked on this machine.
type State struct {
	Prereq  Prereq
	Present bool
}

// Status checks every row that applies to p.
func Status(p Platform, present func(string) bool) []State {
	var out []State
	for _, r := range Prereqs {
		if !r.Applies(p) {
			continue
		}
		ok := false
		if r.Present != nil {
			ok = r.Present()
		} else {
			ok = slices.ContainsFunc(r.Bins, present)
		}
		out = append(out, State{Prereq: r, Present: ok})
	}
	return out
}

var wingetAccept = []string{"--accept-source-agreements", "--accept-package-agreements", "--disable-interactivity"}

// InstallCommand builds the install for r with pm. Without a terminal, sudo runs with -n so a
// password prompt fails the step instead of hanging; interactive runs inherit the terminal.
func InstallCommand(r Prereq, pm PM, interactive, root bool) (execx.Cmd, bool) {
	pkgs, ok := r.Pkgs[pm]
	if !ok {
		return execx.Cmd{}, false
	}
	var argv, env []string
	switch pm {
	case Brew:
		argv = append([]string{"brew", "install"}, pkgs...)
	case WinGet:
		argv = append(append([]string{"winget", "install", "--exact", "--id"}, pkgs...), wingetAccept...)
	case APT:
		env = []string{"DEBIAN_FRONTEND=noninteractive"}
		argv = append([]string{"apt-get", "install", "-y"}, pkgs...)
	case DNF:
		argv = append([]string{"dnf", "install", "-y"}, pkgs...)
	case APK:
		argv = append([]string{"apk", "add"}, pkgs...)
	case Pacman:
		argv = append([]string{"pacman", "-S", "--noconfirm"}, pkgs...)
	default:
		return execx.Cmd{}, false
	}
	if pm != Brew && pm != WinGet && !root {
		sudo := []string{"sudo"}
		if !interactive {
			sudo = append(sudo, "-n")
		}
		// sudo resets the environment, so the variables go through env(1).
		if len(env) > 0 {
			sudo = append(append(sudo, "env"), env...)
			env = nil
		}
		argv = append(sudo, argv...)
	}
	return execx.Cmd{Argv: argv, Env: env, Inherit: interactive}, true
}

// Route is one way to install Claude Code.
type Route struct {
	Name string // brew, winget, npm, native: the --claude-route values
	Argv []string
	Note string
}

// ClaudeRoutes lists the routes for p in preference order (setup.md). native is last and is
// taken only when named, because it runs a downloaded script.
func ClaudeRoutes(p Platform, present func(string) bool) []Route {
	var rs []Route
	if (p.OS == "darwin" || p.OS == "linux") && present("brew") {
		rs = append(rs, Route{Name: "brew", Argv: []string{"brew", "install", "--cask", "claude-code@latest"}})
	}
	if p.OS == "windows" && present("winget") {
		rs = append(rs, Route{Name: "winget", Argv: append([]string{"winget", "install", "--exact", "--id", "Anthropic.ClaudeCode"}, wingetAccept...)})
	}
	if present("npm") {
		rs = append(rs, Route{Name: "npm", Argv: []string{"npm", "install", "-g", "@anthropic-ai/claude-code@latest"}, Note: "never with sudo"})
	}
	if p.OS == "windows" {
		rs = append(rs, Route{Name: "native", Argv: []string{"powershell", "-NoProfile", "-Command", "irm https://claude.ai/install.ps1 | iex"}})
	} else {
		rs = append(rs, Route{Name: "native", Argv: []string{"bash", "-c", "curl -fsSL https://claude.ai/install.sh | bash"}})
	}
	return rs
}

// AutoRoute is the first package route; false when only native is left.
func AutoRoute(p Platform, present func(string) bool) (Route, bool) {
	r := ClaudeRoutes(p, present)[0]
	return r, r.Name != "native"
}
