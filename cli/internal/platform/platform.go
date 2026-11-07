// Package platform detects the machine (os, arch, WSL, package manager) and holds the
// prerequisite table and the Claude Code install routes (cli/SETUP.md; Claude Code setup docs).
package platform

import (
	"cmp"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"makeit/internal/execx"
	"makeit/internal/fetch"
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
	// When limits the row to machines with one of these programs on PATH (the toolchain it
	// serves); empty means every machine.
	When []string
	// Via are toolchain routes, tried in order before the package manager: the first whose Bin
	// is on PATH is the fix.
	Via []Via
	// PkgFile is a .deb or .rpm the vendor publishes for x86-64, installed with the package manager.
	PkgFile map[PM]string
	// Release is the program's GitHub release, the route when no package or toolchain has it.
	Release *Release
}

// Via is an install command that needs one program, such as rustup or npm.
type Via struct {
	Bin  string
	Argv []string
	// Env is KEY=value for the command. A "~" starting an element of Argv or an Env value is the
	// home directory.
	Env []string
}

// Cmd is the route's command with "~" expanded to home.
func (v Via) Cmd(home string) execx.Cmd {
	argv := make([]string, len(v.Argv))
	for i, a := range v.Argv {
		argv[i] = expandHome(a, home)
	}
	var env []string
	for _, kv := range v.Env {
		k, val, _ := strings.Cut(kv, "=")
		env = append(env, k+"="+expandHome(val, home))
	}
	return execx.Cmd{Argv: argv, Env: env}
}

func expandHome(s, home string) string {
	switch {
	case s == "~":
		return home
	case strings.HasPrefix(s, "~/"):
		return filepath.Join(home, s[2:])
	}
	return s
}

// Release is a program published as a binary on a GitHub release, verified by the checksum file
// the project publishes beside it.
type Release struct {
	Repo string // owner/repo; makeit installs its newest release, looked up at each run
	Bin  string // the program's name inside the archive and in ~/.local/bin
	// Asset names the archive and its checksum file for a target; "" when the project ships
	// nothing for it.
	Asset func(v, goos, goarch string) (asset, sums string)
}

// URL is where name of release version is downloaded from, under base (https://github.com).
func (r Release) URL(base, version, name string) string {
	return base + "/" + r.Repo + "/releases/download/v" + version + "/" + name
}

// Ships reports whether the project publishes an archive for this target (the names do not
// depend on the version's value).
func (r Release) Ships(goos, goarch string) bool {
	asset, _ := r.Asset("0.0.0", goos, goarch)
	return asset != ""
}

