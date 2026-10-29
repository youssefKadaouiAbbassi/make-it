package platform

import (
	"slices"
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
		{Platform{OS: "darwin"}, []string{"brew", "npm"}, []string{"brew", "npm", "native"}},
		{Platform{OS: "linux", PM: APT}, []string{"apt-get", "brew"}, []string{"brew", "native"}},
		{Platform{OS: "linux", PM: APT}, nil, []string{"native"}},
		{Platform{OS: "windows", PM: WinGet}, []string{"winget", "npm"}, []string{"winget", "npm", "native"}},
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
	npm := ClaudeRoutes(Platform{OS: "linux"}, only("npm"))[0]
	if npm.Name != "npm" || !slices.Equal(npm.Argv, []string{"npm", "install", "-g", "@anthropic-ai/claude-code@latest"}) {
		t.Errorf("npm route %+v, never under sudo", npm)
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
