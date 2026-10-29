package claudecli

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func probe(goos string, onPath string, npmPrefix string, files ...string) Probe {
	return Probe{
		GOOS:      goos,
		Home:      "/home/u",
		LookPath:  func(string) (string, bool) { return onPath, onPath != "" },
		Exists:    func(p string) bool { return slices.Contains(files, p) },
		NpmPrefix: func() string { return npmPrefix },
	}
}

func TestLocate(t *testing.T) {
	native := filepath.Join("/home/u", ".local", "bin", "claude")
	cases := []struct {
		name string
		p    Probe
		want []Install
	}{
		{"linux, every source, PATH first", probe("linux", "/usr/bin/claude", "/home/u/.npm-global",
			native, "/home/linuxbrew/.linuxbrew/bin/claude", "/home/u/.npm-global/bin/claude"),
			[]Install{{"/usr/bin/claude", "path", true}, {native, "native", false},
				{"/home/linuxbrew/.linuxbrew/bin/claude", "brew", false}, {"/home/u/.npm-global/bin/claude", "npm", false}}},
		{"PATH entry that is the native launcher is listed once, as native", probe("linux", native, "", native),
			[]Install{{native, "native", true}}},
		{"PATH entry that is a Homebrew link keeps brew", probe("darwin", "/opt/homebrew/bin/claude", "", "/opt/homebrew/bin/claude"),
			[]Install{{"/opt/homebrew/bin/claude", "brew", true}}},
		{"darwin checks both Homebrew prefixes", probe("darwin", "", "",
			"/opt/homebrew/bin/claude", "/usr/local/bin/claude"),
			[]Install{{"/opt/homebrew/bin/claude", "brew", false}, {"/usr/local/bin/claude", "brew", false}}},
		{"windows: claude.exe launcher, npm shim directly in the prefix, no brew", probe("windows", "", "/appdata/npm",
			native+".exe", "/appdata/npm/claude.cmd", "/home/linuxbrew/.linuxbrew/bin/claude"),
			[]Install{{native + ".exe", "native", false}, {"/appdata/npm/claude.cmd", "npm", false}}},
		{"nothing installed and no npm", probe("linux", "", ""), nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Locate(c.p); !slices.Equal(got, c.want) {
				t.Errorf("Locate = %v, want %v", got, c.want)
			}
		})
	}
}

func TestMachineProbe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts stand in for claude and npm")
	}
	bin, home, prefix := t.TempDir(), t.TempDir(), t.TempDir()
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(bin, "claude"), "#!/bin/sh\n")
	// The fake npm records its arguments: a plain `npm prefix -g` writes a debug log under
	// ~/.npm/_logs and may check the registry, and doctor writes nothing.
	npmArgs := filepath.Join(t.TempDir(), "npm.args")
	write(filepath.Join(bin, "npm"), "#!/bin/sh\nprintf '%s\\n' \"$@\" > "+npmArgs+"\necho "+prefix+"\n")
	write(filepath.Join(prefix, "bin", "claude"), "#!/bin/sh\n")
	// The native launcher is a symlink into versions/ (setup.md), so Exists must follow links.
	write(filepath.Join(home, ".local", "share", "claude", "versions", "2.1.311"), "#!/bin/sh\n")
	if err := os.MkdirAll(filepath.Join(home, ".local", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, ".local", "share", "claude", "versions", "2.1.311"),
		filepath.Join(home, ".local", "bin", "claude")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "notafile", "claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "gone"), filepath.Join(home, "dangling")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	got := Locate(MachineProbe(context.Background(), home))
	want := []Install{{filepath.Join(bin, "claude"), "path", true}, {filepath.Join(home, ".local", "bin", "claude"), "native", false},
		{filepath.Join(prefix, "bin", "claude"), "npm", false}}
	if !slices.Equal(got, want) {
		t.Errorf("Locate(MachineProbe) = %v, want %v", got, want)
	}
	args, err := os.ReadFile(npmArgs)
	if err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"--logs-max=0", "--no-update-notifier"} {
		if !slices.Contains(strings.Fields(string(args)), flag) {
			t.Errorf("npm ran with %q, without %s", args, flag)
		}
	}
	p := MachineProbe(context.Background(), home)
	if p.Exists(filepath.Join(home, "notafile", "claude")) {
		t.Error("a directory named claude counts as a binary")
	}
	if p.Exists(filepath.Join(home, "dangling")) {
		t.Error("a symlink to a removed version counts as a binary")
	}
}