// Into is the folder this route installs programs into, which is often not on PATH: go install
// into $GOBIN, else $GOPATH's first entry /bin, else ~/go/bin; rustup into $CARGO_HOME/bin, else
// ~/.cargo/bin. "" for routes that install onto PATH.
func (v Via) Into(getenv func(string) string) string {
	home := cmp.Or(getenv("HOME"), getenv("USERPROFILE"))
	switch v.Bin {
	case "go":
		if d := getenv("GOBIN"); d != "" {
			return d
		}
		if gp, _, _ := strings.Cut(getenv("GOPATH"), string(filepath.ListSeparator)); gp != "" {
			return filepath.Join(gp, "bin")
		}
		if home != "" {
			return filepath.Join(home, "go", "bin")
		}
	case "rustup":
		if d := getenv("CARGO_HOME"); d != "" {
			return filepath.Join(d, "bin")
		}
		if home != "" {
			return filepath.Join(home, ".cargo", "bin")
		}
	}
	return ""
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
		Bins: []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome", "Google Chrome"},
		Pkgs: map[PM][]string{Brew: {"--cask", "google-chrome"}, WinGet: {"Google.Chrome"}},
		// chrome-devtools-mcp supports Google Chrome and Chrome for Testing only, not chromium.
		PkgFile: map[PM]string{APT: "https://dl.google.com/linux/direct/google-chrome-stable_current_amd64.deb",
			DNF: "https://dl.google.com/linux/direct/google-chrome-stable_current_x86_64.rpm"},
		Manual: "Google Chrome or Chrome for Testing: https://www.google.com/chrome/",
	},
	{
		ID: "prek", Why: "the commit hook runner (reads .pre-commit-config.yaml, no Python needed)", Bins: []string{"prek"},
		Pkgs:    map[PM][]string{Brew: {"prek"}, WinGet: {"j178.prek"}},
		Release: &Release{Repo: "j178/prek", Bin: "prek", Asset: prekAsset},
		Manual:  "a release binary from github.com/j178/prek",
	},
	{
		ID: "deja-vu", Why: "the memory plugin's local search binary", Bins: []string{"deja"},
		Pkgs:    map[PM][]string{Brew: {"deja-vu"}},
		Release: &Release{Repo: "vshulcz/deja-vu", Bin: "deja", Asset: goreleaserAsset("deja-vu", "checksums.txt", true)},
		Manual:  "a release binary from github.com/vshulcz/deja-vu",
	},
	{
		ID: "grype", Why: "the vulnerability scan, as a local command (CI runs it too)", Bins: []string{"grype"},
		Pkgs:    map[PM][]string{Brew: {"grype"}, WinGet: {"Anchore.Grype"}},
		Release: &Release{Repo: "anchore/grype", Bin: "grype", Asset: goreleaserAsset("grype", "grype_%s_checksums.txt", false)},
		Manual:  "a release binary from github.com/anchore/grype",
	},
	// The language servers the bundle's LSP plugins start (LSP-ROWS.md), and marked for the
	// plugin's reply check. npm needs node, a required row, so the npm rows apply everywhere.
	{
		ID: "typescript-language-server", Why: "the typescript-lsp plugin runs it for .ts and .js files", Bins: []string{"typescript-language-server"},
		Pkgs:   map[PM][]string{Brew: {"typescript-language-server"}},
		Via:    []Via{{Bin: "npm", Argv: []string{"npm", "install", "-g", "--prefix", "~/.local", "typescript-language-server", "typescript"}}},
		Manual: "npm install -g typescript-language-server typescript",
	},
	{
		ID: "pyright", Why: "the pyright-lsp plugin runs it for .py files", Bins: []string{"pyright-langserver"},
		Pkgs:   map[PM][]string{Brew: {"pyright"}},
		Via:    []Via{{Bin: "npm", Argv: []string{"npm", "install", "-g", "--prefix", "~/.local", "pyright"}}},
		Manual: "npm install -g pyright",
	},
	{
		ID: "marked", Why: "the makeit plugin's reply check numbers lists the way Claude Code shows them", Bins: []string{"marked"},
		Via:    []Via{{Bin: "npm", Argv: []string{"npm", "install", "-g", "--prefix", "~/.local", "marked"}}},
		Manual: "npm install -g marked",
	},
	{
		ID: "rust-analyzer", Why: "the rust-analyzer-lsp plugin runs it for .rs files", Bins: []string{"rust-analyzer"},
		When:   []string{"cargo", "rustup"},
		Pkgs:   map[PM][]string{Brew: {"rust-analyzer"}, APK: {"rust-analyzer"}, DNF: {"rust-analyzer"}},
		Via:    []Via{{Bin: "rustup", Argv: []string{"rustup", "component", "add", "rust-analyzer"}}},
		Manual: "rustup component add rust-analyzer (it lands in ~/.cargo/bin, which must be on PATH)",
	},
	{
		ID: "gopls", Why: "the gopls-lsp plugin runs it for .go files", Bins: []string{"gopls"},
		When:   []string{"go"},
		Pkgs:   map[PM][]string{Brew: {"gopls"}, APT: {"gopls"}, APK: {"gopls"}},
		Via:    []Via{{Bin: "go", Argv: []string{"go", "install", "golang.org/x/tools/gopls@latest"}, Env: []string{"GOBIN=~/.local/bin"}}},
		Manual: "go install golang.org/x/tools/gopls@latest",
	},
}

// prekAsset names prek's cargo-dist archives: the musl build on Linux (static, so Alpine works).
func prekAsset(_, goos, goarch string) (string, string) {
	cpu := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[goarch]
	triple, ext := map[string]string{"linux": "unknown-linux-musl", "darwin": "apple-darwin", "windows": "pc-windows-msvc"}[goos], "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	if cpu == "" || triple == "" {
		return "", ""
	}
	asset := "prek-" + cpu + "-" + triple + "." + ext
	return asset, asset + ".sha256"
}

