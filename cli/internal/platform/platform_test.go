package platform

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func only(names ...string) func(string) bool {
	return func(n string) bool { return slices.Contains(names, n) }
}

func TestDetect(t *testing.T) {
	cases := []struct {
		goos, goarch string
		present      []string
		env          map[string]string
		want         Platform
	}{
		{"linux", "amd64", []string{"dnf"}, nil, Platform{OS: "linux", Arch: "amd64", PM: DNF}},
		{"linux", "arm64", []string{"apt-get", "brew"}, map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}, Platform{OS: "linux", Arch: "arm64", PM: APT, WSL: true}},
		{"linux", "amd64", nil, map[string]string{"WSL_INTEROP": "/run/x"}, Platform{OS: "linux", Arch: "amd64", PM: None, WSL: true}},
		{"darwin", "arm64", nil, map[string]string{"WSL_DISTRO_NAME": "x"}, Platform{OS: "darwin", Arch: "arm64", PM: None}},
		{"windows", "amd64", []string{"scoop"}, nil, Platform{OS: "windows", Arch: "amd64", PM: Scoop}},
		{"windows", "amd64", []string{"scoop", "winget"}, nil, Platform{OS: "windows", Arch: "amd64", PM: WinGet}},
	}
	for _, c := range cases {
		got := Detect(c.goos, c.goarch, only(c.present...), func(k string) string { return c.env[k] })
		if got != c.want {
			t.Errorf("Detect(%s, %v, %v) = %+v, want %+v", c.goos, c.present, c.env, got, c.want)
		}
	}
}

func TestInstallCommand(t *testing.T) {
	cases := []struct {
		name              string
		pm                PM
		id                string
		interactive, root bool
		argv, env         []string
		ok                bool
	}{
		{"apt non-interactive uses sudo -n and env", APT, "git", false, false,
			[]string{"sudo", "-n", "env", "DEBIAN_FRONTEND=noninteractive", "apt-get", "install", "-y", "git"}, nil, true},
		{"apt interactive lets sudo prompt", APT, "node", true, false,
			[]string{"sudo", "env", "DEBIAN_FRONTEND=noninteractive", "apt-get", "install", "-y", "nodejs", "npm"}, nil, true},
		{"apt as root needs no sudo", APT, "jq", false, true,
			[]string{"apt-get", "install", "-y", "jq"}, []string{"DEBIAN_FRONTEND=noninteractive"}, true},
		{"pacman never asks", Pacman, "gh", false, false,
			[]string{"sudo", "-n", "pacman", "-S", "--noconfirm", "github-cli"}, nil, true},
		{"brew never uses sudo", Brew, "chrome", false, false,
			[]string{"brew", "install", "--cask", "google-chrome"}, nil, true},
		{"winget carries the accept flags", WinGet, "git", true, false,
			[]string{"winget", "install", "--exact", "--id", "Git.Git", "--accept-source-agreements", "--accept-package-agreements", "--disable-interactivity"}, nil, true},
		{"no route for the manager", APT, "prek", false, false, nil, nil, false},
		{"no manager", None, "git", false, false, nil, nil, false},
	}
	for _, c := range cases {
		cmd, ok := InstallCommand(Find(c.id), c.pm, c.interactive, c.root)
		if ok != c.ok || !slices.Equal(cmd.Argv, c.argv) || !slices.Equal(cmd.Env, c.env) || cmd.Inherit != c.interactive && ok {
			t.Errorf("%s: got %v %v %v inherit=%v", c.name, cmd.Argv, cmd.Env, ok, cmd.Inherit)
		}
	}
}

func TestTable(t *testing.T) {
	for _, p := range Prereqs {
		if p.ID == "" || p.Why == "" || (len(p.Bins) == 0) == (p.Present == nil) {
			t.Errorf("row %q needs an id, a why, and exactly one of bins or Present", p.ID)
		}
	}
	for _, id := range []string{"git", "node", "jq", "ca-certificates"} {
		if !Find(id).Required {
			t.Errorf("%s must be required", id)
		}
	}
}

func TestApplies(t *testing.T) {
	ca := Find("ca-certificates")
	if !ca.Applies(Platform{OS: "linux"}) || ca.Applies(Platform{OS: "darwin"}) || ca.Applies(Platform{OS: "windows"}) {
		t.Error("ca-certificates is a Linux row only")
	}
	if !Find("git").Applies(Platform{OS: "windows"}) {
		t.Error("git applies everywhere")
	}
}

func TestStatus(t *testing.T) {
	p := Platform{OS: "linux", PM: APT}
	got := Status(p, only("git", "chromium"))
	byID := map[string]bool{}
	for _, s := range got {
		byID[s.Prereq.ID] = s.Present
	}
	if !byID["git"] || byID["jq"] || !byID["chrome"] {
		t.Errorf("status %v", byID)
	}
	if _, ok := byID["ca-certificates"]; !ok {
		t.Error("ca-certificates missing on linux")
	}
	if _, ok := byID["ca-certificates"]; ok && len(Status(Platform{OS: "darwin"}, only())) != len(got)-1 {
		t.Error("ca-certificates must be skipped off linux")
	}
}