// goreleaserAsset names a goreleaser project's archives, <name>_<v>_<os>_<arch>.tar.gz (zip on
// Windows), with sums the checksum file (a %s in it is the version). windowsArm64 says whether
// the project ships that target.
func goreleaserAsset(name, sums string, windowsArm64 bool) func(v, goos, goarch string) (string, string) {
	return func(v, goos, goarch string) (string, string) {
		if !slices.Contains([]string{"linux", "darwin", "windows"}, goos) || !slices.Contains([]string{"amd64", "arm64"}, goarch) {
			return "", ""
		}
		if goos == "windows" && goarch == "arm64" && !windowsArm64 {
			return "", ""
		}
		ext := "tar.gz"
		if goos == "windows" {
			ext = "zip"
		}
		return name + "_" + v + "_" + goos + "_" + goarch + "." + ext, strings.ReplaceAll(sums, "%s", v)
	}
}

// hasCABundle reports a CA bundle on disk, read from the files Go's crypto/x509 reads (and
// SSL_CERT_FILE, SSL_CERT_DIR). It must not call x509.SystemCertPool: Go loads that pool once per
// process, and this probe runs before setup installs ca-certificates, so it would cache an empty
// pool for every download the same run makes afterward.
func hasCABundle() bool { return caBundleIn(fetch.BundlePaths(os.Getenv)) }

// caBundleIn reports whether one of paths holds a certificate.
func caBundleIn(paths []string) bool {
	for _, p := range paths {
		if b, err := os.ReadFile(p); err == nil && strings.Contains(string(b), "BEGIN CERTIFICATE") {
			return true
		}
	}
	return false
}

// State is one row checked on this machine.
type State struct {
	Prereq  Prereq
	Present bool
	// Via is the first toolchain route whose program is on PATH, or nil.
	Via []string
}

// Status checks every row that applies to p.
func Status(p Platform, present func(string) bool) []State {
	var out []State
	for _, r := range Prereqs {
		if !r.Applies(p) || (len(r.When) > 0 && !slices.ContainsFunc(r.When, present)) {
			continue
		}
		var via []string
		if i := slices.IndexFunc(r.Via, func(v Via) bool { return present(v.Bin) }); i >= 0 {
			via = r.Via[i].Argv
		}
		ok := false
		if r.Present != nil {
			ok = r.Present()
		} else {
			ok = slices.ContainsFunc(r.Bins, present)
		}
		out = append(out, State{Prereq: r, Present: ok, Via: via})
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
	Name string // brew, winget, apt, dnf, apk, native
	Argv []string
	Note string
}

// ClaudeRoutes lists the routes for p in preference order: a package manager that updates
// Claude Code, then the signed repository of the distribution's package manager, then the
// native installer, which runs a downloaded script. npm is not offered (DESIGN.md 4.9).
func ClaudeRoutes(p Platform, present func(string) bool) []Route {
	var rs []Route
	if (p.OS == "darwin" || p.OS == "linux") && present("brew") {
		rs = append(rs, Route{Name: "brew", Argv: []string{"brew", "install", "--cask", "claude-code@latest"}})
	}
	if p.OS == "windows" && present("winget") {
		rs = append(rs, Route{Name: "winget", Argv: append([]string{"winget", "install", "--exact", "--id", "Anthropic.ClaudeCode"}, wingetAccept...)})
	}
	if script, ok := RepoScript(p.PM); ok && p.OS == "linux" {
		rs = append(rs, Route{Name: string(p.PM), Argv: []string{"sh", "-c", script}, Note: "signed repository, latest channel"})
	}
	if p.OS == "windows" {
		rs = append(rs, Route{Name: "native", Argv: []string{"powershell", "-NoProfile", "-Command", "irm https://claude.ai/install.ps1 | iex"}})
	} else {
		rs = append(rs, Route{Name: "native", Argv: []string{"bash", "-c", "curl -fsSL https://claude.ai/install.sh | bash"}})
	}
	return rs
}

// Text is the route's command as a person would type it: the script itself for a shell route,
// so doctor shows exactly what setup runs.
func (r Route) Text() string {
	if len(r.Argv) == 3 && (r.Argv[0] == "sh" || r.Argv[0] == "bash") && r.Argv[1] == "-c" {
		return r.Argv[2]
	}
	return execx.Quote(r.Argv)
}

// The Claude Code release signing key (setup.md 363-460). A key that fails its check makes a
// repository script exit 3, which is never answered with the native installer. The gpg check
// accepts the file only when it holds exactly one primary key and that key's fingerprint is the
// pin: a file with the pinned key plus another would make apt trust both for the repository,
// and rpm trust both system-wide.
const (
	keyFingerprint = "31DDDE24DDFAB679F42D7BD2BAA929FF1A7ECACE"
	apkKeySha256   = "395759c1f7449ef4cdef305a42e820f3c766d6090d142634ebdb049f113168b6"
	gpgCheck       = `gpg --show-keys --with-colons "$key" 2>/dev/null | awk -F: '$1 == "pub" { n++; want = 1; next } $1 == "fpr" && want { fpr = $10; want = 0 } END { exit !(n == 1 && fpr == "` + keyFingerprint + `") }' || { echo "makeit: the Claude Code signing key is not the pinned key alone" >&2; exit 3; }`
)

// RepoScript is the shell script that installs Claude Code from the signed repository of pm
// on the latest channel (setup.md 363-460). doctor shows it as is and setup runs it, with sudo
// as a shell function so --yes can add -n. ok is false for a manager with no repository.
func RepoScript(pm PM) (script string, ok bool) {
	head := "set -e\nexport GNUPGHOME=\"$(mktemp -d)\"\nkey=\"$(mktemp)\"\ntrap 'rm -rf \"$GNUPGHOME\" \"$key\"' EXIT\n"
	switch pm {
	case APT:
		return head + `command -v curl >/dev/null && command -v gpg >/dev/null || { sudo apt-get update && sudo apt-get install -y curl gnupg; }
curl -fsSL https://downloads.claude.ai/keys/claude-code.asc -o "$key"
` + gpgCheck + `
sudo install -d -m 0755 /etc/apt/keyrings
sudo install -m 0644 "$key" /etc/apt/keyrings/claude-code.asc
echo 'deb [signed-by=/etc/apt/keyrings/claude-code.asc] https://downloads.claude.ai/claude-code/apt/latest latest main' | sudo tee /etc/apt/sources.list.d/claude-code.list >/dev/null
sudo apt-get update
sudo apt-get install -y claude-code
`, true
	case DNF:
		return head + `command -v gpg >/dev/null || sudo dnf install -y gnupg2
curl -fsSL https://downloads.claude.ai/keys/claude-code.asc -o "$key"
` + gpgCheck + `
sudo install -d -m 0755 /etc/pki/rpm-gpg
sudo install -m 0644 "$key" /etc/pki/rpm-gpg/claude-code.asc
sudo rpm --import /etc/pki/rpm-gpg/claude-code.asc
printf '%s\n' '[claude-code]' 'name=Claude Code' 'baseurl=https://downloads.claude.ai/claude-code/rpm/latest' 'enabled=1' 'gpgcheck=1' 'gpgkey=file:///etc/pki/rpm-gpg/claude-code.asc' | sudo tee /etc/yum.repos.d/claude-code.repo >/dev/null
sudo dnf install -y claude-code
`, true
	case APK:
		return head + `wget -qO "$key" https://downloads.claude.ai/keys/claude-code.rsa.pub
echo "` + apkKeySha256 + `  $key" | sha256sum -c - >/dev/null 2>&1 || { echo "makeit: the Claude Code signing key has the wrong sha256" >&2; exit 3; }
sudo install -m 0644 "$key" /etc/apk/keys/claude-code.rsa.pub
grep -qx 'https://downloads.claude.ai/claude-code/apk/latest' /etc/apk/repositories || echo 'https://downloads.claude.ai/claude-code/apk/latest' | sudo tee -a /etc/apk/repositories >/dev/null
sudo apk update
sudo apk add claude-code
`, true
	}
	return "", false
}

// AutoRoute is the first package route; false when only native is left.
func AutoRoute(p Platform, present func(string) bool) (Route, bool) {
	r := ClaudeRoutes(p, present)[0]
	return r, r.Name != "native"
}