func TestClaudeRoutes(t *testing.T) {
	names := func(rs []Route) (out []string) {
		for _, r := range rs {
			out = append(out, r.Name)
		}
		return out
	}
	cases := []struct {
		p       Platform
		present []string
		want    []string
	}{
		{Platform{OS: "darwin"}, []string{"brew", "npm"}, []string{"brew", "native"}},
		{Platform{OS: "linux", PM: APT}, []string{"apt-get", "brew", "npm"}, []string{"brew", "apt", "native"}},
		{Platform{OS: "linux", PM: APT}, nil, []string{"apt", "native"}},
		{Platform{OS: "linux", PM: DNF}, nil, []string{"dnf", "native"}},
		{Platform{OS: "linux", PM: APK}, nil, []string{"apk", "native"}},
		{Platform{OS: "linux", PM: Pacman}, []string{"npm"}, []string{"native"}},
		{Platform{OS: "windows", PM: WinGet}, []string{"winget", "npm"}, []string{"winget", "native"}},
	}
	for _, c := range cases {
		if got := names(ClaudeRoutes(c.p, only(c.present...))); !slices.Equal(got, c.want) {
			t.Errorf("%+v %v: %v, want %v", c.p, c.present, got, c.want)
		}
	}
	mac := ClaudeRoutes(Platform{OS: "darwin"}, only("brew"))
	if !slices.Equal(mac[0].Argv, []string{"brew", "install", "--cask", "claude-code@latest"}) {
		t.Errorf("brew route %v", mac[0].Argv)
	}
	win := ClaudeRoutes(Platform{OS: "windows"}, only("winget"))
	if !slices.Equal(win[0].Argv, []string{"winget", "install", "--exact", "--id", "Anthropic.ClaudeCode", "--accept-source-agreements", "--accept-package-agreements", "--disable-interactivity"}) {
		t.Errorf("winget route %v", win[0].Argv)
	}
	apt := ClaudeRoutes(Platform{OS: "linux", PM: APT}, only())[0]
	if script, _ := RepoScript(APT); !slices.Equal(apt.Argv, []string{"sh", "-c", script}) {
		t.Errorf("apt route %v, want the repository script", apt.Argv)
	}
}

func TestRepoScript(t *testing.T) {
	const fpr = "31DDDE24DDFAB679F42D7BD2BAA929FF1A7ECACE"
	for pm, want := range map[PM][]string{
		APT: {"https://downloads.claude.ai/keys/claude-code.asc", fpr, "exit 3",
			"deb [signed-by=/etc/apt/keyrings/claude-code.asc] https://downloads.claude.ai/claude-code/apt/latest latest main",
			"/etc/apt/sources.list.d/claude-code.list", "sudo apt-get install -y claude-code"},
		DNF: {"https://downloads.claude.ai/keys/claude-code.asc", fpr, "exit 3", "sudo rpm --import /etc/pki/rpm-gpg/claude-code.asc",
			"baseurl=https://downloads.claude.ai/claude-code/rpm/latest", "gpgcheck=1", "gpgkey=file:///etc/pki/rpm-gpg/claude-code.asc",
			"sudo dnf install -y claude-code"},
		APK: {"https://downloads.claude.ai/keys/claude-code.rsa.pub", "395759c1f7449ef4cdef305a42e820f3c766d6090d142634ebdb049f113168b6",
			"exit 3", "https://downloads.claude.ai/claude-code/apk/latest", "sudo apk add claude-code"},
	} {
		script, ok := RepoScript(pm)
		if !ok {
			t.Fatalf("%s: no script", pm)
		}
		for _, w := range want {
			if !strings.Contains(script, w) {
				t.Errorf("%s script lacks %q", pm, w)
			}
		}
		if strings.Contains(script, "/stable") {
			t.Errorf("%s script uses the stable channel", pm)
		}
		if strings.Contains(script, "gpgkey=https") {
			t.Errorf("%s: dnf would fetch and import a key the script never checked", pm)
		}
	}
	if _, ok := RepoScript(Pacman); ok {
		t.Error("pacman has no signed repository")
	}
}

func TestAutoRoute(t *testing.T) {
	if r, ok := AutoRoute(Platform{OS: "linux"}, only()); ok {
		t.Errorf("native is never chosen automatically, got %+v", r)
	}
	if r, ok := AutoRoute(Platform{OS: "darwin"}, only("brew")); !ok || r.Name != "brew" {
		t.Errorf("got %+v %v", r, ok)
	}
}

// The repository scripts' key check, run by a real shell and a real gpg on the real Claude Code
// key (testdata/claude-code.asc, from downloads.claude.ai) and a throwaway key: only the pinned
// key alone may pass. A file with the pinned key plus another would make apt trust both for the
// repository and rpm trust both system-wide.
func TestRepoKeyCheck(t *testing.T) {
	if _, err := exec.LookPath("gpg"); err != nil || runtime.GOOS == "windows" {
		t.Skip("needs sh and gpg")
	}
	real, err := os.ReadFile("testdata/claude-code.asc")
	if err != nil {
		t.Fatal(err)
	}
	other, err := os.ReadFile("testdata/other-key.asc")
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string][]byte{
		"pinned key":            real,
		"pinned key plus other": append(append([]byte{}, real...), other...),
		"other key":             other,
		"html page":             []byte("<html>not a key</html>\n"),
	}
	for _, pm := range []PM{APT, DNF} {
		script, _ := RepoScript(pm)
		for name, key := range keys {
			bin := t.TempDir()
			fixture := filepath.Join(bin, "key.fixture")
			if err := os.WriteFile(fixture, key, 0o644); err != nil {
				t.Fatal(err)
			}
			// curl -fsSL <url> -o <file>: the fake copies the fixture to <file>.
			if err := os.WriteFile(filepath.Join(bin, "curl"), []byte("#!/bin/sh\ncp "+fixture+" \"$4\"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			// The check comes before every sudo step, so reaching sudo means the key passed.
			guard := "sudo() { exit 9; }\n"
			cmd := exec.Command("sh", "-c", guard+script)
			cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
			err := cmd.Run()
			code := 0
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				code = ee.ExitCode()
			}
			want := 3
			if name == "pinned key" {
				want = 9
			}
			if code != want {
				t.Errorf("%s, %s: exit %d, want %d", pm, name, code, want)
			}
		}
	}
}
